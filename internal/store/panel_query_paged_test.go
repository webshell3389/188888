package store

import (
	"fmt"
	"testing"
)

// mkDev 构造最小设备行（只含分页/筛选相关字段）。
func mkDev(uuid, firstSeen, ip string, wallets int) map[string]any {
	d := map[string]any{
		"uuid":       uuid,
		"first_seen": firstSeen,
		"ip":         ip,
		"machine":    "",
		"ios":        "",
		"build":      "",
		"deviceName": "",
	}
	if wallets > 0 {
		ws := make([]map[string]any, 0, wallets)
		for i := 0; i < wallets; i++ {
			ws = append(ws, map[string]any{"rule": fmt.Sprintf("r%d", i), "count": 1})
		}
		d["wallets"] = ws
	}
	return d
}

func TestPageDevices(t *testing.T) {
	all := make([]map[string]any, 0, 45)
	for i := 0; i < 45; i++ {
		all = append(all, mkDev(
			fmt.Sprintf("uuid-%02d", i),
			fmt.Sprintf("2026-09-30 10:%02d:00", i),
			fmt.Sprintf("10.0.0.%d", i),
			i%3)) // 每 3 台有 1 台无钱包 → 30 有钱包 / 15 无
	}

	// 每页 20：45 条 → 3 页（20/20/5），总数与钱包全量计数
	rows, total, wTotal := pageDevices(all, 1, 20, "", "all")
	if total != 45 || len(rows) != 20 || wTotal != 30 {
		t.Fatalf("page1: total=%d len=%d wTotal=%d", total, len(rows), wTotal)
	}
	rows, _, _ = pageDevices(all, 3, 20, "", "all")
	if len(rows) != 5 {
		t.Fatalf("page3: len=%d", len(rows))
	}
	// 越界页 → 空切片，不 panic
	rows, total, _ = pageDevices(all, 99, 20, "", "all")
	if len(rows) != 0 || total != 45 {
		t.Fatalf("page99: len=%d total=%d", len(rows), total)
	}

	// 稳定排序：first_seen 倒序（uuid-44 最新在首行）
	rows, _, _ = pageDevices(all, 1, 20, "", "all")
	if toStr(rows[0]["uuid"]) != "uuid-44" {
		t.Fatalf("sort first: %v", rows[0]["uuid"])
	}

	// kw 筛选：大小写不敏感 + 跨全量（不是只筛当页）
	rows, total, _ = pageDevices(all, 1, 20, "UUID-07", "all")
	if total != 1 || len(rows) != 1 || toStr(rows[0]["uuid"]) != "uuid-07" {
		t.Fatalf("kw uuid filter: total=%d len=%d", total, len(rows))
	}
	rows, total, _ = pageDevices(all, 1, 20, "10.0.0.1", "all") // 命中 .1 与 .10~.19 共 11 台
	if total != 11 || len(rows) != 11 {
		t.Fatalf("kw ip filter: total=%d len=%d", total, len(rows))
	}

	// 钱包筛选（与旧前端语义一致：wallets 列表长度）
	if _, total, _ = pageDevices(all, 1, 20, "", "wallet"); total != 30 {
		t.Fatalf("wallet filter: total=%d", total)
	}
	if _, total, _ = pageDevices(all, 1, 20, "", "none"); total != 15 {
		t.Fatalf("none filter: total=%d", total)
	}

	// 参数收敛：per 缺省 20 / 上限 200；page<1 视作 1
	rows, _, _ = pageDevices(all, 0, 0, "", "all")
	if len(rows) != 20 {
		t.Fatalf("clamp per: len=%d", len(rows))
	}
	rows, _, _ = pageDevices(all, 1, 999, "", "all")
	if len(rows) != 45 {
		t.Fatalf("clamp per max: len=%d", len(rows))
	}

	// 空列表：不 panic，空切片
	rows, total, wTotal = pageDevices(nil, 1, 20, "", "all")
	if total != 0 || wTotal != 0 || rows == nil || len(rows) != 0 {
		t.Fatalf("empty: total=%d wTotal=%d len=%d", total, wTotal, len(rows))
	}
}
