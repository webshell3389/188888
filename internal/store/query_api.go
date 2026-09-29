package store

// 只读查询服务 —— 所有函数只读走 DB 唯一连接。
// 与 ingest.go（写侧）对偶：DB_SCHEMA.md 常用查询模板的函数化。

const (
	// DefaultLimit 默认 limit。
	DefaultLimit = 200
	// MaxLimit limit 上限。
	MaxLimit = 2000
)

func clamp(n, def, cap int) int {
	if n <= 0 {
		return def
	}
	if n > cap {
		return cap
	}
	return n
}

// ListDevicesTable 设备台账全量（最近上线在前）。
func ListDevicesTable() []map[string]any {
	return GetDB().Query(
		"SELECT id, first_seen, last_seen, ip, host, ua," +
			" device_version, domain FROM devices ORDER BY last_seen DESC")
}

// DeviceTimeline 某 client（IP）在 access_log 的全生命周期流水（新 -> 旧）。
func DeviceTimeline(ip string, limit int) []map[string]any {
	limit = clamp(limit, DefaultLimit, MaxLimit)
	return GetDB().Query(
		"SELECT id, ts, role, method, path, query, host, client, ua, status, cost_ms"+
			" FROM access_log WHERE client=? ORDER BY ts_epoch DESC, id DESC LIMIT ?",
		ip, limit)
}

// PacketsByEndpoint 按端点取回传包索引（新 -> 旧）；sinceEpoch 只取其后（含）。
func PacketsByEndpoint(endpoint string, limit int, sinceEpoch *float64) []map[string]any {
	limit = clamp(limit, DefaultLimit, MaxLimit)
	sql := "SELECT id, endpoint, recv_ts, recv_epoch, file_path, size, sha256, content_kind" +
		" FROM packets WHERE endpoint=?"
	args := []any{endpoint}
	if sinceEpoch != nil {
		sql += " AND recv_epoch>=?"
		args = append(args, *sinceEpoch)
	}
	sql += " ORDER BY recv_epoch DESC, id DESC LIMIT ?"
	args = append(args, limit)
	return GetDB().Query(sql, args...)
}

// PacketDetail 单包详情：packets 主行 + packet_meta 字段键值对聚合。
func PacketDetail(packetID int64) map[string]any {
	row := GetDB().QueryOne(
		"SELECT id, endpoint, recv_ts, recv_epoch, file_path, size, sha256, content_kind"+
			" FROM packets WHERE id=?", packetID)
	if row == nil {
		return nil
	}
	metas := map[string]any{}
	for _, m := range GetDB().Query(
		"SELECT field, value FROM packet_meta WHERE packet_id=? ORDER BY id", packetID) {
		metas[toStr(m["field"])] = m["value"]
	}
	row["meta"] = metas
	return row
}

// SearchMeta packet_meta 检索：按 field 精确 + 可选 value 模糊。
func SearchMeta(field, valueLike string, limit int) []map[string]any {
	limit = clamp(limit, 50, MaxLimit)
	sql := "SELECT m.id, m.packet_id, m.field, m.value, p.endpoint, p.recv_ts" +
		" FROM packet_meta m JOIN packets p ON p.id=m.packet_id WHERE m.field=?"
	args := []any{field}
	if valueLike != "" {
		sql += " AND m.value LIKE ?"
		args = append(args, "%"+valueLike+"%")
	}
	sql += " ORDER BY m.id DESC LIMIT ?"
	args = append(args, limit)
	return GetDB().Query(sql, args...)
}

// CommandRecent 指令 audit 流水（armed -> dispatched -> cleared），最新在前。
func CommandRecent(n int) []map[string]any {
	n = clamp(n, 20, MaxLimit)
	return GetDB().Query(
		"SELECT id, command_id, cmd_type, params_json, armed_ts, once, state,"+
			" dispatched_ts, client_ip FROM commands ORDER BY id DESC LIMIT ?", n)
}

// DBStats 七表计数汇总 + 回传包规模最快概览。
func DBStats() map[string]any {
	db := GetDB()
	tables := []string{"devices", "access_log", "packets", "packet_meta",
		"probe_log", "commands", "schema_migrations"}
	counts := map[string]any{}
	for _, t := range tables {
		row := db.QueryOne("SELECT COUNT(*) AS n FROM " + t)
		var n any = 0
		if row != nil {
			n = row["n"]
		}
		counts[t] = n
	}
	byEp := db.Query(
		"SELECT endpoint, COUNT(*) AS packets, COALESCE(SUM(size),0) AS bytes" +
			" FROM packets GROUP BY endpoint ORDER BY packets DESC")
	return map[string]any{"counts": counts, "packets_by_endpoint": byEp}
}
