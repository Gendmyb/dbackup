package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"dbackup/internal/compose"
	"dbackup/internal/dc"
	"github.com/spf13/cobra"
)

type scanOutput struct {
	Containers []scanContainer `json:"containers"`
	Compose    *compose.Doc    `json:"compose_preview"`
	Warnings   []string        `json:"warnings"`
}

type scanContainer struct {
	Name    string      `json:"name"`
	State   string      `json:"state"`
	Image   string      `json:"image"`
	Mounts  []dc.Mount  `json:"mounts"`
	Ports   string      `json:"ports"`
	Network string      `json:"network"`
	Labels  map[string]string `json:"labels,omitempty"`
}

func scanCmd() *cobra.Command {
	var jsonOut bool
	var host, cfgPath string
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "列出容器、挂载,生成 compose 预览",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(host)
			if err != nil {
				return err
			}
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}
			all, err := c.Containers(true)
			if err != nil {
				return err
			}
			var names []string
			var inputs []compose.ContainerInput
			var out scanOutput
			nameSet, excludeSet := map[string]bool{}, map[string]bool{}
			for _, x := range cfg.Exclude {
				excludeSet[x] = true
			}
			if !cfg.IsAll() {
				for _, x := range cfg.Include {
					nameSet[x] = true
				}
			}
			for _, s := range all {
				if len(s.Names) == 0 {
					continue
				}
				dn := strings.TrimPrefix(s.Names[0], "/")
				if cfg.IsAll() {
					if excludeSet[dn] {
						continue
					}
				} else if !nameSet[dn] {
					continue
				}
				names = append(names, dn)
				insp, err := c.Inspect(s.ID)
				if err != nil {
					fmt.Fprintf(os.Stderr, "警告: inspect %s 失败: %v\n", dn, err)
					continue
				}
				inputs = append(inputs, compose.ContainerInput{Name: dn, Insp: insp})
				sc := scanContainer{
					Name:    dn,
					State:   s.State,
					Network: s.HostConfig.NetworkMode,
					Labels:  s.Labels,
				}
				if insp.Config != nil {
					sc.Image = insp.Config.Image
				}
				for _, p := range s.Ports {
					sc.Ports = addPort(sc.Ports, p)
				}
				sc.Mounts = insp.Mounts
				out.Containers = append(out.Containers, sc)
			}
			sort.Slice(out.Containers, func(i, j int) bool { return out.Containers[i].Name < out.Containers[j].Name })
			doc, _, warns := compose.Generate(inputs)
			out.Compose = doc
			out.Warnings = warns

			if jsonOut {
				b, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(b))
				return nil
			}
			// 人类可读
			fmt.Printf("共 %d 个容器(按配置):\n\n", len(out.Containers))
			for _, sc := range out.Containers {
				fmt.Printf("  %-24s %-10s %s\n", sc.Name, sc.State, sc.Image)
				if sc.Ports != "" {
					fmt.Printf("      端口: %s\n", sc.Ports)
				}
				for _, m := range sc.Mounts {
					fmt.Printf("      挂载: %-18s %s → %s %s\n", m.Type, m.Source, m.Destination, m.Mode)
				}
			}
			if len(out.Warnings) > 0 {
				fmt.Printf("\n警告:\n")
				for _, w := range out.Warnings {
					fmt.Printf("  - %s\n", w)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "输出完整 JSON 清单")
	cmd.Flags().StringVar(&host, "host", "", "docker endpoint(缺省 $DOCKER_HOST 或 unix:///var/run/docker.sock)")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "config.yaml(选容器/覆盖)")
	return cmd
}

func addPort(cur string, p dc.PortSummary) string {
	var s string
	if p.IP != "" {
		s = fmt.Sprintf("%s:%d→%d/%s", p.IP, p.PublicPort, p.PrivatePort, p.Type)
	} else {
		s = fmt.Sprintf("%d→%d/%s", p.PublicPort, p.PrivatePort, p.Type)
	}
	if cur == "" {
		return s
	}
	return cur + ", " + s
}
