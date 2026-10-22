package main

import (
	"fmt"
	"strings"

	"dbackup/internal/dc"
	"dbackup/internal/restore"
	"github.com/spf13/cobra"
)

func restoreCmd() *cobra.Command {
	var (
		host, cfgPath, tarPath, dataDir, project, composeOut string
		dryRun, noPull, startAll                             bool
	)
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "从备份 tar 恢复(落数据 + 调整 compose + up)",
		RunE: func(cmd *cobra.Command, args []string) error {
			in := restore.Options{
				Tar:        tarPath,
				DataDir:    dataDir,
				Project:    project,
				ComposeOut: composeOut,
				DryRun:     dryRun,
				NoPull:     noPull,
				StartAll:   startAll,
			}
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}
			in.Config = cfg

			// compose-out / dry-run 不需要 daemon;部署需要。
			var c *dc.Client
			if !dryRun && composeOut == "" {
				c, err = newClient(host)
				if err != nil {
					return err
				}
			}
			rep, err := restore.Run(c, in)
			if err != nil {
				return err
			}
			if dryRun {
				fmt.Println("dry-run,将执行:")
				for _, a := range rep.Actions {
					fmt.Printf("  - %s\n", a)
				}
				return nil
			}
			if composeOut != "" {
				fmt.Printf("已导出调整后的 compose: %s\n", composeOut)
				return nil
			}
			fmt.Printf("恢复完成(project=%s)。\n", rep.Project)
			if len(rep.MatchedStopped) > 0 {
				fmt.Printf("按备份状态保持停止(%d): %s\n", len(rep.MatchedStopped), strings.Join(rep.MatchedStopped, ", "))
			}
			if rep.ServiceOut != "" {
				fmt.Println(rep.ServiceOut)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "docker endpoint")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "恢复侧覆盖 config.yaml")
	cmd.Flags().StringVar(&tarPath, "tar", "", "备份 tar 路径(必需)")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "数据落盘根目录(缺省=原始绝对路径)")
	cmd.Flags().StringVar(&project, "project", "", "compose project 名(缺省=manifest 记录的)")
	cmd.Flags().StringVar(&composeOut, "compose-out", "", "只导出调整后 compose,不部署")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "打印将执行的动作")
	cmd.Flags().BoolVar(&noPull, "no-pull", false, "不拉镜像")
	cmd.Flags().BoolVar(&startAll, "start-all", false, "up 后全部服务保持运行(缺省=按备份时运行状态恢复,原停的仍停)")
	_ = cmd.MarkFlagRequired("tar")
	return cmd
}
