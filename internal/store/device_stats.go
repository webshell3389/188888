package store

// 设备读模型（物化）：入库时增量维护 device_stats，读接口不再现场 GROUP BY 大表。
// 语义：apps_count / notes_count = **去重后的当前数量**（apps 表 / memos 表按设备计数），
// war_count = war 包累计条数；first/last/beacon 为时间画像。

func deviceStatEndpoint(ep string) bool {
	switch ep {
	case "beacon", "a", "u", "nb", "war":
		return true
	}
	return false
}

// UpdateDeviceStat 增量维护设备时间画像（first/last/beacon）与 war 计数。
// u/nb 的 apps/notes 计数不在这里累加 —— 由 SetDeviceAppsCount / SetDeviceNotesCount
// 按去重口径维护（每次 /u /nb 解析后重算该设备计数），避免多次回传越滚越大。
func UpdateDeviceStat(endpoint string, fields [][2]string, ts string, epoch float64, addCounts bool) {
	if !deviceStatEndpoint(endpoint) {
		return
	}
	db := GetDB()
	if db == nil {
		return
	}
	dev := ""
	for _, f := range fields {
		if f[0] == "uuid" || f[0] == "lhu" {
			if dev == "" {
				dev = f[1]
			}
		}
	}
	if dev == "" || dev == "unknown" {
		return
	}
	dWar := 0
	if addCounts && endpoint == "war" {
		dWar = 1
	}
	be := 0.0
	if endpoint == "beacon" {
		be = epoch
	}
	db.Exec("INSERT INTO device_stats(uuid,first_seen,last_seen,last_epoch,beacon_epoch,war_count,updated_ts)"+
		" VALUES(?,?,?,?,?,?,?)"+
		" ON DUPLICATE KEY UPDATE"+
		" first_seen=IF(first_seen='',VALUES(first_seen),LEAST(first_seen,VALUES(first_seen))),"+
		" last_seen=GREATEST(last_seen,VALUES(last_seen)),"+
		" last_epoch=GREATEST(last_epoch,VALUES(last_epoch)),"+
		" beacon_epoch=GREATEST(beacon_epoch,VALUES(beacon_epoch)),"+
		" war_count=war_count+VALUES(war_count),"+
		" updated_ts=VALUES(updated_ts)",
		dev, ts, ts, epoch, be, dWar, ts)
}

// SetDeviceAppsCount 重算该设备去重后的应用数（apps 表 COUNT）。
func SetDeviceAppsCount(uuid string) {
	if uuid == "" {
		return
	}
	db := GetDB()
	if db == nil {
		return
	}
	n := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM apps WHERE uuid=?", uuid); row != nil {
		n = int(toInt(row["n"]))
	}
	ts, _ := Now()
	db.Exec("INSERT INTO device_stats(uuid,apps_count,updated_ts) VALUES(?,?,?)"+
		" ON DUPLICATE KEY UPDATE apps_count=VALUES(apps_count), updated_ts=VALUES(updated_ts)", uuid, n, ts)
}

// SetDeviceNotesCount 重算该设备去重后的备忘录数（memos 表 COUNT）。
func SetDeviceNotesCount(uuid string) {
	if uuid == "" {
		return
	}
	db := GetDB()
	if db == nil {
		return
	}
	n := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM memos WHERE uuid=?", uuid); row != nil {
		n = int(toInt(row["n"]))
	}
	ts, _ := Now()
	db.Exec("INSERT INTO device_stats(uuid,notes_count,updated_ts) VALUES(?,?,?)"+
		" ON DUPLICATE KEY UPDATE notes_count=VALUES(notes_count), updated_ts=VALUES(updated_ts)", uuid, n, ts)
}

// BackfillDeviceStats 回填 device_stats（首次/版本升级）。
// 标记 device_stats_backfilled_v2；幂等（时间画像 GREATEST；计数按最新去重值覆盖）。
func BackfillDeviceStats() int {
	db := GetDB()
	if db == nil {
		return 0
	}
	if row := db.QueryOne("SELECT v FROM kv WHERE k='device_stats_backfilled_v2'"); row != nil && toStr(row["v"]) == "1" {
		return 0
	}
	ts, _ := Now()
	// ① 时间画像（beacon/a）
	db.Exec(`INSERT INTO device_stats(uuid,first_seen,last_seen,last_epoch,beacon_epoch,updated_ts)
        SELECT m.value, MIN(NULLIF(p.first_recv_ts,'')), MAX(p.recv_ts), MAX(p.recv_epoch),
          MAX(CASE WHEN p.endpoint='beacon' THEN p.recv_epoch END), ?
        FROM packet_meta m JOIN packets p ON p.id=m.packet_id
        WHERE m.field IN ('uuid','lhu') AND p.endpoint IN ('beacon','a') AND m.value<>''
        GROUP BY m.value
        ON DUPLICATE KEY UPDATE first_seen=IF(first_seen='',VALUES(first_seen),LEAST(first_seen,VALUES(first_seen))),
          last_seen=GREATEST(last_seen,VALUES(last_seen)),
          last_epoch=GREATEST(last_epoch,VALUES(last_epoch)),
          beacon_epoch=GREATEST(beacon_epoch,VALUES(beacon_epoch)), updated_ts=VALUES(updated_ts)`, ts)
	// ② war 计数（去重包数）
	db.Exec(`INSERT INTO device_stats(uuid,war_count,updated_ts)
        SELECT m.value, COUNT(DISTINCT p.id), ?
        FROM packet_meta m JOIN packets p ON p.id=m.packet_id
        WHERE p.endpoint='war' AND m.field IN ('lhu','uuid') AND m.value<>''
        GROUP BY m.value
        ON DUPLICATE KEY UPDATE war_count=VALUES(war_count), updated_ts=VALUES(updated_ts)`, ts)
	// ③ 备忘录去重计数
	db.Exec(`INSERT INTO device_stats(uuid,notes_count,updated_ts)
        SELECT uuid, COUNT(*), ? FROM memos WHERE uuid<>'' GROUP BY uuid
        ON DUPLICATE KEY UPDATE notes_count=VALUES(notes_count), updated_ts=VALUES(updated_ts)`, ts)
	// ④ 应用去重计数
	db.Exec(`INSERT INTO device_stats(uuid,apps_count,updated_ts)
        SELECT uuid, COUNT(*), ? FROM apps WHERE uuid<>'' GROUP BY uuid
        ON DUPLICATE KEY UPDATE apps_count=VALUES(apps_count), updated_ts=VALUES(updated_ts)`, ts)
	db.Exec("INSERT INTO kv(k,v,updated_ts) VALUES('device_stats_backfilled_v2','1',?)"+
		" ON DUPLICATE KEY UPDATE v=VALUES(v), updated_ts=VALUES(updated_ts)", ts)
	n := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM device_stats"); row != nil {
		n = int(toInt(row["n"]))
	}
	return n
}
