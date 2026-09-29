package store

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
func ListDevices() []map[string]any {
	db := GetDB()
	if db == nil {
		return []map[string]any{}
	}
	rows := db.Query(`
        SELECT m.value AS uuid,
               MIN(p.recv_ts) AS first_seen,
               MAX(p.recv_ts) AS last_seen,
               MAX(CASE WHEN p.endpoint='beacon' THEN p.recv_epoch END) AS beacon_epoch
        FROM packet_meta m JOIN packets p ON p.id = m.packet_id
        WHERE m.field IN ('uuid','lhu') AND p.endpoint IN ('beacon','a') AND m.value<>''
        GROUP BY m.value ORDER BY last_seen DESC, m.value ASC`)
	if len(rows) == 0 {
		return []map[string]any{}
	}

	// 批量聚合（各一次查询）
	warFiles, warCounts := devicesWarAgg()
	notes := devicesNotesCountAgg()
	apps := devicesAppsCountAgg()
	wallets := devicesWalletsAgg()
	profiles := devicesProfileAgg()

	// 就近 IP 推断（devices 台账一次读取）
	var timeline []visitPoint
	for _, v := range db.Query("SELECT last_seen, ip FROM devices ORDER BY last_seen") {
		ls, ip := toStr(v["last_seen"]), toStr(v["ip"])
		if tsRe.MatchString(ls) && ip != "" {
			timeline = append(timeline, visitPoint{ls, ip})
		}
	}

	nowT := nowEpoch()
	progress := ClientStageProgress()
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		uuid := toStr(r["uuid"])
		beaconEpoch := toFloat(r["beacon_epoch"])
		online := beaconEpoch != 0 && (nowT-beaconEpoch) <= 1800
		// ip 优先取 device_profiles 权威档案；缺失才回退 nearIP 就近推断
		ip := toStr(profiles[uuid]["ip"])
		if ip == "" {
			ip = nearIPAt(timeline, toStr(r["last_seen"]))
		}
		pct := progress[ip]
		d := map[string]any{
			"uuid":        uuid,
			"ip":          ip,
			"first_seen":  r["first_seen"],
			"last_seen":   r["last_seen"],
			"online":      online,
			"war_count":   warCounts[uuid],
			"war_files":   warFiles[uuid],
			"wallets":     wallets[uuid],
			"notes_count": notes[uuid],
			"apps_count":  apps[uuid],
			"progress":    pct,
			"stage":       StageNameForPercent(pct),
		}
		for k, v := range profiles[uuid] {
			d[k] = v
		}
		out = append(out, d)
	}
	return out
}

// visitPoint 台账时间线点（last_seen, ip）。
type visitPoint struct{ ts, ip string }

// nearIPAt 在台账时间线上找与目标时间最近（±30min 内）的 IP；无则取最新一条的 IP。
func nearIPAt(timeline []visitPoint, target string) string {
	if len(timeline) == 0 {
		return ""
	}
	t, err := time.ParseInLocation(tsFmt, target, time.Local)
	if err != nil {
		return timeline[len(timeline)-1].ip
	}
	best, bestd := timeline[len(timeline)-1].ip, math.MaxFloat64
	for _, e := range timeline {
		lt, err := time.ParseInLocation(tsFmt, e.ts, time.Local)
		if err != nil {
			continue
		}
		if d := math.Abs(lt.Sub(t).Seconds()); d < bestd {
			best, bestd = e.ip, d
		}
	}
	if bestd <= 1800 {
		return best
	}
	return timeline[len(timeline)-1].ip
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

// devicesWalletsAgg 批量聚合钱包命中：仅来自 war 端点落库的 wallet_seen 快照
// （war 只做钱包包名解析，keychain.wallets 桶快照），不再读 u 文件 / apps 表补推断。
func devicesWalletsAgg() map[string][]map[string]any {
	db := GetDB()
	agg := map[string]map[string]any{}
	for _, r := range db.Query(
		"SELECT device_uuid, rule_name, MAX(count) AS cnt FROM wallet_seen"+
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
	// 转 []map 并按 count 降序
	out := map[string][]map[string]any{}
	for uuid, m := range agg {
		list := make([]map[string]any, 0, len(m))
		for _, v := range m {
			if wm, ok := v.(map[string]any); ok {
				list = append(list, wm)
			}
		}
		sort.Slice(list, func(i, j int) bool { return toInt(list[i]["count"]) > toInt(list[j]["count"]) })
		out[uuid] = list
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

// walletsMeta 单设备钱包命中（仅 war 端点 wallet_seen 快照；war 只做钱包包名解析，不再读 u 文件补推断）。
func walletsMeta(lhu string) []map[string]any {
	db := GetDB()
	out := map[string]map[string]any{}
	for _, r := range db.Query(
		"SELECT rule_name, MAX(count) AS cnt FROM wallet_seen"+
			" WHERE device_uuid=? GROUP BY rule_name", lhu) {
		rn := toStr(r["rule_name"])
		out[rn] = map[string]any{"rule": rn, "display": Display(rn), "count": toInt(r["cnt"])}
	}
	list := make([]map[string]any, 0, len(out))
	for _, v := range out {
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
func NoteRows(kw string, page, per int) map[string]any {
	db := GetDB()
	cap := capRoot()
	pids := db.Query(`
        SELECT p.id, p.recv_ts, p.file_path FROM packets p
        WHERE p.endpoint='nb' ORDER BY p.recv_epoch DESC LIMIT 200`)
	rows := []map[string]any{}
	for _, r := range pids {
		b, err := os.ReadFile(filepath.Join(cap, filepath.FromSlash(toStr(r["file_path"]))))
		if err != nil {
			continue
		}
		var o map[string]any
		if json.Unmarshal(b, &o) != nil {
			continue
		}
		lhu := toStr(o["lhu"])
		for _, nv := range asAnySlice(o["list"]) {
			n, _ := nv.(map[string]any)
			title := toStr(n["title"])
			if title == "" {
				title = "(空标题)"
			}
			row := map[string]any{"id": n["id"], "title": title,
				"snippet": toStr(n["snippet"]), "mod": fmtMod(n["mod"]),
				"ts": r["recv_ts"], "lhu": lhu, "packet_id": r["id"]}
			if kw != "" {
				k := strings.ToLower(kw)
				if !strings.Contains(strings.ToLower(toStr(row["title"])), k) &&
					!strings.Contains(strings.ToLower(toStr(row["snippet"])), k) &&
					!strings.Contains(strings.ToLower(lhu), k) {
					continue
				}
			}
			rows = append(rows, row)
		}
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
	onlineRow := db.QueryOne(
		"SELECT COUNT(*) AS n FROM packet_meta m JOIN packets p ON p.id=m.packet_id" +
			" WHERE m.field='uuid' AND p.endpoint='beacon'" +
			"   AND p.recv_epoch >= UNIX_TIMESTAMP()-1800")
	online := 0
	if onlineRow != nil {
		online = int(toInt(onlineRow["n"]))
	}
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
		"visits":        visits,
		"visits_today":  visitsToday,
		"new_devices":   newDevices,
		"devices":       len(devs),
		"online":        online,
		"notes":         toInt(notesRow["n"]),
		"war":           toInt(warRow["n"]),
		"apps_pkg":      toInt(appsRow["n"]),
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
	st["online_devices"] = onlineDevices
	st["online_journal"] = onlineJournal
	return st
}
