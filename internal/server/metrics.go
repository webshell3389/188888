package server

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"mockc2/internal/store"
)

// 运行指标（INC-2026-0929-001 §6.7）：GET /__metrics —— Prometheus 文本格式。
//
// 关键指标（对照报告验收项）：
//   - mockc2_in_flight_requests     峰值在途请求（有硬上限且可观测）
//   - mockc2_expand_fastfail_total  背压快速失败次数（§6.2）
//   - mockc2_db_queries_total       DB 查询计数（N+1 回归检测）
//   - mockc2_db_wait_count          连接池等待（容量告警）
//   - mockc2_audit_dropped_total    审计有界队列丢弃（§6.6/§6.8）
//   - mockc2_goroutines / uptime    运行时健康

var (
	metricsUp    = time.Now()
	inflightReqs atomic.Int64
	reqs2xx      atomic.Int64
	reqs3xx      atomic.Int64
	reqs4xx      atomic.Int64
	reqs5xx      atomic.Int64
)

// metricsMiddleware 最外层包裹：在途请求 + 状态码分布（全量计入）。
func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inflightReqs.Add(1)
		defer inflightReqs.Add(-1)
		sr := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(sr, r)
		switch {
		case sr.status >= 500:
			reqs5xx.Add(1)
		case sr.status >= 400:
			reqs4xx.Add(1)
		case sr.status >= 300:
			reqs3xx.Add(1)
		default:
			reqs2xx.Add(1)
		}
	})
}

// registerMetrics 挂载 /__metrics（在受守卫前缀内，见 auth.go protectedPrefixes）。
func registerMetrics(rt *Router) {
	rt.GET("/__metrics", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(renderMetrics()))
	})
}

// renderMetrics 汇总各层计数为 Prometheus 文本。
func renderMetrics() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	line("# HELP mockc2_uptime_seconds 进程运行时长")
	line("# TYPE mockc2_uptime_seconds gauge")
	line("mockc2_uptime_seconds %d", int64(time.Since(metricsUp).Seconds()))

	line("# HELP mockc2_in_flight_requests 当前在途请求数")
	line("# TYPE mockc2_in_flight_requests gauge")
	line("mockc2_in_flight_requests %d", inflightReqs.Load())

	line("# HELP mockc2_requests_total HTTP 请求计数（按状态类）")
	line("# TYPE mockc2_requests_total counter")
	line("mockc2_requests_total{class=\"2xx\"} %d", reqs2xx.Load())
	line("mockc2_requests_total{class=\"3xx\"} %d", reqs3xx.Load())
	line("mockc2_requests_total{class=\"4xx\"} %d", reqs4xx.Load())
	line("mockc2_requests_total{class=\"5xx\"} %d", reqs5xx.Load())

	line("# HELP mockc2_audit_dropped_total 审计队列满丢弃条数（有界内存保护）")
	line("# TYPE mockc2_audit_dropped_total counter")
	line("mockc2_audit_dropped_total %d", AuditDroppedTotal())

	line("# HELP mockc2_goroutines 运行中 goroutine 数")
	line("# TYPE mockc2_goroutines gauge")
	line("mockc2_goroutines %d", runtime.NumGoroutine())

	line("# HELP mockc2_gc_pause_total_ns GC 累计暂停（纳秒）")
	line("# TYPE mockc2_gc_pause_total_ns counter")
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	line("mockc2_gc_pause_total_ns %d", ms.PauseTotalNs)
	line("mockc2_heap_alloc_bytes %d", ms.HeapAlloc)

	// 数据层计数（store.MetricsSnapshot）
	snap := store.MetricsSnapshot()
	type metricDef struct{ name, typ, help string }
	for _, def := range []metricDef{
		{"db_queries_total", "counter", "DB 查询语句计数"},
		{"db_execs_total", "counter", "DB 写入语句计数"},
		{"db_errors_total", "counter", "DB 执行错误计数"},
		{"db_open_connections", "gauge", "连接池打开连接数"},
		{"db_in_use", "gauge", "连接池占用连接数"},
		{"db_idle", "gauge", "连接池空闲连接数"},
		{"db_wait_count", "counter", "连接池等待次数"},
		{"db_wait_duration_ms", "counter", "连接池等待累计毫秒"},
		{"expand_fastfail_total", "counter", "expandCands 背压快速失败次数"},
		{"idmap_reload_total", "counter", "设备身份映射缓存回源次数"},
	} {
		if v, ok := snap[def.name]; ok {
			line("# HELP mockc2_%s %s", def.name, def.help)
			line("# TYPE mockc2_%s %s", def.name, def.typ)
			line("mockc2_%s %d", def.name, v)
		}
	}
	return b.String()
}
