package store

import (
	"context"
	"time"
)

// DBHealthy 数据库健康检查（2s 超时）。供 /__health 使用。
func DBHealthy() bool {
	d := GetDB()
	if d == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return d.conn.PingContext(ctx) == nil
}

// Close 优雅停机：关闭 DB 连接池与留存句柄（幂等，供 main 信号处理调用）。
func Close() {
	if globalDB != nil {
		_ = globalDB.Close()
	}
	if globalStore != nil {
		globalStore.CloseHandles()
	}
}

// SchemaVersion 已应用的最高迁移版本。
func SchemaVersion() int {
	d := GetDB()
	if d == nil {
		return 0
	}
	if row := d.QueryOne("SELECT COALESCE(MAX(version),0) v FROM schema_migrations"); row != nil {
		return int(toInt(row["v"]))
	}
	return 0
}

// DBPoolStats 连接池快照。
func DBPoolStats() map[string]any {
	d := GetDB()
	if d == nil {
		return map[string]any{}
	}
	st := d.Stats()
	return map[string]any{
		"open": st.OpenConnections, "in_use": st.InUse,
		"idle": st.Idle, "wait_count": st.WaitCount,
	}
}

// EnsureCriticalIndexes 幂等确保关键索引存在（新库 / 历史库都安全）。
// 现有手工加过索引的库不会重复 ALTER。
func EnsureCriticalIndexes() []string {
	d := GetDB()
	if d == nil {
		return nil
	}
	var added []string
	ensure := func(table, name, col string) {
		row := d.QueryOne("SELECT COUNT(*) n FROM information_schema.statistics"+
			" WHERE table_schema=DATABASE() AND table_name=? AND index_name=?", table, name)
		if row != nil && toInt(row["n"]) == 0 {
			d.Exec("ALTER TABLE " + table + " ADD KEY " + name + " (" + col + ")")
			added = append(added, table+"."+name)
		}
	}
	// access_log 以 ts_epoch 打头的索引：RecentAccess 的 ORDER BY / 保留删除都靠它，缺则全表扫。
	ensure("access_log", "ix_access_epoch", "ts_epoch")
	// probe_log 以 ts 打头：保留删除。
	ensure("probe_log", "ix_probe_ts", "ts")
	return added
}
