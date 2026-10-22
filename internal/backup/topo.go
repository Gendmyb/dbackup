package backup

import (
	"sort"
	"strings"

	"dbackup/internal/config"
)

// topoOrder 按依赖关系给容器排拓扑序(依赖在前)。
// deps: 显示名 → 其依赖的显示名列表(来自 compose depends_on 标签 + config 覆盖)。
// 返回顺序;环或未知依赖不致命,按字母序补在末尾(保证每个容器都出现一次)。
func topoOrder(names []string, deps map[string][]string) []string {
	// 只保留「依赖也在集合内」的边。
	inSet := map[string]bool{}
	for _, n := range names {
		inSet[n] = true
	}
	adj := map[string][]string{}
	indeg := map[string]int{}
	for _, n := range names {
		indeg[n] = 0
		adj[n] = []string{}
	}
	for _, n := range names {
		seen := map[string]bool{}
		for _, d := range deps[n] {
			d = strings.TrimSpace(d)
			if inSet[d] && d != n && !seen[d] {
				seen[d] = true
				adj[d] = append(adj[d], n) // d 先于 n
				indeg[n]++
			}
		}
	}
	// Kahn:每次取字母序最小的就绪节点,保证可复现。
	order := []string{}
	ready := []string{}
	for _, n := range names {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	sort.Strings(ready)
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		sort.Strings(adj[n])
		var newly []string
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				newly = append(newly, m)
			}
		}
		ready = append(ready, newly...)
		sort.Strings(ready)
	}
	// 环上剩余的节点按字母序补上。
	if len(order) < len(names) {
		done := map[string]bool{}
		for _, n := range order {
			done[n] = true
		}
		rest := []string{}
		for _, n := range names {
			if !done[n] {
				rest = append(rest, n)
			}
		}
		sort.Strings(rest)
		order = append(order, rest...)
	}
	return order
}

// reverse 返回切片副本的反序。
func reverse[T any](in []T) []T {
	out := make([]T, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// collectDeps 从 config 覆盖里汇总每个容器的依赖,并把 service key 翻译成显示名。
// (compose 的 depends_on 标签在 compose 层已消费;这里只需 config 显式声明。)
func collectDeps(names []string, cfg *config.Config, keyToName map[string]string) map[string][]string {
	deps := map[string][]string{}
	for _, n := range names {
		var d []string
		seen := map[string]bool{}
		if cfg != nil {
			for _, cand := range []string{n, keyToName[n]} {
				if o, ok := cfg.Containers[cand]; ok {
					for _, dep := range o.DependsOn {
						dep = strings.TrimSpace(dep)
						if dep == "" || seen[dep] {
							continue
						}
						seen[dep] = true
						if dn, ok := keyToName[dep]; ok {
							d = append(d, dn)
						}
					}
				}
			}
		}
		deps[n] = d
	}
	return deps
}
