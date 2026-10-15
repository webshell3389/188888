package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// boot.html 同步信标（每 2min POST）+ 同步日志 sink（GET /log.html）。

func registerSync(rt *Router) {
	rt.POST("/api/ip-sync/sync", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		// sync 心跳不落盘、不进 packets（留存策略：小包只进台账/日志）。
		// 台账写入在下方 devices.jsonl + RecordDevice 完成。

		var ver, domain string
		var j map[string]any
		if json.Unmarshal(body, &j) == nil {
			ver = store.ToStr(j["deviceVersion"])
			domain = store.ToStr(j["domain"])
		}
		rec := map[string]any{
			"ts":            nowString(),
			"deviceVersion": ver,
			"domain":        domain,
			"ip":            clientIP(r),
			"ua":            r.Header.Get("User-Agent"),
			"host":          hostHeader(r),
		}
		store.GetStore().WriteJSONL("devices.jsonl", rec)
		store.SafeCall(func() { store.RecordDevice(rec) })
		writeJSON(w, http.StatusOK, map[string]any{"code": 200, "status": "ok"})
	})

	rt.GET("/log.html", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		q := r.URL.Query()
		entry := map[string]any{
			"ts":      nowString(),
			"channel": defaultStr(r.Header.Get("Referer"), "-"),
			"id":      q.Get("id"),
			"hex":     defaultStr(q.Get("hex"), "0"),
			"text":    q.Get("text"),
		}
		store.GetStore().WriteJSONL("console.jsonl", entry)
		store.SafeCall(func() { store.RecordProbe(entry) })
		writeText(w, config.Get().LogSinkStatus, "")
	})
}

func defaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// 业务 C2 端点：/beacon（可驱动命令） /result /heartbeat。
func registerC2(rt *Router) {
	rt.POST("/beacon", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		s := config.Get()
		store.GetStore().SaveBody("beacon", body, "json", s.BodyCaptureLimit, clientIP(r))
		// 指令下发模块：先按设备独立队列取（uuid → ip → 广播），取不到再回落全局单行队列。
		// 这样一台设备不来心跳也不会占住别人的指令 —— 批量下发互不阻塞。
		var buuid string
		var bj map[string]any
		if json.Unmarshal(body, &bj) == nil {
			buuid = store.ToStr(bj["uuid"])
		}
		cmd := store.GetQueue().TakeFor([]string{buuid, clientIP(r)})
		if cmd == nil {
			cmd = store.GetQueue().Take()
		}
		var shape map[string]any
		if cmd != nil {
			shape = map[string]any{}
			for k, v := range cmd {
				shape[k] = v
			}
			shape["client_ip"] = defaultStr(clientIP(r), "0.0.0.0")
			store.GetStore().WriteJSONL("dispatch.jsonl", map[string]any{
				"ts": nowString(), "dispatched": cmd, "client": shape["client_ip"]})
			store.SafeCall(func() { store.CommandDispatched(cmd, shape["client_ip"].(string)) })
		} else {
			shape = jsonShape(store.GetControl().Template(), r)
		}
		writeJSON(w, http.StatusOK, shape)
	})

	rt.POST("/result", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		s := config.Get()
		store.GetStore().SaveBody("result", body, "json", s.BodyCaptureLimit, clientIP(r))
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	rt.Add([]string{"GET", "POST"}, "/heartbeat", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		// 心跳只进日志流水（heartbeat.jsonl），不落盘不进 packets（留存策略）。
		b := string(body)
		if len(b) > 256 {
			b = b[:256]
		}
		store.GetStore().WriteJSONL("heartbeat.jsonl", map[string]any{
			"ts": nowString(), "ip": clientIP(r), "ua": r.Header.Get("User-Agent"), "body": b})
		writeText(w, http.StatusOK, "ok")
	})
}

// jsonShape 应用 %PEER_IP% 占位替换。
func jsonShape(shape map[string]any, r *http.Request) map[string]any {
	ip := defaultStr(clientIP(r), "0.0.0.0")
	out := make(map[string]any, len(shape))
	for k, v := range shape {
		if s, ok := v.(string); ok {
			out[k] = strings.ReplaceAll(s, "%PEER_IP%", ip)
		} else {
			out[k] = v
		}
	}
	return out
}
