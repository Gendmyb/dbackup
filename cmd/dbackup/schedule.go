package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// selfCmdLine 生成「本二进制 backup ...」的可执行命令行。
func selfCmdLine(args ...string) string {
	exe, err := os.Executable()
	if err != nil {
		exe = "dbackup"
	}
	return strings.Join(append([]string{exe}, args...), " ")
}

func installCronCmd() *cobra.Command {
	var (
		host, cfgPath, outDir, name string
		keep                        int
		every                       string
		dryRun                      bool
	)
	cmd := &cobra.Command{
		Use:   "install-cron",
		Short: "生成并写入 cron 行(需 crontab 权限)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 把 --every(如 6h)转成 cron 间隔说明;cron 用简单字段,这里给一个「每小时第 N 分钟」默认。
			extra := []string{"backup"}
			if host != "" {
				extra = append(extra, "--host", host)
			}
			if cfgPath != "" {
				extra = append(extra, "-c", cfgPath)
			}
			if outDir != "" {
				extra = append(extra, "-o", outDir)
			}
			if name != "" {
				extra = append(extra, "--name", name)
			}
			if keep > 0 {
				extra = append(extra, "-k", fmt.Sprintf("%d", keep))
			}
			line := "0 */6 * * * " + selfCmdLine(extra...) + " >> /var/log/dbackup.cron.log 2>&1"
			if every != "" {
				line = "// 间隔 " + every + "(cron 无原生间隔,用上面时间或 crontab 自调)\n" + line
			}
			if dryRun {
				fmt.Println("将写入 crontab:")
				fmt.Println(line)
				return nil
			}
			// 读现有 crontab,追加(幂等:已有 dbackup 行则替换)。
			existing := ""
			if out, err := exec.Command("crontab", "-l").CombinedOutput(); err == nil {
				existing = string(out)
			}
			var keep []string
			for _, l := range strings.Split(existing, "\n") {
				if strings.Contains(l, "dbackup") && !strings.HasPrefix(strings.TrimSpace(l), "#") {
					continue
				}
				keep = append(keep, l)
			}
			newTab := strings.Join(keep, "\n")
			if strings.TrimSpace(newTab) != "" {
				newTab += "\n"
			}
			newTab += line + "\n"
			in := strings.NewReader(newTab)
			c := exec.Command("crontab", "-")
			c.Stdin = in
			if out, err := c.CombinedOutput(); err != nil {
				return fmt.Errorf("写入 crontab 失败: %w\n%s", err, out)
			}
			fmt.Println("已写入 crontab:")
			fmt.Println(line)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "docker endpoint")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "config.yaml")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "输出目录")
	cmd.Flags().StringVar(&name, "name", "", "归档名前缀")
	cmd.Flags().IntVarP(&keep, "keep", "k", 0, "保留最近 N 份")
	cmd.Flags().StringVar(&every, "every", "", "期望间隔(仅注释提示,cron 用时间字段)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只打印,不写入")
	return cmd
}

func installTimerCmd() *cobra.Command {
	var (
		host, cfgPath, outDir, name string
		keep    int
		every   string
		unitDir string
		dryRun  bool
	)
	cmd := &cobra.Command{
		Use:   "install-timer",
		Short: "生成并安装 systemd timer(需相应权限,日志进 journal)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if every == "" {
				every = "6h"
			}
			if unitDir == "" {
				unitDir = "/etc/systemd/system"
			}
			extra := []string{"backup"}
			if host != "" {
				extra = append(extra, "--host", host)
			}
			if cfgPath != "" {
				extra = append(extra, "-c", cfgPath)
			}
			if outDir != "" {
				extra = append(extra, "-o", outDir)
			}
			if name != "" {
				extra = append(extra, "--name", name)
			}
			if keep > 0 {
				extra = append(extra, "-k", fmt.Sprintf("%d", keep))
			}
			exe, _ := os.Executable()
			service := "[Unit]\nDescription=dbackup 定时备份\nAfter=docker.service\n\n" +
				"[Service]\nType=oneshot\nExecStart=" + filepath.Clean(exe) + " " + strings.Join(extra, " ") + "\n"
			timer := "[Unit]\nDescription=dbackup 定时触发\n\n" +
				"[Timer]\nOnBootSec=15min\nOnUnitActiveSec=" + every + "\nPersistent=true\n\n" +
				"[Install]\nWantedBy=timers.target\n"
			svcPath := filepath.Join(unitDir, "dbackup.service")
			tmrPath := filepath.Join(unitDir, "dbackup.timer")

			if dryRun {
				fmt.Println("--- dbackup.service ---")
				fmt.Println(service)
				fmt.Println("--- dbackup.timer ---")
				fmt.Println(timer)
				return nil
			}
			if err := os.WriteFile(svcPath, []byte(service), 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(tmrPath, []byte(timer), 0o644); err != nil {
				return err
			}
			for _, a := range [][]string{
				{"systemctl", "daemon-reload"},
				{"systemctl", "enable", "--now", "dbackup.timer"},
			} {
				if out, err := exec.Command(a[0], a[1:]...).CombinedOutput(); err != nil {
					return fmt.Errorf("%v: %w\n%s", a, err, out)
				}
			}
			fmt.Println("已安装并启用 dbackup.timer(间隔", every, ")。")
			fmt.Println("查看: systemctl list-timers dbackup.timer; journalctl -u dbackup.service")
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "docker endpoint")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "config.yaml")
	cmd.Flags().StringVarP(&outDir, "out", "o", "", "输出目录")
	cmd.Flags().StringVar(&name, "name", "", "归档名前缀")
	cmd.Flags().IntVarP(&keep, "keep", "k", 0, "保留最近 N 份")
	cmd.Flags().StringVar(&every, "every", "6h", "触发间隔")
	cmd.Flags().StringVar(&unitDir, "unit-dir", "/etc/systemd/system", "unit 目录")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "只打印,不安装")
	return cmd
}
