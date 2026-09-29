package store

// MySQL 集成测试（企业库真连）。
//
// 门控（防误伤）：
//   MOCKC2_TEST_DB=1          必须显式开启
//   MOCKC2_DB_NAME 含 "test"   库名必须含 test（拒绝在非测试库执行破坏性用例）
//   MOCKC2_DB_HOST/PORT/USER/PASSWORD 连接参数（或 mockc2_settings.json）
//
// 运行示例：
//   $env:MOCKC2_TEST_DB="1"; $env:MOCKC2_DB_NAME="mockc2_test"; go test ./internal/store/ -run TestMySQL -v
//
// 覆盖：迁移、幂等 UPSERT、包去重、指令队列原子领取、批量写、身份映射。

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"mockc2/internal/config"
)

func mysqlTestConfig(t *testing.T) DBConfig {
	t.Helper()
	if os.Getenv("MOCKC2_TEST_DB") != "1" {
		t.Skip("MOCKC2_TEST_DB!=1，跳过 MySQL 集成测试")
	}
	s := config.Get()
	if !strings.Contains(strings.ToLower(s.DBName), "test") {
		t.Skipf("库名 %q 不含 'test'，拒绝执行破坏性测试", s.DBName)
	}
	return DBConfig{
		Host: s.DBHost, Port: s.DBPort, User: s.DBUser,
		Password: s.DBPassword, Name: s.DBName,
		MaxOpenConns: 8, MaxIdleConns: 4,
	}
}

// resetSchema 清空全部业务表（测试库专用；由门控保证安全）。
func resetSchema(t *testing.T, d *DB) {
	t.Helper()
	tables := []string{
		"result_data", "memos", "keychain_meta", "keychain_items", "apps",
		"device_profiles", "device_commands", "kv", "command_queue",
		"wallet_seen", "wallet_rules", "pe_events", "files", "chunks",
		"commands", "probe_log", "packet_meta", "packets", "access_log",
		"devices", "schema_migrations",
	}
	for _, tb := range tables {
		if _, err := d.conn.Exec("DROP TABLE IF EXISTS " + tb); err != nil {
			t.Fatalf("drop %s: %v", tb, err)
		}
	}
}

func TestMySQL_MigrateAndSeed(t *testing.T) {
	cfg := mysqlTestConfig(t)
	if err := InitDB(cfg); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	d := GetDB()
	resetSchema(t, d)
	if err := d.migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 迁移版本落表
	row := d.QueryOne("SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1")
	if row == nil || toInt(row["version"]) != 1 {
		t.Fatalf("schema_migrations 应为 v1: %v", row)
	}
	// 规则种子幂等
	SeedRules()
	SeedRules()
	n := toInt(d.QueryOne("SELECT COUNT(*) n FROM wallet_rules")["n"])
	if n != int64(len(WALLET_RULES)) {
		t.Fatalf("wallet_rules 应为 %d 条，实际 %d", len(WALLET_RULES), n)
	}
}

func TestMySQL_DeviceUpsertIdempotent(t *testing.T) {
	if err := InitDB(mysqlTestConfig(t)); err != nil {
		t.Fatal(err)
	}
	d := GetDB()
	resetSchema(t, d)
	if err := d.migrate(); err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"ip": "10.9.9.1", "ua": "UA-test", "deviceVersion": "IOS 18.6.2",
		"domain": "test.lab", "host": "test.lab",
	}
	id1 := RecordDevice(rec)
	id2 := RecordDevice(rec) // 复见：同身份刷新，不新增
	if id1 == 0 || id1 != id2 {
		t.Fatalf("同身份应命中同一行: id1=%d id2=%d", id1, id2)
	}
	if n := toInt(d.QueryOne("SELECT COUNT(*) n FROM devices")["n"]); n != 1 {
		t.Fatalf("devices 应 1 行，实际 %d", n)
	}
	// 版本升级：先无版本后上报 → 原地升级不拆行
	recNoVer := map[string]any{"ip": "10.9.9.2", "ua": "UA-test2", "deviceVersion": "unknown"}
	RecordDevice(recNoVer)
	recNoVer["deviceVersion"] = "IOS 18.4"
	RecordDevice(recNoVer)
	if n := toInt(d.QueryOne("SELECT COUNT(*) n FROM devices WHERE ip='10.9.9.2'")["n"]); n != 1 {
		t.Fatalf("无版本→有版本应原地升级为 1 行，实际 %d", n)
	}
}

func TestMySQL_PacketDedupAndMeta(t *testing.T) {
	if err := InitDB(mysqlTestConfig(t)); err != nil {
		t.Fatal(err)
	}
	d := GetDB()
	resetSchema(t, d)
	if err := d.migrate(); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"uuid":"dev-test-1","status":"idle"}`)
	pid1, dup1 := RecordPacket("beacon", "beacon/a.json", len(body), body)
	if pid1 == 0 || dup1 {
		t.Fatalf("首包应插入: pid=%d dup=%v", pid1, dup1)
	}
	pid2, dup2 := RecordPacket("beacon", "beacon/b.json", len(body), body)
	if pid2 != pid1 || !dup2 {
		t.Fatalf("重复包应命中同一行且 dup=true: pid=%d dup=%v", pid2, dup2)
	}
	// packet_meta 提炼字段存在且唯一约束生效
	if n := toInt(d.QueryOne("SELECT COUNT(*) n FROM packet_meta WHERE packet_id=?", pid1)["n"]); n == 0 {
		t.Fatal("packet_meta 应有提炼字段")
	}
	if n := toInt(d.QueryOne("SELECT COUNT(*) n FROM packets")["n"]); n != 1 {
		t.Fatalf("packets 应 1 行，实际 %d", n)
	}
}

func TestMySQL_CommandQueueAtomic(t *testing.T) {
	if err := InitDB(mysqlTestConfig(t)); err != nil {
		t.Fatal(err)
	}
	d := GetDB()
	resetSchema(t, d)
	if err := d.migrate(); err != nil {
		t.Fatal(err)
	}
	q := NewCommandQueue()
	// 单台武装 → 领走 → 再领为空（once 单发即清）
	q.SetFor("dev-A", "execute_command", map[string]any{"cmd": "id"}, true, "", "")
	if got := q.TakeFor([]string{"dev-A"}); got == nil || got["type"] != "execute_command" {
		t.Fatalf("按设备应领到指令: %v", got)
	}
	if got := q.TakeFor([]string{"dev-A"}); got != nil {
		t.Fatalf("once 指令领后应为空: %v", got)
	}
	// 全局单行队列
	q.Set("wallet_scan", map[string]any{}, true, "")
	if got := q.Take(); got == nil || got["type"] != "wallet_scan" {
		t.Fatalf("全局队列应领到: %v", got)
	}
	if got := q.Take(); got != nil {
		t.Fatalf("全局 once 领后应为空: %v", got)
	}
	// 取消不影响其它设备
	q.SetFor("dev-B", "ls", map[string]any{}, true, "", "")
	q.SetFor("dev-C", "ls", map[string]any{}, true, "", "")
	q.ClearFor("dev-B")
	if got := q.TakeFor([]string{"dev-B"}); got != nil {
		t.Fatalf("dev-B 应已取消: %v", got)
	}
	if got := q.TakeFor([]string{"dev-C"}); got == nil {
		t.Fatal("dev-C 不应受影响")
	}
}

func TestMySQL_MemosIdempotentAndBatch(t *testing.T) {
	if err := InitDB(mysqlTestConfig(t)); err != nil {
		t.Fatal(err)
	}
	d := GetDB()
	resetSchema(t, d)
	if err := d.migrate(); err != nil {
		t.Fatal(err)
	}
	// memos 幂等 UPSERT：同 (uuid,note_id) 两次 → 1 行，hits=2
	body := []byte(`{"lhu":"dev-memo-1","list":[{"id":7,"title":"t","snippet":"s","mod":0}]}`)
	pid, _ := RecordPacket("nb", "nb/a.json", len(body), body)
	parseMemos(body, pid)
	parseMemos(body, pid)
	row := d.QueryOne("SELECT COUNT(*) n, MAX(hits) h FROM memos WHERE uuid='dev-memo-1'")
	if toInt(row["n"]) != 1 || toInt(row["h"]) != 2 {
		t.Fatalf("memos 应 1 行 hits=2: %v", row)
	}
	// 批量多值 INSERT：500 行 access_log
	recs := make([]map[string]any, 500)
	for i := range recs {
		recs[i] = map[string]any{"role": "delivery", "method": "GET", "path": "/batch",
			"client": "10.0.0.9", "status": 200, "cost_ms": 0.5}
	}
	RecordAccessBatch(recs)
	if n := toInt(d.QueryOne("SELECT COUNT(*) n FROM access_log WHERE path='/batch'")["n"]); n != 500 {
		t.Fatalf("access_log 应 500 行，实际 %d", n)
	}
}

func TestMySQL_IdentityPairs(t *testing.T) {
	if err := InitDB(mysqlTestConfig(t)); err != nil {
		t.Fatal(err)
	}
	d := GetDB()
	resetSchema(t, d)
	if err := d.migrate(); err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf(`{"lhu":"dev-pair-1","machine":"iPhone17,1","ip":"10.7.7.7","ios_version":"18.6"}`))
	pid, _ := RecordPacket("a", "a/p.json", len(body), body)
	parseProfile(body, pid)
	pairs := loadDeviceIdentityPairs()
	if len(pairs) != 1 || pairs[0].uuid != "dev-pair-1" || pairs[0].ip != "10.7.7.7" {
		t.Fatalf("身份映射应为 lhu↔ip: %v", pairs)
	}
	// 缓存读取一致性
	if len(deviceIdentityPairs()) != 1 {
		t.Fatal("缓存应返回同一映射")
	}
}
