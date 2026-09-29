// dedupcheck 幂等/去重校验（MySQL 企业库版）。
//
// 用途：连接当前配置的 MySQL 库，执行两次 capture 回填（BackfillFull），
// 验证实体表去重幂等，并输出各表重复行检查（应为空）。
//
// 用法：
//
//	go run ./cmd/dedupcheck                         # 根目录取 MOCKC2_ROOT/工作目录
//	MOCKC2_DB_HOST=10.0.0.10 go run ./cmd/dedupcheck
package main

import (
	"fmt"
	"os"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/server"
	"mockc2/internal/store"
)

func q(title, sqlText string) {
	fmt.Println("==== " + title + " ====")
	for _, row := range store.GetDB().Query(sqlText) {
		fmt.Printf("    %v\n", row)
	}
}

func main() {
	s := config.Get()
	root := os.Getenv("MOCKC2_CAPTURE_ROOT")
	if root == "" {
		root = s.CaptureRoot
	}
	fmt.Printf("db: %s:%d/%s  capture: %s\n", s.DBHost, s.DBPort, s.DBName, root)

	if err := store.InitDB(server.DBConfigFromSettings(s)); err != nil {
		fmt.Println("init err:", err)
		os.Exit(1)
	}
	defer func() { _ = store.GetDB().Close() }()

	// 迁移幂等：已建库上重复执行应为 no-op（版本表命中）。
	t0 := time.Now()
	r := store.BackfillFull(root)
	fmt.Printf("backfill #1: %v (%.1fs)\n", r, time.Since(t0).Seconds())
	t0 = time.Now()
	r2 := store.BackfillFull(root)
	fmt.Printf("backfill #2 (幂等，应与 #1 处理文件数一致): %v (%.1fs)\n", r2, time.Since(t0).Seconds())

	q("tables count", "SELECT"+
		" (SELECT COUNT(*) FROM devices) devices,"+
		" (SELECT COUNT(*) FROM packets) packets,"+
		" (SELECT COUNT(*) FROM packet_meta) packet_meta,"+
		" (SELECT COUNT(*) FROM apps) apps,"+
		" (SELECT COUNT(*) FROM keychain_items) kc,"+
		" (SELECT COUNT(*) FROM memos) memos,"+
		" (SELECT COUNT(*) FROM files) files")

	fmt.Println("==== 重复行检查（应全部为空）====")
	q("apps 重复(uuid,bundle_id)", "SELECT uuid, bundle_id, COUNT(*) c FROM apps GROUP BY uuid,bundle_id HAVING c>1 LIMIT 20")
	q("keychain_items 重复(身份键)", "SELECT device_uuid,tbl,service,account,rowid_orig, COUNT(*) c FROM keychain_items GROUP BY 1,2,3,4,5 HAVING c>1 LIMIT 20")
	q("memos 重复(uuid,note_id)", "SELECT uuid,note_id, COUNT(*) c FROM memos GROUP BY 1,2 HAVING c>1 LIMIT 20")
	q("packets 重复(endpoint,sha256)", "SELECT endpoint, sha256, COUNT(*) c FROM packets GROUP BY 1,2 HAVING c>1 LIMIT 20")
	q("files 重复(幂等键)", "SELECT command_id,fname,sha256,complete, COUNT(*) c FROM files GROUP BY 1,2,3,4 HAVING c>1 LIMIT 20")
	q("wallet_seen 重复(packet_id,rule)", "SELECT packet_id, rule_name, COUNT(*) c FROM wallet_seen GROUP BY 1,2 HAVING c>1 LIMIT 20")
}
