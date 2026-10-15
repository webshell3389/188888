package server

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// 企业后台 /api/* 兼容层。
//
// 收敛后仅保留 Vue 控制台依赖的三个端点：
//
//	/api/stats        → 仪表台 KPI（DashboardView）
//	/api/data/photos  → 照片实体（PhotosView）
//	/api/file/{id}    → 原始字节流（照片预览/下载）
//
// 其余 /api/* 历史端点（activity / list / tasks / device / logout / dispatch /
// data/memos / data/wallets）已随前端全量迁移 Vue 下线：对应数据由 /__dash/api、
// /panel/api/* 单一来源承载，不再重复暴露。

func apiErr(w http.ResponseWriter, msg string, code int) {
	writeJSON(w, code, map[string]any{"ok": false, "reason": msg})
}

func apiOK(w http.ResponseWriter, payload map[string]any) {
	if _, ok := payload["ok"]; !ok {
		payload["ok"] = true
	}
	writeJSON(w, http.StatusOK, payload)
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
			"tasks":          tasksDone + tasksPending, "tasks_done": tasksDone, "tasks_pending": tasksPending,
		})
	})

	rt.GET("/api/data/photos", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		limit := queryInt(r, "limit", 60)
		rows := store.GetDB().Query(
			"SELECT id, recv_ts, file_path, size, sha256 FROM packets"+
				" WHERE endpoint='p' ORDER BY recv_epoch DESC LIMIT ?", limit)
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
}
