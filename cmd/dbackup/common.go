// Package main 是 dbackup 的 CLI 入口。
// 子命令:scan / backup / restore / verify / serve / install-cron / install-timer。
package main

import (
	"fmt"
	"os"

	"dbackup/internal/config"
	"dbackup/internal/dc"
)

// dockerHost 解析 daemon socket:优先 --host flag,其次 DOCKER_HOST,缺省标准路径。
func dockerHost(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("DOCKER_HOST"); env != "" {
		return env
	}
	return "unix:///var/run/docker.sock"
}

func newClient(flagHost string) (*dc.Client, error) {
	endpoint := dockerHost(flagHost)
	c, err := dc.New(endpoint)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// loadConfig 读入配置文件(可为空)。
func loadConfig(path string) (*config.Config, error) {
	if path == "" {
		return &config.Config{}, nil
	}
	return config.Load(path)
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", a...)
	os.Exit(1)
}
