package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// 管理授权路由 + 前端静态挂载（登录页 + 单页后台）。

var adminAssetRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

func registerAdmin(rt *Router) {
	rt.POST("/__auth/login", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		body := readBody(r)
		var j map[string]any
		if json.Unmarshal(body, &j) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad json"})
			return
		}
		sid, ok := AdminLogin(store.ToStr(j["username"]), store.ToStr(j["password"]))
		if !ok {
			// 登录失败审计留痕（失败来源与频次可观测）
			store.GetStore().WriteJSONL("control.jsonl", map[string]any{
				"action": "auth_fail", "ip": clientIP(r), "ts": nowString()})
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "bad password"})
			return
		}
		setSessionCookie(w, sid)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	rt.POST("/__auth/logout", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if c, err := r.Cookie(adminCookie); err == nil {
			AdminLogout(c.Value)
		}
		clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	rt.GET("/__auth/me", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		sid := ""
		if c, err := r.Cookie(adminCookie); err == nil {
			sid = c.Value
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": AdminValidate(sid)})
	})

	// 前端单页（登录页；一次 JS 完成权限跳转）。
	// 入口路径可配置（默认 /admin，环境变量 MOCKC2_ADMIN_PATH 可改为随机密径）。
	// index.html 资产是相对路径 ./assets/*，地址栏必须落在 <adminPath>/（带尾斜杠）。
	adminPath := strings.TrimRight(config.Get().AdminPath, "/")
	if adminPath == "" {
		adminPath = "/admin"
	}
	serveAdminIndex := func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		serveFile(w, filepath.Join(config.Get().FrontendDir, "index.html"), "text/html", "no-store")
	}
	serveAdminAsset := func(w http.ResponseWriter, r *http.Request, p map[string]string) {
		fname := p["fname"]
		if !adminAssetRe.MatchString(fname) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad name"})
			return
		}
		path := filepath.Join(config.Get().FrontendDir, "assets", fname)
		if !isFile(path) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		mt := "application/javascript"
		if len(fname) > 4 && fname[len(fname)-4:] == ".css" {
			mt = "text/css"
		}
		serveFile(w, path, mt, "no-store")
	}

	// 入口（无尾斜杠 → 308 到带斜杠，保证相对资产正确解析）
	rt.GET(adminPath, func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if !strings.HasSuffix(r.URL.Path, "/") {
			target := adminPath + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
			return
		}
		serveAdminIndex(w, r, nil)
	})
	rt.GET(adminPath+"/assets/{fname}", serveAdminAsset)
}
