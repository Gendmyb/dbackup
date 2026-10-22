package backup

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"dbackup/internal/config"
	"dbackup/internal/dc"
	"dbackup/internal/manifest"
)

// Selection 是备份要处理的容器集合(按显示名组织)。
type Selection struct {
	// Containers 显示名(无前导 /)→ inspect。
	Containers map[string]dc.Inspect
	// ServiceKey 显示名 → compose service key。
	ServiceKey map[string]string
	// Project 推断出的 compose project 名(若容器带 compose 标签)。
	Project string
}

// PlanData 规划数据条目:遍历选定容器的挂载,去重(bind 按宿主路径,volume 按卷名),
// 处理 skip_mounts 与 extra_data,分配稳定的 entry-id。
func PlanData(sel *Selection, ov map[string]config.ContainerOverride, volumes map[string]dc.VolumeInfo) []manifest.DataEntry {
	type key struct{ kind, source string }
	var order []key
	byKey := map[key]*manifest.DataEntry{}

	add := func(kind, source, volumeName, ownerKey, containerPath string) {
		k := key{kind, source}
		e, ok := byKey[k]
		if !ok {
			e = &manifest.DataEntry{Kind: kind, Source: source, VolumeName: volumeName}
			if kind == "volume" {
				if vi, ok := volumes[volumeName]; ok {
					e.Mountpoint = vi.Mountpoint
					e.Anonymous = vi.IsAnonymous()
				}
			}
			byKey[k] = e
			order = append(order, k)
		}
		if !containsStr(e.Owners, ownerKey) {
			e.Owners = append(e.Owners, ownerKey)
		}
		if !containsStr(e.ContainerPaths, containerPath) {
			e.ContainerPaths = append(e.ContainerPaths, containerPath)
		}
	}

	names := make([]string, 0, len(sel.Containers))
	for n := range sel.Containers {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		insp := sel.Containers[name]
		key := sel.ServiceKey[name]
		skip := map[string]bool{}
		var extra []string
		seenCand := map[string]bool{}
		for _, cand := range []string{name, key} {
			if seenCand[cand] {
				continue
			}
			seenCand[cand] = true
			if o, ok := ov[cand]; ok {
				for _, s := range o.SkipMounts {
					skip[s] = true
				}
				extra = append(extra, o.ExtraData...)
			}
		}
		for _, d := range extra {
			add("bind", d, "", key, "(extra)")
		}
		for _, m := range insp.Mounts {
			if skip[m.Destination] {
				continue
			}
			switch m.Type {
			case "bind":
				add("bind", m.Source, "", key, m.Destination)
			case "volume":
				add("volume", m.Name, m.Name, key, m.Destination)
			}
		}
	}

	entries := make([]manifest.DataEntry, 0, len(order))
	for i, k := range order {
		e := byKey[k]
		e.ID = entryID(i+1, k.source)
		entries = append(entries, *e)
	}
	return entries
}

func entryID(n int, source string) string {
	base := path.Base(source)
	if base == "" || base == "." || base == "/" {
		base = source
	}
	san := nonAlnum.ReplaceAllString(base, "-")
	san = strings.Trim(san, "-")
	if san == "" {
		san = "entry"
	}
	if len(san) > 40 {
		san = san[:40]
	}
	return pad2(n) + "-" + san
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
