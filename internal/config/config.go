// Package config 加载备份/恢复的可选覆盖配置 (config.yaml)。
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 对应 ARCHITECTURE.md §8。
type Config struct {
	// Include: "all" 或容器名列表。缺省 all。
	Include []string `yaml:"include"`
	// Exclude: 排除的容器名。
	Exclude []string `yaml:"exclude"`
	// StopTimeout 停容器等待秒数。
	StopTimeout int `yaml:"stop_timeout"`
	// Keep 保留最近 N 份备份。
	Keep int `yaml:"keep"`
	// Out 默认输出目录。
	Out string `yaml:"out"`
	// Name 归档名前缀。
	Name string `yaml:"name"`
	// Containers 按 service key 的覆盖。
	Containers map[string]ContainerOverride `yaml:"containers"`
}

type ContainerOverride struct {
	// DependsOn 显式依赖(恢复写进 compose;inspect 无法还原)。
	DependsOn []string `yaml:"depends_on"`
	// SkipMounts 备份时跳过的容器内挂载目标路径。
	SkipMounts []string `yaml:"skip_mounts"`
	// ExtraData 额外要备份的宿主目录。
	ExtraData []string `yaml:"extra_data"`
	// Compose 深合并进生成的 service(最后生效)。
	Compose map[string]any `yaml:"compose"`
	// RestoreCompose 仅恢复时生效的覆盖。
	RestoreCompose map[string]any `yaml:"restore_compose"`
}

// Load 读入 config 文件;路径为空时返回零值。
func Load(path string) (*Config, error) {
	cfg := &Config{}
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	return cfg, nil
}

// IsAll 报告 Include 是否为「全部」。
func (c *Config) IsAll() bool {
	return len(c.Include) == 0 || len(c.Include) == 1 && (c.Include[0] == "all" || c.Include[0] == "*")
}
