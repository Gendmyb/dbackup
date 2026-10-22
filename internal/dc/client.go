// Package dc 通过 unix socket 上的 HTTP API 与本地 docker daemon 通信。
// 只用标准库,避免 docker client 库的版本耦合;只用到的端点:
// /_ping, /info, /version, /containers/json, /containers/{id}/inspect,
// /containers/{id}/stop, /containers/{id}/start, /volumes。
package dc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	hc       *http.Client
	endpoint string
}

// New 连接到 docker socket。endpoint 形如 unix:///var/run/docker.sock。
func New(endpoint string) (*Client, error) {
	if endpoint == "" {
		endpoint = "unix:///var/run/docker.sock"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("解析 endpoint 失败: %w", err)
	}
	if u.Scheme != "unix" {
		return nil, fmt.Errorf("当前只支持 unix socket endpoint,得到: %s", endpoint)
	}
	c := &Client{endpoint: endpoint, hc: &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "unix", u.Path)
			},
		},
	}}
	if err := c.Ping(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Ping() error {
	req, _ := http.NewRequest("GET", "http://docker/_ping", nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("连接 docker daemon 失败(%s): %w — 检查 socket 权限或 DOCKER_HOST", c.endpoint, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker _ping 返回 %s", resp.Status)
	}
	return nil
}

func (c *Client) get(path string, out any) error {
	req, _ := http.NewRequest("GET", "http://docker"+path, nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s 返回 %s: %s", path, resp.Status, truncate(string(body), 200))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("解析 GET %s 的响应: %w", path, err)
		}
	}
	return nil
}

func (c *Client) getRaw(path string) (json.RawMessage, error) {
	req, _ := http.NewRequest("GET", "http://docker"+path, nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s 返回 %s: %s", path, resp.Status, truncate(string(body), 200))
	}
	return json.RawMessage(body), nil
}

func (c *Client) post(path string, out any) error {
	req, _ := http.NewRequest("POST", "http://docker"+path, nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s 返回 %s: %s", path, resp.Status, truncate(string(body), 200))
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("解析 POST %s 的响应: %w", path, err)
		}
	}
	return nil
}

// postJSON 发送 JSON 请求体(用于 /volumes/create 等)。
func (c *Client) postJSON(path string, payload any, out any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", "http://docker"+path, strings.NewReader(string(buf)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST %s 返回 %s: %s", path, resp.Status, truncate(string(body), 200))
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("解析 POST %s 的响应: %w", path, err)
		}
	}
	return nil
}

// Endpoint 返回连接用的 endpoint 串(供恢复侧设置 DOCKER_HOST)。
func (c *Client) Endpoint() string { return c.endpoint }

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- 类型(只覆盖用到的字段;原始 JSON 另行整体保留) ----

type Info struct {
	Containers        int
	ContainersRunning int
	Name              string
	ServerVersion     string
	OperatingSystem   string
	OSType            string
	Architecture      string
	NCPU              int
	MemTotal          uint64
	Driver            string
}

type VersionInfo struct {
	Platform struct {
		Name string
	}
	Version          string
	ApiVersion       string
	Arch             string
	Os               string
	GitCommit        string
	BuildTime        string
	KernelVersion    string
	OperatingSystem  string
	OSType           string
	Architecture     string
	IndexServerAddress string
}

type ContainerSummary struct {
	ID         string
	Names      []string
	Image      string
	ImageID    string
	Command    string
	Created    int64
	State      string
	Status     string
	Ports      []PortSummary
	Labels     map[string]string
	HostConfig struct {
		NetworkMode string
	}
	NetworkSettings struct {
		Networks map[string]any
	}
	Mounts []Mount
}

type PortSummary struct {
	IP          string
	PrivatePort int
	PublicPort  int
	Type        string
}

type Mount struct {
	Type        string
	Name        string
	Source      string
	Destination string
	Mode        string
	RW          bool
	Propagation string
	Driver      string
}

// Inspect 容器 /inspect 的关键字段。
type Inspect struct {
	ID              string
	Name            string
	Created         string
	Path            string
	Args            []string
	Config          *Config
	State           *State
	Image           string
	HostConfig      *HostConfig
	Mounts          []Mount
	NetworkSettings *NetworkSettings
	Driver          string
}

type Config struct {
	Hostname       string
	Domainname     string
	ExposedPorts   map[string]struct{}
	Env            []string
	Cmd            []string
	Healthcheck    *Healthcheck
	Image          string
	Volumes        map[string]struct{}
	WorkingDir     string
	Entrypoint     []string
	Labels         map[string]string
	StopSignal     string
	User           string
	MacAddress     string
	OnBuild        []string
	AttachStdin    bool
	AttachStdout   bool
	AttachStderr   bool
	ExposedPortMap map[string]struct{}
	Tty            bool
	OpenStdin      bool
	StdinOnce      bool
	Init           *bool
	NetworkDisabled bool
	StopTimeout    int
	Shell          []string
}

type Healthcheck struct {
	Test        []string
	Interval    int64 // nanoseconds
	Timeout     int64
	StartPeriod int64
	StartInterval int64
	Retries     int
	Disable     bool
}

type State struct {
	Status     string
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	Dead       bool
	Pid        int
	ExitCode   int
	StartedAt  string
	FinishedAt string
	Health     *Health
}

type Health struct {
	Status        string
	FailingStreak int
}

type HostConfig struct {
	Privileged    bool
	NoNewPrivileges bool
	RestartPolicy struct {
		Name              string
		MaximumRetryCount int
	}
	AutoRemove  bool
	Binds       []string
	ContainerIDFile string
	LogConfig   struct {
		Type   string
		Config map[string]string
	}
	NetworkMode string
	PortBindings map[string][]PortBinding
	Hostname      string
	Domains       []string
	DNS           []string `json:"Dns"`
	DNSOptions    []string
	DNSSearch     []string
	VolumesFrom   []string
	ShmSize       int64
	SwarmResource struct{}
	Links         []string
	ExtraHosts    []string
	PublishAllPorts bool
	Privileges    []struct{}
	CapAdd        []string
	CapDrop       []string
	CgroupParent  string
	Cgroupns      string
	DeviceCgroupRules []string
	Devices       []DeviceMapping
	DeviceReadBps    []struct{}
	DeviceReadIOps   []struct{}
	DeviceWriteBps   []struct{}
	DeviceWriteIOps  []struct{}
	GroupAdd       []string
	IOMaximum      int64
	IOMaximumWeight int64
	IOMinimum      int64
	IOMinimumWeight int64
	IOMerge        int64
	IOWeight       int64
	CPUShares      int64 `json:"CpuShares"`
	CPUQuota       int64 `json:"CpuQuota"`
	CPUPeriod      int64 `json:"CpuPeriod"`
	CPURealtimePeriod int64
	CPURealtimeRuntime  int64
	CPUBurst     int64
	CpusetCPUs   string `json:"CpusetCpus"`
	CpusetMems   string
	Isolation    string
	KernelMemory int64
	KernelMemoryTCP int64
	Memory       int64
	MemorySwap   int64
	MemorySwappiness *int64
	NanoCPUs     int64 `json:"NanoCpus"`
	OOMKillDisable *bool
	OomScoreAdj  int
	PidsLimit    int64
	Ulimits      []Ulimit
	IPCMode      string
	PidMode      string
	UTSMode      string
	UsernsMode   string
	ShmSizeLegacy  int64
	ReadOnly         bool
	ReadOnlyTmpfs    bool
	SecurityOpt      []string
	StorageOpt       map[string]string
	Tmpfs            map[string]string
	MaskedPaths      []string
	ReadonlyPaths    []string
	Sysctls          map[string]string
	Runtime          string
	IsolationLegacy  string
	Resources        Resources
	BlkioDeviceReadBps  []struct{}
	BlkioDeviceReadIOps []struct{}
	BlkioDeviceWriteBps []struct{}
	BlkioDeviceWriteIOps []struct{}
	BlkioWeight         int64
	BlkioWeightDevice   []struct{}
	CgroupPath        string
	CPUCount          int64
	CPUPercent        int64
	IOMaximumIOps     int64
	IOMinimumIOps     int64
	MemoryReservation int64
	MemorySwapLimit   int64
}

type Resources struct {
	CPU    *struct{}
	Memory *struct{}
}

type PortBinding struct {
	HostIP   string
	HostPort string
}

type DeviceMapping struct {
	PathOnHost        string
	PathInContainer   string
	CgroupPermissions string
}

type Ulimit struct {
	Name  string
	Soft  int64
	Hard  int64
}

type NetworkSettings struct {
	Bridge        string
	SandboxKey    string
	HairpinMode   bool
	LinkLocalIPv6Address string
	LinkLocalIPv6PrefixLen int
	Ports         map[string][]PortMapping
	Gateway       string
	GlobalIPv6Address string
	GlobalIPv6PrefixLen int
	IPAddress     string
	IPPrefixLen   int
	IPv6Gateway   string
	MacAddress    string
	Networks      map[string]NetworkDetail
	EndpointID    string
	NetworksKey   string
}

type NetworkDetail struct {
	IPAMConfig      *struct{}
	Links           map[string]string
	NetworkID       string
	Gateway         string
	IPAddress       string
	IPPrefixLen     int
	IPv6Gateway     string
	GlobalIPv6Address string
	MacAddress      string
	DriverOpts      map[string]string
	Aliases         []string
}

type PortMapping struct {
	HostIP   string
	HostPort string
}

type VolumeInfo struct {
	Driver       string
	Mountpoint   string
	Name         string
	Scope        string
	Options      map[string]string
	MountpointReal string
	Labels       map[string]string
	CreatedAt    string
}

// IsAnonymous 报告该卷是否为匿名卷(由 com.docker.volume.anonymous 标签或
// 哈希名判定)。CasaOS 应用的数据主要落在匿名卷,备份必须覆盖。
func (v VolumeInfo) IsAnonymous() bool {
	if _, ok := v.Labels["com.docker.volume.anonymous"]; ok {
		return true
	}
	// 无标签时:64 位十六进制哈希名即匿名卷。
	if len(v.Name) == 64 {
		for _, r := range v.Name {
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				return false
			}
		}
		return true
	}
	return false
}

// ---- 方法 ----

func (c *Client) Info() (Info, error) {
	var v Info
	err := c.get("/info", &v)
	return v, err
}

func (c *Client) Version() (VersionInfo, error) {
	var v VersionInfo
	err := c.get("/version", &v)
	return v, err
}

func (c *Client) Containers(all bool) ([]ContainerSummary, error) {
	q := "0"
	if all {
		q = "1"
	}
	var out []ContainerSummary
	err := c.get("/containers/json?all="+q, &out)
	return out, err
}

// Inspect 容器。docker>=29 的 inspect 端点是 /containers/{id}/json(旧版是 /inspect)。
func (c *Client) Inspect(id string) (Inspect, error) {
	var v Inspect
	err := c.get("/containers/"+id+"/json", &v)
	return v, err
}

// InspectRaw 返回容器 inspect 的原始 JSON(用于 manifest 完整存档)。
func (c *Client) InspectRaw(id string) (json.RawMessage, error) {
	return c.getRaw("/containers/"+id+"/json")
}

// InspectContainer 是 Inspect 的别名(与官方 client 命名一致)。
func (c *Client) InspectContainer(id string) (Inspect, error) { return c.Inspect(id) }

func (c *Client) Stop(id string, timeoutSec int) error {
	return c.post("/containers/"+id+"/stop?t="+itoa(timeoutSec), nil)
}

func (c *Client) Start(id string) error {
	return c.post("/containers/"+id+"/start", nil)
}

// Volumes 返回按名索引的卷表。/volumes 的 Volumes 字段是**数组**(非对象),
// 这里解码数组后再建成 map。
func (c *Client) Volumes() (map[string]VolumeInfo, error) {
	var out struct {
		Volumes []VolumeInfo `json:"Volumes"`
	}
	if err := c.get("/volumes", &out); err != nil {
		return nil, err
	}
	m := make(map[string]VolumeInfo, len(out.Volumes))
	for _, v := range out.Volumes {
		m[v.Name] = v
	}
	return m, nil
}

// CreateVolume 建一个卷(幂等:已存在则不报错)。
func (c *Client) CreateVolume(name string) error {
	payload := map[string]any{"Name": name}
	req, err := http.NewRequest("POST", "http://docker/volumes/create", strings.NewReader(toJSON(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 409 { // 已存在,视为成功
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("POST /volumes/create(%s) 返回 %s: %s", name, resp.Status, truncate(string(body), 200))
	}
	return nil
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
