package server

import (
	"log"
	"net/http"
	"runtime/debug"
)

// 过载保护：在途请求超过上限时快速 503，避免 goroutine/连接无界堆积（背压）。
// 与 /__metrics 的 inflight 共用计数器。
const maxInflight = 5000

// overloadMiddleware 最外层之一：过载快速失败（不进入业务/DB）。
func overloadMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inflightReqs.Load() >= maxInflight {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"busy"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recoverMiddleware 最外层：任何 handler panic 记栈并回 500，进程不崩。
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("[panic] %v\n%s", p, debug.Stack())
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
