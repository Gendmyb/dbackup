package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dbackup/internal/manifest"
	"gopkg.in/yaml.v3"
)

// pruneOld 保留最近 keep 份归档(按文件名里的时间戳排序),删除更旧的。
// 文件名形如 <name>-<ts>-<host>.tar,只匹配以 <name>- 开头的。
func pruneOld(dir, name string, now time.Time, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var tars []string
	prefix := name + "-"
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, prefix) && strings.HasSuffix(n, ".tar") && !strings.HasSuffix(n, ".tmp") {
			tars = append(tars, n)
		}
	}
	sort.Strings(tars) // 时间戳在文件名中段,字典序≈时间序
	if len(tars) <= keep {
		return
	}
	// 删最旧的(len-keep 个)。
	for i := 0; i < len(tars)-keep; i++ {
		os.Remove(filepath.Join(dir, tars[i]))
	}
}

// jsonToYAML 把任意值序列化成 YAML(用于归档里的 config.yaml)。
func jsonToYAML(v any) []byte {
	b, err := yaml.Marshal(v)
	if err != nil {
		return []byte("# config 序列化失败\n")
	}
	return b
}

// renderREADME 生成人类可读的恢复说明。entries 是规划好的数据条目(统计可能尚未填充)。
func renderREADME(man *manifest.Manifest, rootDir string, entries []manifest.DataEntry) string {
	var b strings.Builder
	b.WriteString("# 备份恢复说明\n\n")
	fmt.Fprintf(&b, "- 生成时间: %s\n", man.CreatedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&b, "- 工具: %s %s\n", man.ToolName, man.ToolVersion)
	fmt.Fprintf(&b, "- 备份模式: %s\n\n", man.Mode)
	fmt.Fprintf(&b, "## 源宿主\n\n")
	fmt.Fprintf(&b, "- 主机名: %s\n", man.Host.Hostname)
	fmt.Fprintf(&b, "- Docker: %s (API %s)\n", man.Host.DockerVersion, man.Host.ApiVersion)
	fmt.Fprintf(&b, "- 架构: %s / %s / 内核 %s\n\n", man.Host.Architecture, man.Host.Os, man.Host.Kernel)
	fmt.Fprintf(&b, "## 服务 (%d)\n\n", len(man.Services))
	for _, s := range man.Services {
		fmt.Fprintf(&b, "- `%s` → image `%s` (容器名 %s, %s)\n", s.ServiceKey, s.Image, s.Name, s.State)
	}
	b.WriteString("\n## 恢复步骤\n\n")
	b.WriteString("```\n")
	b.WriteString("mkdir restore && tar xf <备份.tar> -C restore\n")
	b.WriteString("cat restore/" + rootDir + "/docker-compose.yml\n")
	b.WriteString("cat restore/" + rootDir + "/data/index.json\n")
	b.WriteString("# 用 dbackup restore 一键恢复(会拉镜像、落数据、up):\n")
	b.WriteString("dbackup restore --tar <备份.tar>\n")
	b.WriteString("\n# 或手工:把 bind 目录放到 index.json 的 source 路径\n")
	b.WriteString("# (命名卷由 compose 自动创建),然后:\n")
	b.WriteString("cd restore/" + rootDir + "\n")
	b.WriteString("docker compose pull && docker compose up -d\n")
	b.WriteString("```\n\n")
	if len(entries) > 0 {
		b.WriteString("## 数据条目\n\n")
		b.WriteString("| id | 类型 | 源 | 归属服务 |\n|---|---|---|---|\n")
		for _, e := range entries {
			src := e.Source
			if e.Kind == "volume" && e.Anonymous {
				src = e.Source + " (匿名卷)"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", e.ID, e.Kind, src, strings.Join(e.Owners, ", "))
		}
		b.WriteString("\n")
	}
	if len(man.Warnings) > 0 {
		b.WriteString("## 警告(恢复前请阅读)\n\n")
		for _, w := range man.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
		b.WriteString("\n")
	}
	return b.String()
}
