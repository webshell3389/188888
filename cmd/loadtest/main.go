// Command loadtest 简易并发压测（验证服务在高并发下的稳定性与吞吐）。
//
// 用法：
//
//	go run ./cmd/loadtest -base http://127.0.0.1:9100 -c 100 -n 20000
//	go run ./cmd/loadtest -base http://127.0.0.1:9100 -c 50 -n 5000 -path /boot.html
//
// 说明：-path 省略时按混合场景（交付静态 / C2 回传 / 只读查询）轮转。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type scenario struct {
	method string
	path   string
	body   []byte
	ctype  string
}

var scenarios = []scenario{
	{"GET", "/boot.html?c=1", nil, ""},
	{"GET", "/B10/rce_loader.js", nil, ""},
	{"POST", "/beacon", []byte(`{"uuid":"loadtest","status":"idle"}`), "application/json"},
	{"POST", "/result", []byte(`{"command_id":"lt","status":"ok"}`), "application/json"},
	{"GET", "/__q/stats", nil, ""},
	{"GET", "/panel/api/overview", nil, ""},
	{"GET", "/__dash/api", nil, ""},
	{"GET", "/log.html?id=1&text=load", nil, ""},
}

func main() {
	base := flag.String("base", "http://127.0.0.1:9100", "服务基址")
	conc := flag.Int("c", 100, "并发数")
	total := flag.Int("n", 20000, "总请求数")
	path := flag.String("path", "", "固定路径（省略=混合场景）")
	flag.Parse()

	tr := &http.Transport{
		MaxIdleConns:        *conc * 2,
		MaxIdleConnsPerHost: *conc * 2,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second}

	// 可选：登录取 cookie（只读管理面需要）
	cookie := login(client, *base)

	var (
		ok200    int64
		statuses sync.Map // int -> *int64
		latMu    sync.Mutex
		lats     = make([]float64, 0, *total)
		wg       sync.WaitGroup
		start    = time.Now()
	)

	perWorker := *total / *conc
	if perWorker == 0 {
		perWorker = 1
	}

	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				sc := scenarios[(wid+i)%len(scenarios)]
				if *path != "" {
					sc = scenario{"GET", *path, nil, ""}
				}
				req, err := http.NewRequest(sc.method, *base+sc.path, bytes.NewReader(sc.body))
				if err != nil {
					continue
				}
				if sc.ctype != "" {
					req.Header.Set("Content-Type", sc.ctype)
				}
				if cookie != "" {
					req.Header.Set("Cookie", cookie)
				}
				t0 := time.Now()
				resp, err := client.Do(req)
				dt := time.Since(t0).Seconds() * 1000
				code := 0
				if err == nil {
					code = resp.StatusCode
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				if code == 200 {
					atomic.AddInt64(&ok200, 1)
				}
				ctr, _ := statuses.LoadOrStore(code, new(int64))
				atomic.AddInt64(ctr.(*int64), 1)
				latMu.Lock()
				lats = append(lats, dt)
				latMu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)

	sort.Float64s(lats)
	pct := func(p float64) float64 {
		if len(lats) == 0 {
			return 0
		}
		idx := int(math.Ceil(p/100*float64(len(lats)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(lats) {
			idx = len(lats) - 1
		}
		return lats[idx]
	}

	fmt.Printf("并发=%d 总请求=%d 用时=%.2fs\n", *conc, len(lats), elapsed.Seconds())
	fmt.Printf("吞吐=%.0f req/s   成功(200)=%d\n", float64(len(lats))/elapsed.Seconds(), ok200)
	fmt.Print("状态码分布: ")
	statuses.Range(func(k, v any) bool {
		fmt.Printf("%v=%d  ", k, atomic.LoadInt64(v.(*int64)))
		return true
	})
	fmt.Printf("\n延迟(ms): p50=%.1f p90=%.1f p99=%.1f max=%.1f\n",
		pct(50), pct(90), pct(99), pct(100))
}

func login(client *http.Client, base string) string {
	req, _ := http.NewRequest("POST", base+"/__auth/login",
		bytes.NewReader([]byte(`{"password":"lab888"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	for _, c := range resp.Cookies() {
		if c.Name == "c2mock_admin" {
			return c.Name + "=" + c.Value
		}
	}
	return ""
}
