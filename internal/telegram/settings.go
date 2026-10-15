package telegram

import (
	"encoding/json"
	"strconv"
	"strings"

	"mockc2/internal/store"
)

const cfgKey = "telegram_cfg"

// loadRuntime 读共享 kv 覆盖；无覆盖时回落文件/env 配置。
func loadRuntime(base Config) Config {
	rt := base
	if db := store.GetDB(); db != nil {
		if row := db.QueryOne("SELECT v FROM kv WHERE k=?", cfgKey); row != nil {
			var ov Config
			if json.Unmarshal([]byte(store.ToStr(row["v"])), &ov) == nil {
				rt = ov
			}
		}
	}
	normalize(&rt)
	return rt
}

// saveRuntime 写共享 kv（多实例一致）。
func saveRuntime(rt Config) error {
	db := store.GetDB()
	if db == nil {
		return nil
	}
	normalize(&rt)
	b, _ := json.Marshal(rt)
	ts, _ := store.Now()
	db.Exec("INSERT INTO kv(k, v, updated_ts) VALUES(?,?,?)"+
		" ON DUPLICATE KEY UPDATE v=VALUES(v), updated_ts=VALUES(updated_ts)", cfgKey, string(b), ts)
	return nil
}

// runtimeState 线程安全的运行时快照（含解析后的白名单 map）。
type runtimeState struct {
	Config
	allowed map[int64]bool
	events  map[string]bool
}

func compile(c Config) runtimeState {
	st := runtimeState{Config: c, allowed: map[int64]bool{}, events: map[string]bool{}}
	for _, id := range c.ChatIDs {
		if n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err == nil {
			st.allowed[n] = true
		}
	}
	for _, e := range c.Events {
		st.events[e] = true
	}
	return st
}
