package compose

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"dbackup/internal/dc"
)

// ContainerInput 是生成 compose 需要的每个容器的输入。
type ContainerInput struct {
	Name    string // 显示名(不含前导 /)
	Insp    dc.Inspect
}

// Generate 把一组容器 inspect 翻译成 compose 文档 + 警告列表。
// 同时返回显示名 → service key 的映射(manifest 与 backup 流程需要)。
func Generate(inputs []ContainerInput) (*Doc, map[string]string, []string) {
	doc := &Doc{Services: map[string]Service{}, Volumes: map[string]any{}, Networks: map[string]any{}}
	var warnings []string
	keys := map[string]string{} // 显示名 → service key

	// service key 分配：优先 compose 的 com.docker.compose.service 标签，否则容器名。
	used := map[string]bool{}
	for _, in := range inputs {
		key := in.Name
		if in.Insp.Config != nil {
			if sv, ok := in.Insp.Config.Labels["com.docker.compose.service"]; ok && sv != "" {
				key = sv
			}
		}
		// 去重：两个容器映射到同一 service key 时，追加 -N。
		base := key
		for i := 2; used[key]; i++ {
			key = fmt.Sprintf("%s-%d", base, i)
		}
		used[key] = true
		keys[in.Name] = key

		svc := buildService(in)
		doc.Services[key] = svc
		for _, w := range svcWarnings(in, key) {
			warnings = append(warnings, w)
		}

		// 顶层 volumes / networks 收集。
		for name := range svcVolumeNames(in.Insp) {
			if _, ok := doc.Volumes[name]; !ok {
				doc.Volumes[name] = nil
			}
		}
		for _, net := range svcNetworks(in.Insp) {
			if _, ok := doc.Networks[net]; !ok {
				doc.Networks[net] = nil
			}
		}
	}
	// 空的顶层段不输出。
	if len(doc.Volumes) == 0 {
		doc.Volumes = nil
	}
	if len(doc.Networks) == 0 {
		doc.Networks = nil
	}
	if len(warnings) == 0 {
		warnings = []string{}
	}
	return doc, keys, warnings
}

func buildService(in ContainerInput) Service {
	s := Service{}
	cfg, hc, ns := in.Insp.Config, in.Insp.HostConfig, in.Insp.NetworkSettings

	s.ContainerName = in.Name
	if cfg != nil {
		s.Image = cfg.Image
		s.Environment = envList(cfg.Env)
		if len(cfg.Cmd) > 0 {
			s.Command = cfg.Cmd
		}
		if len(cfg.Entrypoint) > 0 {
			s.Entrypoint = cfg.Entrypoint
		}
		s.WorkingDir = cfg.WorkingDir
		s.User = cfg.User
		s.StopSignal = cfg.StopSignal
		s.Init = cfg.Init
		s.Hostname = cfg.Hostname
		s.Domainname = cfg.Domainname
		s.Labels = copyLabels(cfg.Labels)
		if cfg.StopTimeout != 0 {
			s.StopGracePeriod = fmt.Sprintf("%ds", cfg.StopTimeout)
		}
		if cfg.Healthcheck != nil {
			s.Healthcheck = mapHealthcheck(cfg.Healthcheck)
		}
		if len(cfg.ExposedPorts) > 0 {
			s.Expose = expose(cfg.ExposedPorts, portBindingSet(hc))
		}
	}
	if hc != nil {
		s.Restart = restartPolicy(hc.RestartPolicy.Name, hc.RestartPolicy.MaximumRetryCount)
		s.Ports = portsFromBindings(hc.PortBindings)
		s.NetworkMode = networkMode(hc.NetworkMode)
		if len(hc.DNS) > 0 {
			s.DNS = hc.DNS
		}
		if len(hc.ExtraHosts) > 0 {
			s.ExtraHosts = hc.ExtraHosts
		}
		if len(hc.CapAdd) > 0 {
			s.CapAdd = hc.CapAdd
		}
		if len(hc.CapDrop) > 0 {
			s.CapDrop = hc.CapDrop
		}
		s.Privileged = hc.Privileged
		if len(hc.SecurityOpt) > 0 {
			s.SecurityOpt = hc.SecurityOpt
		}
		if len(hc.Devices) > 0 {
			s.Devices = deviceList(hc.Devices)
		}
		if len(hc.Tmpfs) > 0 {
			s.Tmpfs = hc.Tmpfs
		}
		if len(hc.Ulimits) > 0 {
			s.Ulimits = ulimitsMap(hc.Ulimits)
		}
		if len(hc.Sysctls) > 0 {
			s.Sysctls = hc.Sysctls
		}
		if hc.ShmSize != 0 && hc.ShmSize != 67108864 { // 64m 是 docker 默认
			s.ShmSize = humanBytes(hc.ShmSize)
		}
		if hc.IPCMode != "" && hc.IPCMode != "private" {
			s.IPCMode = hc.IPCMode
		}
		if hc.PidMode != "" {
			s.PidMode = hc.PidMode
		}
		if hc.UTSMode != "" {
			s.UTSMode = hc.UTSMode
		}
		s.ReadOnly = hc.ReadOnly
		if len(hc.GroupAdd) > 0 {
			s.GroupAdd = hc.GroupAdd
		}
		s.MemLimit = humanBytes(hc.Memory)
		s.MemswapLimit = humanBytes(hc.MemorySwap)
		if hc.CPUShares != 0 {
			s.CPUShares = hc.CPUShares
		}
		if hc.NanoCPUs != 0 {
			s.Cpus = float64(hc.NanoCPUs) / 1e9
		} else if hc.CPUQuota != 0 && hc.CPUPeriod != 0 {
			s.Cpus = float64(hc.CPUQuota) / float64(hc.CPUPeriod)
		}
		if lc := hc.LogConfig; lc.Type != "json-file" || len(lc.Config) > 0 {
			s.Logging = map[string]any{"driver": lc.Type}
			if len(lc.Config) > 0 {
				opts := map[string]any{}
				for k, v := range lc.Config {
					opts[k] = v
				}
				s.Logging["options"] = opts
			}
		}
	}
	if ns != nil {
		s.Networks = svcNetworks(in.Insp)
		// 只挂在默认 docker bridge 的容器(无自定义网络,典型即 CasaOS 应用):
		// 放进 compose project 后默认行为会把它接到 <project>_default 网络,恢复后
		// 网络形态就变了。显式声明 network_mode: bridge,让恢复的容器仍接默认 docker
		// 桥,与源机一致。(与 networks 列表互斥,故仅在没有用户网络时声明;
		// 既有用户网络又挂 bridge 的容器,以用户网络为准。)
		if len(s.Networks) == 0 && s.NetworkMode == "" {
			s.NetworkMode = "bridge"
		}
	}
	// 挂载 → volumes
	if mnts := volumeMounts(in.Insp); len(mnts) > 0 {
		s.Volumes = mnts
	}
	// depends_on：来自 compose 标签（inspect 里保留的原始声明）。
	// 标签值是逗号分隔的 "service[:condition[:required]]"。compose 建容器时把短形式
	// "- db" 规范化成 "db:service_started:false",所以这里要能把它还原回短形式,否则
	// 反复 restore→backup 会把 "db:service_started:false" 当服务名,导致 compose 报错。
	if cfg != nil {
		if dep, ok := cfg.Labels["com.docker.compose.depends_on"]; ok && dep != "" {
			if v, ok := parseDependsOn(dep); ok {
				s.DependsOn = v
			}
		}
	}
	return s
}

// svcWarnings 返回单容器值得写进 README 的警告。
func svcWarnings(in ContainerInput, key string) []string {
	var w []string
	cfg, hc := in.Insp.Config, in.Insp.HostConfig
	if cfg != nil && cfg.Image != "" && strings.Contains(cfg.Image, "sha256:") {
		w = append(w, fmt.Sprintf("service %q 用 digest 形式镜像 %s——digest 通常锁定架构，跨架构恢复可能拉不到", key, cfg.Image))
	}
	if hc != nil {
		if hc.NetworkMode == "host" {
			w = append(w, fmt.Sprintf("service %q 用 network_mode: host——恢复机端口需与容器端口一致", key))
		}
		if len(hc.Devices) > 0 {
			var dev []string
			for _, d := range hc.Devices {
				dev = append(dev, d.PathOnHost)
			}
			w = append(w, fmt.Sprintf("service %q 依赖宿主设备 %s——恢复机需存在这些设备", key, strings.Join(dev, ", ")))
		}
	}
	return w
}

// ---- 字段辅助 ----

func svcVolumeNames(insp dc.Inspect) map[string]bool {
	names := map[string]bool{}
	for _, m := range insp.Mounts {
		if m.Type == "volume" && m.Name != "" {
			names[m.Name] = true
		}
	}
	return names
}

func svcNetworks(insp dc.Inspect) []string {
	if insp.NetworkSettings == nil {
		return nil
	}
	out := []string{}
	for n := range insp.NetworkSettings.Networks {
		if isUserNet(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func isUserNet(n string) bool {
	if n == "bridge" || n == "host" || n == "none" {
		return false
	}
	return !strings.HasPrefix(n, "container:")
}

func volumeMounts(insp dc.Inspect) []string {
	out := []string{}
	for _, m := range insp.Mounts {
		switch m.Type {
		case "bind":
			out = append(out, m.Source+":"+m.Destination+mountSuffix(m))
		case "volume":
			out = append(out, m.Name+":"+m.Destination+mountSuffix(m))
		}
	}
	return out
}

func mountSuffix(m dc.Mount) string {
	// docker inspect 的 Mount.Mode 对 compose 创建的容器形如 "rw,z":rw 是读写标志,
	// z 等才是后缀。rw/ro 由 m.RW 单独决定,这里只收集其余标志,避免拼出非法的
	// 四段式卷名(如 vol:/dst:rw:z)。
	parts := []string{}
	for _, f := range strings.Split(m.Mode, ",") {
		if f = strings.TrimSpace(f); f == "" || f == "rw" || f == "ro" {
			continue
		}
		parts = append(parts, f)
	}
	if !m.RW {
		parts = append(parts, "ro")
	}
	if len(parts) == 0 {
		return ""
	}
	return ":" + strings.Join(parts, ":")
}

func envList(env []string) []string {
	order := []string{}
	idx := map[string]int{}
	for _, e := range env {
		i := strings.IndexByte(e, '=')
		if i < 0 {
			continue
		}
		k := e[:i]
		if k == "PATH" {
			continue
		}
		if j, ok := idx[k]; ok {
			order[j] = e // 键重复取最后值，保留首次出现位置
		} else {
			idx[k] = len(order)
			order = append(order, e)
		}
	}
	return order
}

func restartPolicy(name string, maxRetry int) string {
	if name == "" || name == "no" {
		return ""
	}
	if name == "on-failure" && maxRetry > 0 {
		return fmt.Sprintf("on-failure:%d", maxRetry)
	}
	return name
}

func portBindingSet(hc *dc.HostConfig) map[string]bool {
	set := map[string]bool{}
	if hc != nil {
		for k := range hc.PortBindings {
			set[k] = true
		}
	}
	return set
}

func portsFromBindings(pb map[string][]dc.PortBinding) []string {
	if len(pb) == 0 {
		return nil
	}
	out := []string{}
	for _, cport := range sortedKeys(pb) {
		for _, b := range pb[cport] {
			s := ""
			if b.HostIP != "" {
				s += b.HostIP + ":"
			}
			out = append(out, s+b.HostPort+":"+cport)
		}
	}
	return out
}

func expose(exposed map[string]struct{}, published map[string]bool) []string {
	out := []string{}
	for p := range exposed {
		if !published[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func networkMode(mode string) string {
	if mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") {
		return mode
	}
	return "" // bridge 与用户网络都走 networks 列表
}

// parseDependsOn 解析 com.docker.compose.depends_on 标签值(逗号分隔的
// "service[:condition[:required]]"),还原成 compose depends_on 字段。
// 若所有依赖都是默认的 service_started 条件(即短形式),返回字符串切片;
// 否则返回带条件/required 的长形式 map。
func parseDependsOn(label string) (any, bool) {
	var short []string
	long := map[string]any{}
	anyLong := false
	for _, item := range strings.Split(label, ",") {
		parts := strings.Split(item, ":")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		svc := parts[0]
		cond := ""
		required := false
		if len(parts) > 1 {
			cond = parts[1]
		}
		if len(parts) > 2 {
			required = parts[2] == "true"
		}
		// 短形式 = 无显式条件,或条件就是默认的 service_started,且非 required。
		if (cond == "" || cond == "service_started") && !required {
			short = append(short, svc)
			continue
		}
		anyLong = true
		m := map[string]any{}
		if cond != "" {
			m[cond] = true
		}
		if required {
			m["required"] = true
		}
		long[svc] = m
	}
	if len(short) == 0 && len(long) == 0 {
		return nil, false
	}
	if !anyLong {
		return short, true
	}
	return long, true
}

func deviceList(devs []dc.DeviceMapping) []string {
	out := []string{}
	for _, d := range devs {
		s := d.PathOnHost + ":" + d.PathInContainer
		if d.CgroupPermissions != "" {
			s += ":" + d.CgroupPermissions
		}
		out = append(out, s)
	}
	return out
}

func ulimitsMap(ul []dc.Ulimit) map[string]Ulimit {
	m := map[string]Ulimit{}
	for _, u := range ul {
		m[u.Name] = Ulimit{Soft: u.Soft, Hard: u.Hard}
	}
	return m
}

func mapHealthcheck(h *dc.Healthcheck) *Healthcheck {
	out := &Healthcheck{Test: h.Test, Retries: h.Retries, Disable: h.Disable}
	if h.Interval != 0 {
		out.Interval = time.Duration(h.Interval).String()
	}
	if h.Timeout != 0 {
		out.Timeout = time.Duration(h.Timeout).String()
	}
	if h.StartPeriod != 0 {
		out.StartPeriod = time.Duration(h.StartPeriod).String()
	}
	return out
}

func copyLabels(l map[string]string) map[string]string {
	if len(l) == 0 {
		return nil
	}
	out := make(map[string]string, len(l))
	for k, v := range l {
		out[k] = v
	}
	return out
}

// ---- 小工具 ----

func humanBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%db", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	suffix := "kmgtpe"[exp]
	v := float64(n) / float64(div)
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d%c", int64(v), suffix)
	}
	return fmt.Sprintf("%.2f%c", v, suffix)
}

func sortedKeys(m map[string][]dc.PortBinding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
