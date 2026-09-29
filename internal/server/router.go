// Package server HTTP 服务层：路由装配、中间件、全部 handler。
// 与 Python 版 c2mock/routers/* + main.py + middleware/* 行为对齐。
package server

import (
	"net/http"
	"strings"
)

// HandlerFunc 带路径参数的 handler。
type HandlerFunc func(w http.ResponseWriter, r *http.Request, params map[string]string)

type compiledRoute struct {
	methods  map[string]bool
	segs     []string
	catchAll bool
	handler  HandlerFunc
}

// Router 按注册顺序匹配的路由器（对齐 FastAPI 注册顺序语义）。
type Router struct {
	routes []*compiledRoute
}

// NewRouter 构造。
func NewRouter() *Router { return &Router{} }

// Add 注册一条路由。pattern 支持 {name} 单段参数与 {name:path} 尾部通配。
func (rt *Router) Add(methods []string, pattern string, h HandlerFunc) {
	cr := &compiledRoute{handler: h}
	if len(methods) > 0 {
		cr.methods = map[string]bool{}
		for _, m := range methods {
			cr.methods[strings.ToUpper(m)] = true
		}
	}
	cr.segs = strings.Split(pattern, "/")
	if n := len(cr.segs); n > 0 {
		last := cr.segs[n-1]
		if strings.HasPrefix(last, "{") && strings.HasSuffix(last, ":path}") {
			cr.catchAll = true
		}
	}
	rt.routes = append(rt.routes, cr)
}

// GET/POST 便捷注册。
func (rt *Router) GET(pattern string, h HandlerFunc)  { rt.Add([]string{"GET"}, pattern, h) }
func (rt *Router) POST(pattern string, h HandlerFunc) { rt.Add([]string{"POST"}, pattern, h) }

// ServeHTTP 顺序匹配。
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
	}
	segs := strings.Split(path, "/")
	for _, cr := range rt.routes {
		if cr.methods != nil && !cr.methods[r.Method] {
			continue
		}
		if cr.catchAll {
			// /{p:path} —— 捕获去掉前导斜杠的完整路径
			p := strings.TrimPrefix(path, "/")
			cr.handler(w, r, map[string]string{"p": p})
			return
		}
		if params, ok := matchSegs(cr.segs, segs); ok {
			cr.handler(w, r, params)
			return
		}
	}
	http.Error(w, "404 Not Found", http.StatusNotFound)
}

func matchSegs(pattern, path []string) (map[string]string, bool) {
	if len(pattern) != len(path) {
		return nil, false
	}
	params := map[string]string{}
	for i, ps := range pattern {
		if strings.HasPrefix(ps, "{") && strings.HasSuffix(ps, "}") {
			params[ps[1:len(ps)-1]] = path[i]
			continue
		}
		if ps != path[i] {
			return nil, false
		}
	}
	return params, true
}
