package server

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("{}")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeText(w http.ResponseWriter, status int, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(s))
}

func readBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	b, _ := io.ReadAll(r.Body)
	return b
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	// 经内置 LB / 反代时取真实客户端 IP（多实例横向扩展）
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return xr
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func hostHeader(r *http.Request) string { return r.Host }

func queryStr(r *http.Request, name, def string) string {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	return v
}

func queryInt(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func queryFloatPtr(r *http.Request, name string) *float64 {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return nil
	}
	return &f
}

func nowString() string { return time.Now().Format("2006-01-02 15:04:05") }

func setNoStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func parseInt64(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

// serveFile 读取文件并写出（可选 contentType / cacheControl）。
func serveFile(w http.ResponseWriter, path, contentType, cacheControl string) {
	data, err := os.ReadFile(path)
	if err != nil {
		writeText(w, http.StatusNotFound, "404 Not Found")
		return
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if cacheControl != "" {
		w.Header().Set("Cache-Control", cacheControl)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
