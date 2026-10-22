package main

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"dbackup/internal/manifest"
	"github.com/spf13/cobra"
)

func verifyCmd() *cobra.Command {
	var tarPath string
	var strict bool
	cmd := &cobra.Command{
		Use:   "verify --tar FILE",
		Short: "校验备份包:manifest 完整性、数据条目齐全、大小对账",
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(tarPath)
			if err != nil {
				return err
			}
			defer f.Close()
			tr := tar.NewReader(f)

			var manB, idxB, composeB, readmeB []byte
			var rootDir string
			prefixStat := map[string]*struct{ bytes, files int64 }{}
			dataPresent := map[string]bool{}

			for {
				hdr, herr := tr.Next()
				if herr == io.EOF {
					break
				}
				if herr != nil {
					return herr
				}
				name := hdr.Name
				rel := name
				if strings.HasPrefix(name, rootDir+"/") {
					rel = strings.TrimPrefix(name, rootDir+"/")
				} else if strings.HasSuffix(name, "/") && rootDir == "" {
					rootDir = strings.TrimSuffix(name, "/")
					continue
				}
				switch {
				case rel == "manifest.json":
					manB, _ = io.ReadAll(tr)
				case rel == "data/index.json":
					idxB, _ = io.ReadAll(tr)
				case rel == "docker-compose.yml":
					composeB, _ = io.ReadAll(tr)
				case rel == "README.md":
					readmeB, _ = io.ReadAll(tr)
				case strings.HasPrefix(rel, "data/"):
					id := rel[len("data/"):]
					id = strings.TrimSuffix(id, "/")
					if i := strings.Index(id, "/"); i > 0 {
						id = id[:i]
					}
					if id != "" && id != "index.json" {
						dataPresent[id] = true
						st := prefixStat[id]
						if st == nil {
							st = &struct{ bytes, files int64 }{}
							prefixStat[id] = st
						}
						if hdr.Typeflag != tar.TypeDir {
							st.bytes += hdr.Size
							st.files++
						}
					}
				}
			}

			ok := true
			if rootDir == "" {
				return fmt.Errorf("找不到根目录")
			}
			if manB == nil {
				return fmt.Errorf("缺少 manifest.json")
			}
			if composeB == nil {
				return fmt.Errorf("缺少 docker-compose.yml")
			}
			var man manifest.Manifest
			if err := json.Unmarshal(manB, &man); err != nil {
				return fmt.Errorf("manifest 解析失败: %w", err)
			}
			var idx manifest.DataIndex
			if idxB != nil {
				if err := json.Unmarshal(idxB, &idx); err != nil {
					return fmt.Errorf("index 解析失败: %w", err)
				}
			}

			fmt.Printf("备份: %s\n", path.Base(tarPath))
			fmt.Printf("  根目录: %s\n", rootDir)
			fmt.Printf("  服务数: %d, 警告: %d\n", len(man.Services), len(man.Warnings))
			if readmeB == nil {
				fmt.Println("  缺少 README.md")
				ok = false
			}
			fmt.Printf("  数据条目: %d\n", len(idx.Entries))
			for _, e := range idx.Entries {
				present := dataPresent[e.ID]
				gotSize, gotFiles := int64(0), int64(0)
				if st := prefixStat[e.ID]; st != nil {
					gotSize, gotFiles = st.bytes, st.files
				}
				status := "OK"
				if !present {
					status = "缺失"
					ok = false
				} else if e.Size > 0 && gotSize == 0 {
					status = "空(声明非空)"
					ok = false
				}
				fmt.Printf("    %-42s %-6s 声明=%dB/%d文件 实际=%dB/%d文件 [%s]\n",
					e.ID, e.Kind, e.Size, e.FileCount, gotSize, gotFiles, status)
			}
			if ok {
				fmt.Println("\n✓ 校验通过")
			} else {
				fmt.Println("\n✗ 有差异")
				if strict {
					os.Exit(2)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tarPath, "tar", "", "备份 tar 路径(必需)")
	cmd.Flags().BoolVar(&strict, "strict", false, "发现差异时以非零码退出")
	_ = cmd.MarkFlagRequired("tar")
	return cmd
}
