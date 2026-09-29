package store

import (
	"encoding/json"
	"sync"
)

// BeaconControl beacon 命令驱动服务（DB 共享版）—— 多实例一致。
//
// 默默 noop 模式即足够全链路存活；如需进入命令分发循环，可通过 /__control
// 修改运行时 beacon 响应形态。运行时可变态存于共享库 kv 表（k='beacon_shape'），
// 任意实例修改，全部实例生效。
type BeaconControl struct {
	mu  sync.Mutex
	tpl map[string]any
}

// NewBeaconControl 构造。
func NewBeaconControl(noopTemplate map[string]any) *BeaconControl {
	return &BeaconControl{tpl: copyMap(noopTemplate)}
}

// Template 当前应答形态（先读共享 KV，缺省回落默认 noop）。
func (b *BeaconControl) Template() map[string]any {
	if db := GetDB(); db != nil {
		if row := db.QueryOne("SELECT v FROM kv WHERE k='beacon_shape'"); row != nil {
			var m map[string]any
			if json.Unmarshal([]byte(toStr(row["v"])), &m) == nil && len(m) > 0 {
				return m
			}
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return copyMap(b.tpl)
}

// SetResponse 修改运行时应答形态（写共享 KV）。
func (b *BeaconControl) SetResponse(shape map[string]any) {
	if db := GetDB(); db != nil {
		v, _ := json.Marshal(shape)
		ts, _ := Now()
		db.Exec("INSERT INTO kv(k, v, updated_ts) VALUES('beacon_shape',?,?)"+
			" ON DUPLICATE KEY UPDATE v=VALUES(v), updated_ts=VALUES(updated_ts)", string(v), ts)
	}
}

// ResetNoop 恢复默认 noop（清共享 KV 覆盖）。
func (b *BeaconControl) ResetNoop() {
	if db := GetDB(); db != nil {
		db.Exec("DELETE FROM kv WHERE k='beacon_shape'")
	}
}

// EnsureSeeded 首次启动写入默认形态（幂等；多实例安全）。
func (b *BeaconControl) EnsureSeeded() {
	if db := GetDB(); db == nil {
		return
	} else if row := db.QueryOne("SELECT v FROM kv WHERE k='beacon_shape'"); row == nil {
		v, _ := json.Marshal(b.tpl)
		ts, _ := Now()
		db.Exec("INSERT IGNORE INTO kv(k, v, updated_ts) VALUES('beacon_shape',?,?)", string(v), ts)
	}
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

var (
	ctlMu     sync.RWMutex // 读多写少：GetControl 在 /beacon 热路径被调用，读用 RLock
	globalCtl *BeaconControl
)

// InitControl 初始化全局控制。
func InitControl(noopTemplate map[string]any) {
	ctlMu.Lock()
	defer ctlMu.Unlock()
	globalCtl = NewBeaconControl(noopTemplate)
}

// GetControl 返回全局控制。
func GetControl() *BeaconControl {
	ctlMu.RLock()
	defer ctlMu.RUnlock()
	return globalCtl
}
