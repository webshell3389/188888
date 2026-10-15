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
//
// 被动故障转移（本次新增）：后端连接失败时立刻改投其它后端。
// 动机 —— 发布重启窗口内 nginx 日志满是 `connect() failed (111: Connection refused)`，
// 纯轮询会把命中死后端的请求直接变成 502。反向代理在**连接失败**时尚未写出任何字节，
// 因此可以在 ErrorHandler 里安全改投；这样"双实例 + 内建 LB"即可做到发布零中断。
type roundRobinLB struct {
	proxies []*httputil.ReverseProxy
	targets []string
	n       uint64
}

// NewRoundRobinLB 构造；targets 形如 http://127.0.0.1:9101。
func NewRoundRobinLB(targets []string) http.Handler {
	l := &roundRobinLB{}
	for _, t := range targets {
		u, err := url.Parse(t)
		if err != nil || u.Host == "" {
			continue
		}
		p := httputil.NewSingleHostReverseProxy(u)
		dead := u.Host
		p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			// 改投其它后端；全部不可用才回 502。
			if l.retryOther(w, r, dead) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"bad gateway","detail":"backend unavailable"}`))
		}
		l.proxies = append(l.proxies, p)
		l.targets = append(l.targets, dead)
	}
	return l
}

// retryOther 把请求改投到除 dead 之外的其它后端，成功接管返回 true。
// 仅在连接失败（尚未写出响应）时调用。
func (l *roundRobinLB) retryOther(w http.ResponseWriter, r *http.Request, dead string) bool {
	for i, t := range l.targets {
		if t == dead {
			continue
		}
		l.proxies[i].ServeHTTP(w, r)
		return true
	}
	return false
}

func (l *roundRobinLB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if len(l.proxies) == 0 {
		http.Error(w, "no backends", http.StatusServiceUnavailable)
		return
	}
	i := atomic.AddUint64(&l.n, 1)
	l.proxies[int(i)%len(l.proxies)].ServeHTTP(w, r)
}
