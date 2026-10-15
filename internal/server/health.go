package server

import (
	"net/http"
	"runtime"
	"time"

	"mockc2/internal/store"
)

// BuildVersion 构建版本（可由 ldflags -X 注入：-X mockc2/internal/server.BuildVersion=...）。
var BuildVersion = "dev"

var svcStart = time.Now()

// registerHealth 暴露探针端点（无敏感信息，供 nginx / systemd / 监控使用）。
//   /__health   DB ping + 连接池 + goroutine + 在途请求；DB 不可用返回 503
//   /__version  构建版本 / Go 版本 / 已应用迁移版本
func registerHealth(rt *Router) {
	rt.GET("/__health", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		dbOK := store.DBHealthy()
		code := http.StatusOK
		status := "ok"
		if !dbOK {
			code = http.StatusServiceUnavailable
			status = "degraded"
		}
		writeJSON(w, code, map[string]any{
			"status":     status,
			"db":         dbOK,
			"uptime_s":   int64(time.Since(svcStart).Seconds()),
			"goroutines": runtime.NumGoroutine(),
			"inflight":   inflightReqs.Load(),
			"db_pool":    store.DBPoolStats(),
			"schema":     store.SchemaVersion(),
		})
	})
	rt.GET("/__version", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		writeJSON(w, http.StatusOK, map[string]any{
			"version": BuildVersion,
			"go":      runtime.Version(),
			"schema":  store.SchemaVersion(),
		})
	})
}
