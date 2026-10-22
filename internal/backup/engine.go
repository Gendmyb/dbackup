package backup

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dbackup/internal/config"
	"dbackup/internal/compose"
	"dbackup/internal/dc"
	"dbackup/internal/manifest"
	"dbackup/internal/tarx"
)

// ToolVersion 是写入 manifest 的工具版本(可用 ldflags 覆盖)。
var ToolVersion = "0.1.0"

// Options 是 backup 的运行参数。
type Options struct {
	OutDir       string
	Name         string
	Mode         string // "cold" | "live";缺省 cold
	StopTimeout  int
	Keep         int
	LeaveStopped bool
	DryRun       bool
	Config       *config.Config
	Now          time.Time
}

// Result 是 backup 的产物。
type Result struct {
	TarPath  string
	Manifest *manifest.Manifest
	Compose  *compose.Doc
	Warnings []string
	Stopped  []string // 本次被停止(且已拉起)的容器名
}

func defaultOpts(o *Options) Options {
	if o == nil {
		o = &Options{}
	}
	out := *o
	if out.OutDir == "" {
		out.OutDir = "backups"
	}
	if out.Mode == "" {
		out.Mode = "cold"
	}
	if out.StopTimeout == 0 {
		out.StopTimeout = 10
	}
	if out.Now.IsZero() {
		out.Now = time.Now()
	}
	if out.Name == "" && out.Config != nil && out.Config.Name != "" {
		out.Name = out.Config.Name
	}
	if out.Keep == 0 && out.Config != nil {
		out.Keep = out.Config.Keep
	}
	if out.StopTimeout == 10 && out.Config != nil && out.Config.StopTimeout > 0 {
		out.StopTimeout = out.Config.StopTimeout
	}
	if out.OutDir == "backups" && out.Config != nil && out.Config.Out != "" {
		out.OutDir = out.Config.Out
	}
	return out
}

// Run 执行一次备份。client 已连接。
func Run(client *dc.Client, in Options) (*Result, error) {
	opts := defaultOpts(&in)
	if opts.Config == nil {
		opts.Config = &config.Config{}
	}

	// 1. 枚举全部容器。
	all, err := client.Containers(true)
	if err != nil {
		return nil, fmt.Errorf("枚举容器: %w", err)
	}
	nameToID := map[string]string{}
	idToName := map[string]string{}
	var names []string
	for _, s := range all {
		if len(s.Names) == 0 {
			continue
		}
		dn := strings.TrimPrefix(s.Names[0], "/")
		nameToID[dn] = s.ID
		idToName[s.ID] = dn
		names = append(names, dn)
	}
	sort.Strings(names)

	// 2. 选定集合(include/exclude)。
	include, exclude := nameSets(opts.Config.Include, opts.Config.Exclude)
	selected := []string{}
	for _, n := range names {
		if opts.Config.IsAll() {
			if exclude[n] {
				continue
			}
			selected = append(selected, n)
			continue
		}
		if include[n] && !exclude[n] {
			selected = append(selected, n)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("没有选中任何容器(include=%v exclude=%v)", opts.Config.Include, opts.Config.Exclude)
	}

	// 3. 逐一 inspect(备份前状态)。
	sel := &Selection{
		Containers: map[string]dc.Inspect{},
		ServiceKey: map[string]string{},
	}
	var inputs []compose.ContainerInput
	var preState map[string]bool // 显示名 → 备份前是否 running
	{
		preState = map[string]bool{}
		for _, n := range selected {
			insp, err := client.Inspect(nameToID[n])
			if err != nil {
				return nil, fmt.Errorf("inspect %s: %w", n, err)
			}
			sel.Containers[n] = insp
			inputs = append(inputs, compose.ContainerInput{Name: n, Insp: insp})
			if insp.State != nil {
				preState[n] = insp.State.Running
			}
		}
	}

	// 4. 生成 compose + service key 映射 + 警告。
	doc, keys, warns := compose.Generate(inputs)
	for n, k := range keys {
		sel.ServiceKey[n] = k
	}
	// 推断 project:优先 compose 标签。
	sel.Project = inferProject(selected, sel.Containers)
	if sel.Project == "" {
		sel.Project = hostname()
	}

	// 5. 卷表(用于数据条目 mountpoint/anonymous)。
	volumes, err := client.Volumes()
	if err != nil {
		return nil, fmt.Errorf("枚举卷: %w", err)
	}

	// 6. 规划数据条目。
	entries := PlanData(sel, opts.Config.Containers, volumes)

	// 7. 依赖与拓扑序。
	deps := collectDeps(selected, opts.Config, sel.ServiceKey)
	topo := topoOrder(selected, deps)
	stopOrder := reverse(topo) // 先停依赖方,再停被依赖方

	// 8. 冷备份:逆拓扑停容器(记录被停者)。
	var stopped []string
	if opts.Mode == "cold" {
		stopped = stopContainers(client, nameToID, stopOrder, preState, opts.StopTimeout)
		defer func() {
			if opts.LeaveStopped || opts.DryRun {
				return
			}
			restartContainers(client, nameToID, topo, stopped)
		}()
	}

	// 9. 组装 manifest。
	man := buildManifest(client, sel, doc, entries, opts)
	man.Warnings = warns

	if opts.DryRun {
		// dry-run:不落盘,返回一个空 tar 路径。
		return &Result{Manifest: man, Compose: doc, Warnings: warns, Stopped: stopped}, nil
	}

	// 10. 打 tar(写 .tmp 再改名)。
	ts := opts.Now.Format("20060102-150405")
	rootDir := opts.Name + "-" + ts
	tn := opts.Name + "-" + ts + "-" + hostname() + ".tar"
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return nil, err
	}
	tarPath := filepath.Join(opts.OutDir, tn)
	tmpPath := tarPath + ".tmp"
	if err := writeArchive(tmpPath, rootDir, man, doc, opts, entries); err != nil {
		os.Remove(tmpPath)
		return nil, err
	}
	if err := os.Rename(tmpPath, tarPath); err != nil {
		os.Remove(tmpPath)
		return nil, err
	}

	// 11. keep 清理。
	if opts.Keep > 0 {
		pruneOld(opts.OutDir, opts.Name, opts.Now, opts.Keep)
	}

	return &Result{
		TarPath:  tarPath,
		Manifest: man,
		Compose:  doc,
		Warnings: warns,
		Stopped:  stopped,
	}, nil
}

// writeArchive 把整个备份写入 tar 文件(流式,单次遍历数据)。
func writeArchive(tarPath, rootDir string, man *manifest.Manifest, doc *compose.Doc, opts Options, entries []manifest.DataEntry) error {
	f, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	tw := tar.NewWriter(f)

	writeMeta := func(name string, data []byte, mode int64) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	// 顶层目录。
	if err := tw.WriteHeader(&tar.Header{Name: rootDir + "/", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		return err
	}
	// docker-compose.yml
	composeYAML, err := compose.Marshal(doc)
	if err != nil {
		return err
	}
	if err := writeMeta(rootDir+"/docker-compose.yml", composeYAML, 0o644); err != nil {
		return err
	}
	// manifest.json
	manJSON, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	if err := writeMeta(rootDir+"/manifest.json", manJSON, 0o644); err != nil {
		return err
	}
	// README.md
	readme := renderREADME(man, rootDir, entries)
	if err := writeMeta(rootDir+"/README.md", []byte(readme), 0o644); err != nil {
		return err
	}
	// config.yaml(备份时使用的配置)
	cfgYAML := jsonToYAML(opts.Config)
	if err := writeMeta(rootDir+"/config.yaml", cfgYAML, 0o644); err != nil {
		return err
	}
	// data/ 目录
	if err := tw.WriteHeader(&tar.Header{Name: rootDir + "/data/", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		return err
	}

	// 数据条目(单次遍历,统计后写 index)。
	var idx manifest.DataIndex
	for _, e := range entries {
		src := entrySourcePath(e)
		rel := "data/" + e.ID
		st, werr := tarx.WriteTree(tw, src, rootDir+"/"+rel)
		if werr != nil {
			// 源缺失:记录空条目并告警,不中断整体。
			idx.Entries = append(idx.Entries, withStats(e, 0, 0))
			man.Warnings = append(man.Warnings, fmt.Sprintf("数据条目 %s(%s)拷贝失败: %v", e.ID, src, werr))
			continue
		}
		idx.Entries = append(idx.Entries, withStats(e, st.Size, st.FileCount))
	}
	// data/index.json(最后写,统计已就绪)
	idxJSON, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := writeMeta(rootDir+"/data/index.json", idxJSON, 0o644); err != nil {
		return err
	}

	return tw.Close()
}

func withStats(e manifest.DataEntry, size, files int64) manifest.DataEntry {
	e.Size = size
	e.FileCount = files
	return e
}

// entrySourcePath 返回数据条目在磁盘上的源路径。
func entrySourcePath(e manifest.DataEntry) string {
	if e.Kind == "volume" {
		return e.Mountpoint
	}
	return e.Source
}

// ---- 停/起容器 ----

func stopContainers(client *dc.Client, nameToID map[string]string, order []string, preState map[string]bool, timeout int) []string {
	var stopped []string
	for _, n := range order {
		if !preState[n] {
			continue
		}
		if err := client.Stop(nameToID[n], timeout); err != nil {
			// 停失败不致命(可能已停);记录。
			continue
		}
		stopped = append(stopped, n)
	}
	return stopped
}

func restartContainers(client *dc.Client, nameToID map[string]string, topo, stopped []string) {
	// 正拓扑拉起,只拉被我们停的。
	stoppedSet := map[string]bool{}
	for _, s := range stopped {
		stoppedSet[s] = true
	}
	for _, n := range topo {
		if stoppedSet[n] {
			client.Start(nameToID[n])
		}
	}
}

// ---- manifest ----

func buildManifest(client *dc.Client, sel *Selection, doc *compose.Doc, entries []manifest.DataEntry, opts Options) *manifest.Manifest {
	info, _ := client.Info()
	ver, _ := client.Version()
	man := &manifest.Manifest{
		ToolVersion: ToolVersion,
		ToolName:    "dbackup",
		CreatedAt:   opts.Now,
		Mode:        opts.Mode,
		Project:     sel.Project,
		Host: manifest.HostInfo{
			Hostname:        hostname(),
			DockerVersion:   info.ServerVersion,
			ApiVersion:      ver.ApiVersion,
			Architecture:    info.Architecture,
			Os:              ver.Os,
			Kernel:          ver.KernelVersion,
			OperatingSystem: info.OperatingSystem,
		},
	}
	// 数据条目 id → 条目,便于服务挂载映射。
	byID := map[string]manifest.DataEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	// 按 service key 稳定顺序输出。
	keys := []string{}
	for k := range doc.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		svc := doc.Services[k]
		rec := manifest.ServiceRec{
			ServiceKey: k,
			Name:       svc.ContainerName,
			Image:      svc.Image,
		}
		if insp, ok := sel.Containers[svc.ContainerName]; ok {
			rec.Inspect, _ = json.Marshal(insp)
			if insp.State != nil {
				rec.State = insp.State.Status
				rec.Running = insp.State.Running
			}
			// 挂载 → data 条目映射。
			for _, m := range insp.Mounts {
				mr := manifest.MountRec{Type: m.Type, Source: m.Source, Destination: m.Destination, Mode: m.Mode}
				for _, e := range entries {
					if (e.Kind == "bind" && e.Source == m.Source) || (e.Kind == "volume" && e.VolumeName == m.Name) {
						mr.DataEntry = e.ID
						break
					}
				}
				rec.Mounts = append(rec.Mounts, mr)
			}
		}
		man.Services = append(man.Services, rec)
	}
	return man
}

// ---- helpers ----

func nameSets(include, exclude []string) (map[string]bool, map[string]bool) {
	return toSet(include), toSet(exclude)
}

func toSet(list []string) map[string]bool {
	s := map[string]bool{}
	for _, x := range list {
		s[x] = true
	}
	return s
}

func inferProject(names []string, containers map[string]dc.Inspect) string {
	for _, n := range names {
		c := containers[n]
		if c.Config == nil {
			continue
		}
		if p, ok := c.Config.Labels["com.docker.compose.project"]; ok && p != "" {
			return p
		}
	}
	return ""
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "localhost"
	}
	return h
}
