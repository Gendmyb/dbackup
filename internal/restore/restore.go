// Package restore 从备份 tar 恢复:校验 → 落数据(保留属主/权限)→ 调整 compose →
// `docker compose pull && up -d` → 报告。
package restore

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"dbackup/internal/compose"
	"dbackup/internal/config"
	"dbackup/internal/dc"
	"dbackup/internal/manifest"
	"dbackup/internal/tarx"
	"gopkg.in/yaml.v3"
)

// Options 是 restore 的运行参数。
type Options struct {
	Tar        string
	DataDir    string   // 数据落盘根;空=按原始绝对路径
	Project    string   // compose project;空=manifest 记录的
	ComposeOut string   // 只导出调整后 compose,不部署
	DryRun     bool
	NoPull     bool
	Config     *config.Config
	// StartAll: true 时 up 后全部保持运行(不匹配备份状态);缺省 false =
	// 恢复后运行状态与备份时一致(备份时停着的恢复后仍停)。
	StartAll   bool
}

// Report 是恢复结果。
type Report struct {
	Manifest   *manifest.Manifest
	DataIndex  manifest.DataIndex
	Compose    *compose.Doc
	Project    string
	Deployed   bool
	Actions    []string // dry-run 时列出将执行的动作
	ServiceOut string   // up 之后的容器状态输出
	MatchedStopped []string // 默认行为: up 后按备份状态保持停止的服务 key
}

// Run 执行恢复。client 在部署路径(非 dry-run/compose-out)时必需。
func Run(client *dc.Client, in Options) (*Report, error) {
	man, idx, doc, rootDir, err := loadArchive(in.Tar)
	if err != nil {
		return nil, err
	}
	if in.Config == nil {
		in.Config = &config.Config{}
	}
	rep := &Report{Manifest: man, DataIndex: idx, Compose: doc}

	project := in.Project
	if project == "" {
		project = man.Project
	}
	if project == "" {
		project = "restored"
	}
	rep.Project = project

	// 数据目标路径。
	targets := dataTargets(idx, in.DataDir)
	// 调整 compose(bind 源路径重写 + restore_compose 覆盖)。
	adjustCompose(doc, targets, idx, in.Config)

	if in.ComposeOut != "" {
		if err := os.WriteFile(in.ComposeOut, composeBytes(doc), 0o644); err != nil {
			return nil, err
		}
	}
	if in.DryRun {
		rep.Actions = planActions(in, man, idx, targets, project)
		return rep, nil
	}
	if client == nil {
		return nil, fmt.Errorf("部署需要一个可用的 docker daemon(--host)")
	}

	// 解包所有数据到工作目录(单遍)。
	work, err := os.MkdirTemp("", "dbackup-work-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if err := extractAllData(in.Tar, rootDir, work); err != nil {
		return nil, err
	}

	// 落数据:bind → 绝对路径;volume → 先建卷再拷入 _data。
	if err := placeBinds(work, idx, targets); err != nil {
		return nil, err
	}
	if err := placeVolumes(client, work, idx); err != nil {
		return nil, err
	}

	// 导出调整后 compose 到工作目录(供 docker compose 使用)。
	composePath := filepath.Join(work, "docker-compose.yml")
	if err := os.WriteFile(composePath, composeBytes(doc), 0o644); err != nil {
		return nil, err
	}

	// 部署。
	if err := deploy(in, client, composePath, work, project); err != nil {
		return rep, err
	}
	// 默认:compose up -d 会把所有服务拉起;这里把"备份时未运行"的服务停掉,
	// 使恢复后的运行状态与备份时一致(备份时停着的恢复后仍停着)。--start-all 关闭此步。
	if !in.StartAll {
		rep.MatchedStopped = applyMatchState(client, man)
	}
	rep.Deployed = true
	return rep, nil
}

// ---- 读取 tar(流式,只读需要的文件) ----

func loadArchive(tarPath string) (*manifest.Manifest, manifest.DataIndex, *compose.Doc, string, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return nil, manifest.DataIndex{}, nil, "", err
	}
	defer f.Close()
	tr := tar.NewReader(f)

	var rootDir string
	var manB, idxB, composeB []byte
	for {
		hdr, herr := tr.Next()
		if herr == io.EOF {
			break
		}
		if herr != nil {
			return nil, manifest.DataIndex{}, nil, "", herr
		}
		name := hdr.Name
		if strings.HasSuffix(name, "/") {
			if rootDir == "" {
				rootDir = strings.TrimSuffix(name, "/")
			}
			continue
		}
		switch {
		case name == rootDir+"/manifest.json":
			manB, _ = io.ReadAll(tr)
		case name == rootDir+"/data/index.json":
			idxB, _ = io.ReadAll(tr)
		case name == rootDir+"/docker-compose.yml":
			composeB, _ = io.ReadAll(tr)
		}
	}
	if rootDir == "" {
		return nil, manifest.DataIndex{}, nil, "", fmt.Errorf("tar 里找不到根目录")
	}
	if manB == nil {
		return nil, manifest.DataIndex{}, nil, "", fmt.Errorf("tar 里找不到 manifest.json")
	}
	var man manifest.Manifest
	if err := json.Unmarshal(manB, &man); err != nil {
		return nil, manifest.DataIndex{}, nil, "", fmt.Errorf("解析 manifest: %w", err)
	}
	var idx manifest.DataIndex
	if idxB != nil {
		if err := json.Unmarshal(idxB, &idx); err != nil {
			return nil, manifest.DataIndex{}, nil, "", fmt.Errorf("解析 index: %w", err)
		}
	}
	if composeB == nil {
		return nil, manifest.DataIndex{}, nil, "", fmt.Errorf("tar 里找不到 docker-compose.yml")
	}
	var doc compose.Doc
	if err := yaml.Unmarshal(composeB, &doc); err != nil {
		return nil, manifest.DataIndex{}, nil, "", fmt.Errorf("解析 compose: %w", err)
	}
	return &man, idx, &doc, rootDir, nil
}

// extractAllData 单遍解包 tar 里 <rootDir>/data/ 下全部条目到 work/<entry-id>/。
func extractAllData(tarPath, rootDir, work string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	_, _, err = tarx.Extract(tr, rootDir+"/data", work)
	return err
}

// ---- 数据落盘 ----

func dataTargets(idx manifest.DataIndex, dataDir string) map[string]string {
	out := map[string]string{}
	for _, e := range idx.Entries {
		if e.Kind != "bind" {
			continue
		}
		if dataDir == "" {
			out[e.ID] = e.Source
		} else {
			out[e.ID] = filepath.Join(dataDir, e.Source)
		}
	}
	return out
}

func placeBinds(work string, idx manifest.DataIndex, targets map[string]string) error {
	for _, e := range idx.Entries {
		if e.Kind != "bind" {
			continue
		}
		dest := targets[e.ID]
		if dest == "" {
			continue
		}
		src := filepath.Join(work, e.ID)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		if err := copyTree(src, dest); err != nil {
			return fmt.Errorf("落数据 %s → %s: %w", e.ID, dest, err)
		}
	}
	return nil
}

// placeVolumes 对每个命名/匿名卷:按原始卷名建卷 → 取 _data 路径 → 拷数据。
// 关键:adjustCompose 已给每个有数据的顶层卷固定 name 字段,compose 会按原始卷名建卷
// (而非默认的 project 前缀名),所以这里必须往**原始卷名**里导入数据,与 compose 建出的
// 卷一一对应,容器挂载到的才是这份数据。
func placeVolumes(client *dc.Client, work string, idx manifest.DataIndex) error {
	vols, err := client.Volumes()
	if err != nil {
		return fmt.Errorf("枚举卷: %w", err)
	}
	// 先确保所有目标卷存在。
	for _, e := range idx.Entries {
		if e.Kind != "volume" || e.VolumeName == "" {
			continue
		}
		if _, ok := vols[e.VolumeName]; !ok {
			if err := client.CreateVolume(e.VolumeName); err != nil {
				return fmt.Errorf("建卷 %s: %w", e.VolumeName, err)
			}
		}
	}
	vols, err = client.Volumes()
	if err != nil {
		return err
	}
	for _, e := range idx.Entries {
		if e.Kind != "volume" || e.VolumeName == "" {
			continue
		}
		vi, ok := vols[e.VolumeName]
		if !ok || vi.Mountpoint == "" {
			continue
		}
		src := filepath.Join(work, e.ID)
		if st, err := os.Stat(src); err != nil || !st.IsDir() {
			continue // 无数据
		}
		if err := copyTree(src, vi.Mountpoint); err != nil {
			return fmt.Errorf("导入卷 %s: %w", e.VolumeName, err)
		}
	}
	return nil
}

// ---- compose 调整 ----

func adjustCompose(doc *compose.Doc, targets map[string]string, idx manifest.DataIndex, cfg *config.Config) {
	srcToTarget := map[string]string{}
	backedVolumes := map[string]bool{}
	for _, e := range idx.Entries {
		switch e.Kind {
		case "bind":
			if t := targets[e.ID]; t != "" {
				srcToTarget[e.Source] = t
			}
		case "volume":
			if e.VolumeName != "" {
				backedVolumes[e.VolumeName] = true
			}
		}
	}
	// 顶层声明的卷默认会被 compose 加 project 前缀(webapp_db-data),使恢复后的
	// 卷名与原始卷名不一致,且反复 restore→backup 会不断叠加前缀。给每个有数据备份
	// 的卷固定 name 字段,compose 就会按原始名建卷,恢复出的卷名与源机完全一致。
	for k, v := range doc.Volumes {
		if !backedVolumes[k] {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			m = map[string]any{}
		}
		m["name"] = k
		doc.Volumes[k] = m
	}
	// 顶层声明的网络同样会被 compose 加 project 前缀。给每个网络固定 name 字段,
	// 使恢复后容器仍连到**原始网络名**——否则反复 restore→backup 会叠加前缀。
	for k, v := range doc.Networks {
		m, ok := v.(map[string]any)
		if !ok {
			m = map[string]any{}
		}
		m["name"] = k
		doc.Networks[k] = m
	}
	keys := make([]string, 0, len(doc.Services))
	for k := range doc.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		svc := doc.Services[key]
		var nv []string
		for _, v := range svc.Volumes {
			if i := strings.IndexByte(v, ':'); i > 0 {
				src := v[:i]
				if t, ok := srcToTarget[src]; ok && t != src {
					v = t + v[i:]
				}
			}
			nv = append(nv, v)
		}
		svc.Volumes = nv
		if ov, ok := cfg.Containers[key]; ok && ov.RestoreCompose != nil {
			if p, ok := ov.RestoreCompose["ports"].([]any); ok {
				var ports []string
				for _, x := range p {
					if s, ok := x.(string); ok {
						ports = append(ports, s)
					}
				}
				svc.Ports = ports
			}
		}
		doc.Services[key] = svc
	}
}

// ---- 部署 ----

// applyMatchState 让恢复后的运行状态与备份时一致:manifest 里记录了每个服务备份时
// 是否 running,compose up -d 会把它们全拉起,这里把"备份时未运行"的服务停掉。
// 按容器名(= service 名)匹配;停失败不致命,记录并返回。
func applyMatchState(client *dc.Client, man *manifest.Manifest) []string {
	var stopped []string
	if man == nil {
		return stopped
	}
	// 容器名 → 备份时是否运行(缺省 false)。
	runningByName := map[string]bool{}
	for _, s := range man.Services {
		runningByName[s.Name] = s.Running
	}
	containers, err := client.Containers(true)
	if err != nil {
		return stopped
	}
	for _, cs := range containers {
		if len(cs.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(cs.Names[0], "/")
		// 只处理"备份时未运行"的容器;当前在跑的才需要停(已停的跳过)。
		if runningByName[name] || cs.State != "running" {
			continue
		}
		if err := client.Stop(cs.ID, 10); err == nil {
			stopped = append(stopped, name)
		}
	}
	return stopped
}

func deploy(in Options, client *dc.Client, composePath, workDir, project string) error {
	env := append(os.Environ(), "DOCKER_HOST="+client.Endpoint())
	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Env = env
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w\n%s", strings.Join(append([]string{name}, args...), " "), err, string(out))
		}
		return nil
	}
	if !in.NoPull {
		// pull 失败不致命(镜像可能已在本地/离线)。
		_ = run("docker", "compose", "-p", project, "pull")
	}
	return run("docker", "compose", "-p", project, "up", "-d")
}

func planActions(in Options, man *manifest.Manifest, idx manifest.DataIndex, targets map[string]string, project string) []string {
	var a []string
	a = append(a, fmt.Sprintf("compose project: %s", project))
	for _, e := range idx.Entries {
		if e.Kind == "bind" {
			a = append(a, fmt.Sprintf("落数据 %s → %s", e.ID, targets[e.ID]))
		} else {
			a = append(a, fmt.Sprintf("建卷并导入 %s(卷 %s)", e.ID, e.VolumeName))
		}
	}
	if !in.NoPull {
		a = append(a, "docker compose pull")
	}
	a = append(a, "docker compose up -d")
	return a
}

// ---- 工具 ----

func composeBytes(doc *compose.Doc) []byte {
	b, _ := compose.Marshal(doc)
	return b
}

// copyTree 把 src 目录内容复制到 dest,保留属主/权限/时间。
func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dest, rel)
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		switch {
		case d.IsDir():
			if err := os.MkdirAll(t, info.Mode().Perm()); err != nil {
				return err
			}
			os.Chtimes(t, info.ModTime(), info.ModTime())
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			os.Remove(t)
			os.Symlink(target, t)
		default:
			if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
				return err
			}
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			if err := os.WriteFile(t, data, info.Mode().Perm()); err != nil {
				return err
			}
			os.Chtimes(t, info.ModTime(), info.ModTime())
			if sst, ok := info.Sys().(*syscall.Stat_t); ok {
				os.Chown(t, int(sst.Uid), int(sst.Gid))
			}
		}
		return nil
	})
}
