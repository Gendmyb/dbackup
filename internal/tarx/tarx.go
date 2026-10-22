// Package tarx 提供自研 tar 读写:保留 mode/uid/gid/mtime/符号链接/硬链接。
// 不依赖 shell tar,避免目标机 tar 版本差异;Go 自己走目录树(ARCHITECTURE.md §2)。
package tarx

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Stats 是一次打包的统计。
type Stats struct {
	Size      int64
	FileCount int64
	DirCount  int64
}

// WriteTree 把 srcDir 的内容写进 tw,tar 内根路径为 rootName(如 "data/01-config")。
// 保留 mode/uid/gid/mtime,支持硬链接去重与符号链接;srcDir 本身映射为 rootName。
func WriteTree(tw *tar.Writer, srcDir, rootName string) (Stats, error) {
	var st Stats
	seen := map[uint64]string{} // dev:ino → 首个 tar 名(硬链接去重)
	rootName = path.Clean(rootName)

	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(srcDir, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			rel = ""
		}
		tarName := rootName
		if rel != "" {
			tarName = path.Join(rootName, filepath.ToSlash(rel))
		}

		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		mode := info.Mode()
		var uid, gid int
		var ino, dev uint64
		if sst, ok := info.Sys().(*syscall.Stat_t); ok {
			uid, gid, ino, dev = int(sst.Uid), int(sst.Gid), sst.Ino, sst.Dev
		}
		hdr := &tar.Header{
			Name:    tarName,
			Mode:    int64(mode.Perm()),
			Uid:     uid,
			Gid:     gid,
			ModTime: info.ModTime(),
		}

		switch {
		case mode.IsDir():
			hdr.Typeflag = tar.TypeDir
			hdr.Name = tarName + "/"
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			st.DirCount++

		case mode&os.ModeSymlink != 0:
			target, lerr := os.Readlink(p)
			if lerr != nil {
				return lerr
			}
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = target
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			st.FileCount++

		case mode.IsRegular():
			hdr.Size = info.Size()
			key := dev<<32 | ino
			if first, ok := seen[key]; ok && info.Size() > 0 {
				hdr.Typeflag = tar.TypeLink
				hdr.Linkname = first
				hdr.Size = 0
				if err := tw.WriteHeader(hdr); err != nil {
					return err
				}
				st.FileCount++
				return nil
			}
			seen[key] = tarName
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			f, oerr := os.Open(p)
			if oerr != nil {
				return oerr
			}
			if _, err := io.Copy(tw, f); err != nil {
				f.Close()
				return err
			}
			f.Close()
			st.Size += info.Size()
			st.FileCount++

		default:
			hdr.Typeflag = tar.TypeRegA
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return st, fmt.Errorf("打包 %s: %w", srcDir, err)
	}
	return st, nil
}

// Extract 把 tr 里属于 prefix(tar 内根,如 "data/01-config")的条目解到
// destDir/<prefix 去掉前缀后的相对路径>,保留 mode/uid/gid/mtime,处理符号/硬链接。
func Extract(tr *tar.Reader, prefix, destDir string) (files int64, bytes int64, err error) {
	prefix = path.Clean(prefix)
	hardlinks := map[string]string{} // tar 名 → 已解出路径(硬链接源)
	for {
		hdr, herr := tr.Next()
		if herr == io.EOF {
			break
		}
		if herr != nil {
			return files, bytes, err
		}
		name := path.Clean(hdr.Name)
		if !hasPrefixPath(name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			continue
		}
		target := filepath.Join(destDir, rel)

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return files, bytes, err
			}
			chownMtime(target, hdr.Uid, hdr.Gid, hdr.ModTime)
		case tar.TypeSymlink:
			os.Remove(target)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, bytes, err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return files, bytes, err
			}
			files++
		case tar.TypeLink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return files, bytes, err
			}
			if err := os.Link(hardlinks[hdr.Linkname], target); err != nil {
				return files, bytes, err
			}
			files++
		case tar.TypeReg, tar.TypeRegA:
			if err := writeRegular(target, tr, hdr); err != nil {
				return files, bytes, err
			}
			hardlinks[name] = target
			files++
			bytes += hdr.Size
		}
	}
	return files, bytes, nil
}

func writeRegular(target string, r io.Reader, hdr *tar.Header) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	f.Close()
	chownMtime(target, hdr.Uid, hdr.Gid, hdr.ModTime)
	return nil
}

// chownMtime 尽力保留属主与时间;非 root 时 chown 失败不致命。
func chownMtime(p string, uid, gid int, mtime time.Time) {
	os.Chtimes(p, mtime, mtime)
	_ = os.Chown(p, uid, gid)
}

func hasPrefixPath(name, prefix string) bool {
	return name == prefix || (len(name) > len(prefix) && name[:len(prefix)+1] == prefix+"/")
}
