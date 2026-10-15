package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
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

// ── 传输压缩：流式 gzip（不整包缓冲）──
//
// 旧实现把整个响应体 buf 进内存再判断是否 gzip：大 JS payload（pe/sbx/rce）每次请求
// 都要在中间件层整包驻留一份 + 重新压缩，并发高时内存与延迟双涨。
// 改为：前 2048 字节缓冲用于判阈值，超过即切 gzip.NewWriter 流式直写；小响应(<2KB)
// 与不可压缩/下载内容直通不压。File read 由 OS page cache 承接，不再每请求重复整包拷贝。

const gzipThreshold = 2048

// compressibleContentType 判是否值得 gzip：文本/JSON/JS 压；媒体/二进制/下载直通。
func compressibleContentType(ct string) bool {
	if ct == "" {
		return true
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	switch {
	case strings.HasPrefix(ct, "image/"),
		strings.HasPrefix(ct, "video/"),
		strings.HasPrefix(ct, "audio/"):
		return false
	case ct == "application/octet-stream",
		ct == "application/zip",
		ct == "application/gzip",
		ct == "application/x-gzip",
		ct == "application/x-tar":
		return false
	}
	return true
}

type gzipResponseWriter struct {
	http.ResponseWriter
	buf         bytes.Buffer
	gz          *gzip.Writer
	status      int
	wroteHeader bool
	compress    bool
}

func (g *gzipResponseWriter) Header() http.Header { return g.ResponseWriter.Header() }

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	g.status = code
	ct := g.ResponseWriter.Header().Get("Content-Type")
	cd := g.ResponseWriter.Header().Get("Content-Disposition")
	if code == http.StatusNoContent || code == http.StatusNotModified ||
		strings.Contains(cd, "attachment") || !compressibleContentType(ct) {
		g.compress = false
		g.ResponseWriter.WriteHeader(code)
		return
	}
	g.compress = true // 先记状态；超阈值才真正写头
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if !g.compress {
		return g.ResponseWriter.Write(b)
	}
	if g.gz == nil {
		if g.buf.Len()+len(b) < gzipThreshold {
			return g.buf.Write(b)
		}
		h := g.ResponseWriter.Header()
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		h.Del("Content-Length")
		g.ResponseWriter.WriteHeader(g.status)
		g.gz = gzip.NewWriter(g.ResponseWriter)
		if g.buf.Len() > 0 {
			if _, err := g.gz.Write(g.buf.Bytes()); err != nil {
				return 0, err
			}
			g.buf.Reset()
		}
	}
	return g.gz.Write(b)
}

func (g *gzipResponseWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// close 冲刷：gzip 流关闭；小响应补写未压缩头+体。
func (g *gzipResponseWriter) close() error {
	if g.gz != nil {
		return g.gz.Close()
	}
	if g.compress {
		g.ResponseWriter.WriteHeader(g.status)
		if g.buf.Len() > 0 {
			_, err := g.ResponseWriter.Write(g.buf.Bytes())
			return err
		}
	}
	return nil
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer func() { _ = gw.close() }()
		next.ServeHTTP(gw, r)
	})
}
