package main

import (
	"fmt"

	"dbackup/internal/backup"
	"github.com/spf13/cobra"
)

func backupCmd() *cobra.Command {
	var (
		host, cfgPath, outDir, name, mode string
		stopTimeout, keep                 int
		leaveStopped, dryRun              bool
	)
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "执行备份(默认冷备份:停容器→拷贝→恢复)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(host)
			if err != nil {
				return err
			}
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}
			opts := backup.Options{
				OutDir:       outDir,
				Name:         name,
				Mode:         mode,
				StopTimeout:  stopTimeout,
				Keep:         keep,
				LeaveStopped: leaveStopped,
				DryRun:       dryRun,
				Config:       cfg,
			}
			if dryRun {
				fmt.Println("dry-run:只规划,不落盘。")
			}
			res, err := backup.Run(c, opts)
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Printf("将备份 %d 个服务\n", len(res.Manifest.Services))
				if len(res.Manifest.Warnings) > 0 {
					fmt.Println("警告:")
					for _, w := range res.Manifest.Warnings {
						fmt.Printf("  - %s\n", w)
					}
				}
				return nil
			}
			fmt.Printf("备份完成: %s\n", res.TarPath)
			fmt.Printf("  服务数: %d\n", len(res.Manifest.Services))
			if len(res.Stopped) > 0 {
				fmt.Printf("  已冷备份并拉起 %d 个容器: %v\n", len(res.Stopped), res.Stopped)
			}
			if len(res.Manifest.Warnings) > 0 {
				fmt.Printf("  警告:\n")
				for _, w := range res.Manifest.Warnings {
					fmt.Printf("    - %s\n", w)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "docker endpoint")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "config.yaml")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "输出目录(缺省 ./backups)")
	cmd.Flags().StringVar(&name, "name", "", "归档名前缀(缺省 主机名)")
	cmd.Flags().StringVar(&mode, "mode", "cold", "cold 或 live")
	cmd.Flags().IntVar(&stopTimeout, "stop-timeout", 0, "停容器等待秒数(缺省 10)")
	cmd.Flags().IntVarP(&keep, "keep", "k", 0, "保留最近 N 份")
	cmd.Flags().BoolVar(&leaveStopped, "leave-stopped", false, "备份后不停拉起(留在停止态)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只规划,不落盘")
	return cmd
}
