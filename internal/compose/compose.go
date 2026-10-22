// Package compose 把 docker inspect 数据翻译成可直接 `docker compose up -d`
// 的 compose 文档。目标是「与现状一致、语义等价」：字段取自 inspect 的真实值，
// 恢复后逐服务的关键字段与原容器一致（验收用 docker compose config 归一化后 diff）。
package compose

import "gopkg.in/yaml.v3"

// Marshal 把 Doc 序列化成 YAML 字节(用于写 docker-compose.yml)。
func Marshal(doc *Doc) ([]byte, error) {
	if doc == nil {
		return []byte("# 无服务\n"), nil
	}
	return yaml.Marshal(doc)
}

// Doc 是一份 docker-compose 文档（v2，无 version 键——现代 compose 已弃用）。
type Doc struct {
	Services map[string]Service `yaml:"services"`
	Volumes  map[string]any     `yaml:"volumes,omitempty"`
	Networks map[string]any     `yaml:"networks,omitempty"`
}

// Service 是 compose 里的一个 service。字段顺序按 YAML 结构体定义；
// 全部 omitempty，空值不出现，保证输出干净。
type Service struct {
	Image           string            `yaml:"image,omitempty"`
	ContainerName   string            `yaml:"container_name,omitempty"`
	Restart         string            `yaml:"restart,omitempty"`
	Ports           []string          `yaml:"ports,omitempty"`
	Expose          []string          `yaml:"expose,omitempty"`
	Environment     []string          `yaml:"environment,omitempty"` // 列表形式 KEY=VALUE,原样保留特殊字符
	Entrypoint      []string          `yaml:"entrypoint,omitempty"`
	Command         []string          `yaml:"command,omitempty"`
	Volumes         []string          `yaml:"volumes,omitempty"`
	WorkingDir      string            `yaml:"working_dir,omitempty"`
	User            string            `yaml:"user,omitempty"`
	Init            *bool             `yaml:"init,omitempty"`
	StopSignal      string            `yaml:"stop_signal,omitempty"`
	Networks        []string          `yaml:"networks,omitempty"`
	NetworkMode     string            `yaml:"network_mode,omitempty"`
	DNS             []string          `yaml:"dns,omitempty"`
	ExtraHosts      []string          `yaml:"extra_hosts,omitempty"`
	CapAdd          []string          `yaml:"cap_add,omitempty"`
	CapDrop         []string          `yaml:"cap_drop,omitempty"`
	Privileged      bool              `yaml:"privileged,omitempty"`
	SecurityOpt     []string          `yaml:"security_opt,omitempty"`
	Devices         []string          `yaml:"devices,omitempty"`
	Tmpfs           map[string]string `yaml:"tmpfs,omitempty"`
	Ulimits         map[string]Ulimit `yaml:"ulimits,omitempty"`
	Sysctls         map[string]string `yaml:"sysctls,omitempty"`
	ShmSize         string            `yaml:"shm_size,omitempty"`
	IPCMode         string            `yaml:"ipc_mode,omitempty"`
	PidMode         string            `yaml:"pid_mode,omitempty"`
	UTSMode         string            `yaml:"uts_mode,omitempty"`
	ReadOnly        bool              `yaml:"read_only,omitempty"`
	GroupAdd        []string          `yaml:"group_add,omitempty"`
	Logging         map[string]any    `yaml:"logging,omitempty"`
	Healthcheck     *Healthcheck      `yaml:"healthcheck,omitempty"`
	MemLimit        string            `yaml:"mem_limit,omitempty"`
	MemswapLimit    string            `yaml:"memswap_limit,omitempty"`
	CPUShares       int64             `yaml:"cpu_shares,omitempty"`
	Cpus            float64           `yaml:"cpus,omitempty"`
	Labels          map[string]string `yaml:"labels,omitempty"`
	Hostname        string            `yaml:"hostname,omitempty"`
	Domainname      string            `yaml:"domainname,omitempty"`
	StopGracePeriod string            `yaml:"stop_grace_period,omitempty"`
	DependsOn       any               `yaml:"depends_on,omitempty"`
}

type Ulimit struct {
	Soft int64 `yaml:"soft"`
	Hard int64 `yaml:"hard"`
}

type Healthcheck struct {
	Test        []string `yaml:"test"`
	Interval    string   `yaml:"interval,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
	StartPeriod string   `yaml:"start_period,omitempty"`
	Retries     int      `yaml:"retries,omitempty"`
	Disable     bool     `yaml:"disable,omitempty"`
}
