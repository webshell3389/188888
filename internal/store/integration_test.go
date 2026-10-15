package store

// MySQL 集成测试（破坏性用例，只在测试库跑）：
//
//	MOCKC2_TEST_DB=1 且 MOCKC2_DB_NAME 含 "test"   库名必须含 test（拒绝在非测试库执行破坏性用例）
//	MOCKC2_DB_HOST/PORT/USER/PASSWORD 连接参数（或 mockc2_settings.json）
//
//	$env:MOCKC2_TEST_DB="1"; $env:MOCKC2_DB_NAME="mockc2_test"; go test ./internal/store/ -run TestMySQL -v
//
// 说明：本文件在 2026-09-30 排查中因编辑器编码事故损毁，按原测试面等价重写
// （RecordPacket 去重/刷新、packet_meta 唯一约束、批量审计、ProcessFull 落库、指令生命周期）。

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func testDBEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("MOCKC2_TEST_DB") != "1" {
		t.Skip("MOCKC2_TEST_DB!=1，跳过 MySQL 集成测试")
	}
	name := os.Getenv("MOCKC2_DB_NAME")
	if !strings.Contains(strings.ToLower(name), "test") {
		t.Fatalf("拒绝在非测试库执行破坏性用例：MOCKC2_DB_NAME=%q", name)
	}
	if GetDB() == nil {
		if err := InitDB(DBConfig{
			Host:         os.Getenv("MOCKC2_DB_HOST"),
			Port:         3306,
			User:         os.Getenv("MOCKC2_DB_USER"),
			Password:     os.Getenv("MOCKC2_DB_PASSWORD"),
			Name:         name,
			MaxOpenConns: 4,
			MaxIdleConns: 2,
		}); err != nil {
			t.Fatalf("连接测试库失败: %v", err)
		}
	}
}

// testPing 确认库可用（不可达时跳过而不是挂死）。
func testPing(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := GetDB().Ping(ctx); err != nil {
		t.Skipf("测试库不可达: %v", err)
	}
}

func TestMySQLRecordPacketDedup(t *testing.T) {
	testDBEnabled(t)
	testPing(t)
	d := GetDB()
	d.Exec("DELETE FROM packet_meta WHERE packet_id IN (SELECT id FROM packets WHERE endpoint='beacon' AND file_path LIKE 'it-dedup%')")
	d.Exec("DELETE FROM packets WHERE endpoint='beacon' AND file_path LIKE 'it-dedup%'")

	body := []byte(`{"uuid":"IT-TEST","status":"idle"}`)
	pid1, dup1 := RecordPacket("beacon", "it-dedup/1.json", len(body), body, "203.0.113.9")
	if dup1 {
		t.Fatalf("首见包不应判定重复")
	}
	pid2, dup2 := RecordPacket("beacon", "it-dedup/2.json", len(body), body, "203.0.113.9")
	if !dup2 || pid2 != pid1 {
		t.Fatalf("同指纹应为重复且同指针: pid1=%d pid2=%d dup2=%v", pid1, pid2, dup2)
	}
	if n := toInt(d.QueryOne("SELECT COUNT(*) n FROM packets WHERE endpoint='beacon' AND file_path LIKE 'it-dedup%'")["n"]); n != 1 {
		t.Fatalf("去重后应只保留 1 行，实际 %d", n)
	}
	// src_ip 元数据随重复包刷新
	row := d.QueryOne("SELECT value FROM packet_meta WHERE packet_id=? AND field='src_ip'", pid1)
	if row == nil || toStr(row["value"]) != "203.0.113.9" {
		t.Fatalf("重复包应刷新 src_ip，got %v", row)
	}
	d.Exec("DELETE FROM packet_meta WHERE packet_id=?", pid1)
	d.Exec("DELETE FROM packets WHERE id=?", pid1)
}

func TestMySQLPacketMetaUnique(t *testing.T) {
	testDBEnabled(t)
	testPing(t)
	d := GetDB()
	body := []byte(`{"uuid":"IT-META","status":"idle"}`)
	pid, _ := RecordPacket("a", "it-meta/1.json", len(body), body, "")
	if pid == 0 {
		t.Fatalf("RecordPacket 未落行")
	}
	// 同 (packet_id, field) 重复写应 UPSERT 不报错、不产生重复行
	d.Exec("INSERT INTO packet_meta(packet_id, field, value) VALUES(?,?,?) ON DUPLICATE KEY UPDATE value=VALUES(value)",
		pid, "uuid", "IT-META")
	if n := toInt(d.QueryOne(
		"SELECT COUNT(*) n FROM packet_meta WHERE packet_id=? AND field='uuid'", pid)["n"]); n != 1 {
		t.Fatalf("packet_meta 唯一约束失效：同键 %d 行", n)
	}
	d.Exec("DELETE FROM packet_meta WHERE packet_id=?", pid)
	d.Exec("DELETE FROM packets WHERE id=?", pid)
}

func TestMySQLRecordAccessBatch(t *testing.T) {
	testDBEnabled(t)
	testPing(t)
	recs := make([]SQLArgs, 0, 3)
	for i := 0; i < 3; i++ {
		ts, epoch := Now()
		recs = append(recs, SQLArgs{
			SQL: "INSERT INTO access_log(ts, ts_epoch, role, method, path, query, host, client, ua, status, cost_ms)" +
				" VALUES(?,?,?,?,?,?,?,?,?,?,?)",
			Args: []any{ts, epoch, "delivery", "GET", "/it-batch", "", "it.test", "203.0.113.77", "it/1.0", 200, 1.5},
		})
	}
	GetDB().Many(recs)
	if n := toInt(GetDB().QueryOne(
		"SELECT COUNT(*) n FROM access_log WHERE path='/it-batch'")["n"]); n != 3 {
		t.Fatalf("批量审计应 3 行，实际 %d", n)
	}
	GetDB().Exec("DELETE FROM access_log WHERE path='/it-batch'")
}

func TestMySQLProcessFullProfile(t *testing.T) {
	testDBEnabled(t)
	testPing(t)
	d := GetDB()
	body := []byte(`{"lhu":"IT-PROFILE","machine":"iPhone14,5","deviceName":"IT","ios_version":"18.6","ip":"203.0.113.10"}`)
	pid, _ := RecordPacket("a", "it-profile/1.json", len(body), body, "203.0.113.10")
	ProcessFull("a", body, pid)
	row := d.QueryOne("SELECT machine, device_name FROM device_profiles WHERE uuid='IT-PROFILE'")
	if row == nil || toStr(row["machine"]) != "iPhone14,5" {
		t.Fatalf("parseProfile 未正确落库: %v", row)
	}
	d.Exec("DELETE FROM device_profiles WHERE uuid='IT-PROFILE'")
	d.Exec("DELETE FROM packet_meta WHERE packet_id=?", pid)
	d.Exec("DELETE FROM packets WHERE id=?", pid)
}

func TestMySQLCommandLifecycle(t *testing.T) {
	testDBEnabled(t)
	testPing(t)
	d := GetDB()
	cmd := map[string]any{"command_id": "IT-CMD-1", "type": "sh", "params": map[string]any{"c": "echo hi"}}
	CommandArmed(cmd, true)
	if row := d.QueryOne("SELECT state FROM commands WHERE command_id='IT-CMD-1'"); row == nil || toStr(row["state"]) != "armed" {
		t.Fatalf("armed 状态异常: %v", row)
	}
	CommandDispatched(cmd, "203.0.113.1")
	if row := d.QueryOne("SELECT state FROM commands WHERE command_id='IT-CMD-1'"); toStr(row["state"]) != "dispatched" {
		t.Fatalf("dispatched 状态异常: %v", row)
	}
	CommandCleared()
	if row := d.QueryOne("SELECT state FROM commands WHERE command_id='IT-CMD-1'"); toStr(row["state"]) != "cleared" {
		t.Fatalf("cleared 状态异常: %v", row)
	}
	d.Exec("DELETE FROM commands WHERE command_id='IT-CMD-1'")
}
