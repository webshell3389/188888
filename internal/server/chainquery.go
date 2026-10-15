package server

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// chainquery 多链余额查询（tools/chainquery，独立 Python 服务，默认 127.0.0.1:8787）。
// 后端只做代理：POST /panel/api/words/balance → chainquery /api/query；
//                 GET  /panel/api/words/balance?job= → /api/job。
// 地址可用 env MOCKC2_CHAINQUERY 覆盖。

func chainqueryBase() string {
	if v := strings.TrimSpace(os.Getenv("MOCKC2_CHAINQUERY")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://127.0.0.1:8787"
}

func registerChainQuery(rt *Router) {
	client := &http.Client{Timeout: 90 * time.Second}

	proxy := func(w http.ResponseWriter, method, target string, body []byte) {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, target, rd)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": "chainquery 不可达（未启动？）: " + err.Error(), "base": chainqueryBase()})
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(b)
	}

	// 创建查询任务（body 透传给 chainquery /api/query：{mnemonics/addresses/chains/tokens/prices}）
	rt.POST("/panel/api/words/balance", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		proxy(w, "POST", chainqueryBase()+"/api/query", readBody(r))
	})
	// 任务进度/结果
	rt.GET("/panel/api/words/balance", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		job := url.QueryEscape(r.URL.Query().Get("job"))
		proxy(w, "GET", chainqueryBase()+"/api/job?id="+job, nil)
	})
	// 支持链列表
	rt.GET("/panel/api/words/chains", func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		proxy(w, "GET", chainqueryBase()+"/api/chains", nil)
	})
}
