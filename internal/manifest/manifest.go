// Package manifest 定义备份包内的 manifest.json / data/index.json 结构。
package manifest

import (
	"encoding/json"
	"time"
)

// Manifest 是备份包的元数据(ARCHITECTURE.md §4)。
type Manifest struct {
	ToolVersion string       `json:"tool_version"`
	ToolName    string       `json:"tool_name"`
	CreatedAt   time.Time    `json:"created_at"`
	Mode        string       `json:"mode"` // cold | live
	Host        HostInfo     `json:"host"`
	Services    []ServiceRec `json:"services"`
	Warnings    []string     `json:"warnings"`
	Project     string       `json:"project"` // 记录的 project 名(恢复缺省用)
}

type HostInfo struct {
	Hostname     string `json:"hostname"`
	DockerVersion string `json:"docker_version"`
	ApiVersion   string `json:"api_version"`
	Architecture string `json:"architecture"`
	Os           string `json:"os"`
	Kernel       string `json:"kernel"`
	OperatingSystem string `json:"operating_system"`
}

// ServiceRec 是单个服务在备份里的完整记录。
type ServiceRec struct {
	Name      string          `json:"name"`       // 容器名
	ServiceKey string         `json:"service_key"` // compose 里的 key
	Image     string          `json:"image"`
	ImageID   string          `json:"image_id"`
	State     string          `json:"state"`
	Running   bool            `json:"running"`
	Inspect   json.RawMessage `json:"inspect"`  // 完整原始 inspect JSON(溯源)
	Compose   map[string]any  `json:"compose"`  // 生成的 compose 片段
	Mounts    []MountRec      `json:"mounts"`   // 该服务的挂载 → data 条目映射
}

type MountRec struct {
	Type        string `json:"type"` // bind | volume
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	DataEntry   string `json:"data_entry"` // 对应的 data/index.json 条目 id(空=跳过)
}

// DataIndex 是 data/index.json。
type DataIndex struct {
	Entries []DataEntry `json:"entries"`
}

// DataEntry 是一个唯一数据源(宿主目录或卷)的记录。
type DataEntry struct {
	ID          string   `json:"id"`            // NN-<sanitized-last-segment>
	Kind        string   `json:"kind"`          // bind | volume
	Source      string   `json:"source"`        // bind: 宿主绝对路径; volume: 卷名
	VolumeName  string   `json:"volume_name,omitempty"`
	Mountpoint  string   `json:"mountpoint,omitempty"` // volume 实际 _data 目录
	Anonymous   bool     `json:"anonymous,omitempty"`
	Owners      []string `json:"owners"`        // 归属服务 key 列表
	ContainerPaths []string `json:"container_paths"` // 容器内挂载目标(多个服务/路径)
	Size        int64    `json:"size"`          // 字节
	FileCount   int64    `json:"file_count"`
	Top         []TopStat `json:"top,omitempty"` // 前若干大目录(可读性)
}

type TopStat struct {
	Path string  `json:"path"`
	Size int64   `json:"size"`
	Frac float64 `json:"frac"`
}
