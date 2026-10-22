package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "dbackup",
	Short:         "把 docker 容器 + 本地数据备份成可恢复的 tar",
	Long:          "dbackup 扫描 docker 容器,生成等价的 docker-compose,连同本地挂载数据打包为单个 tar;支持手动/定时,跨架构恢复。",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func main() {
	rootCmd.AddCommand(
		scanCmd(),
		backupCmd(),
		restoreCmd(),
		verifyCmd(),
		serveCmd(),
		installCronCmd(),
		installTimerCmd(),
	)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
