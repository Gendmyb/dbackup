package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dbackup/internal/backup"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var (
		host, cfgPath, outDir, name, mode string
		stopTimeout, keep                 int
		every                             time.Duration
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "内置定时备份(无 cron 环境的替代)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}
			if every <= 0 {
				every = 6 * time.Hour
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			runOnce := func() {
				c, err := newClient(host)
				if err != nil {
					fmt.Printf("[%s] 连接失败: %v\n", time.Now().Format(time.RFC3339), err)
					return
				}
				opts := backup.Options{
					OutDir:      outDir,
					Name:        name,
					Mode:        mode,
					StopTimeout: stopTimeout,
					Keep:        keep,
					Config:      cfg,
				}
				res, err := backup.Run(c, opts)
				if err != nil {
					fmt.Printf("[%s] 备份失败: %v\n", time.Now().Format(time.RFC3339), err)
					return
				}
				fmt.Printf("[%s] 备份完成: %s\n", time.Now().Format(time.RFC3339), res.TarPath)
			}

			fmt.Printf("serve:每 %s 备份一次(out=%s)\n", every, outDir)
			runOnce()
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					fmt.Println("serve:收到退出信号,停止。")
					return nil
				case <-t.C:
					runOnce()
				}
			}
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "docker endpoint")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "config.yaml")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "输出目录(缺省 ./backups)")
	cmd.Flags().StringVar(&name, "name", "", "归档名前缀")
	cmd.Flags().StringVar(&mode, "mode", "cold", "cold 或 live")
	cmd.Flags().IntVar(&stopTimeout, "stop-timeout", 0, "停容器等待秒数")
	cmd.Flags().IntVarP(&keep, "keep", "k", 0, "保留最近 N 份")
	cmd.Flags().DurationVar(&every, "every", 6*time.Hour, "间隔(如 6h)")
	return cmd
}
