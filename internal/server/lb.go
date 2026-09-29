package server

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
)

// roundRobinLB 内置轮询负载均衡（多实例/多 worker 横向扩展前置）。
//
// 用 httputil.ReverseProxy：自动加 X-Forwarded-For，且默认**保留原始 Host 头**，
// 因此后端 worker 的 roleFor(host) 分域、isC2Host 判定、门禁 cookie 都不受影响。
type roundRobinLB struct {
	proxies []*httputil.ReverseProxy
	n       uint64
}

// NewRoundRobinLB 构造；targets 形如 http://127.0.0.1:9101。
func NewRoundRobinLB(targets []string) http.Handler {
	proxies := make([]*httputil.ReverseProxy, 0, len(targets))
	for _, t := range targets {
		u, err := url.Parse(t)
		if err != nil || u.Host == "" {
			continue
		}
		p := httputil.NewSingleHostReverseProxy(u)
		p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"bad gateway","detail":"backend unavailable"}`))
		}
		proxies = append(proxies, p)
	}
	return &roundRobinLB{proxies: proxies}
}

func (l *roundRobinLB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(l.proxies) == 0 {
		http.Error(w, "no backends", http.StatusServiceUnavailable)
		return
	}
	i := atomic.AddUint64(&l.n, 1)
	l.proxies[int(i)%len(l.proxies)].ServeHTTP(w, r)
}
