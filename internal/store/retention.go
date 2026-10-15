package store

import (
	"time"
)

// 数据保留窗口 worker —— 有界表治理。
//
// 背景（INC-20260930-02）：公网交付域名 28h 收到 181 万次请求（≈19 写/秒），
// access_log 每请求一行、devices 访客台账只增不减，表体随运行天数线性膨胀，
// 直接拖慢同库的聚合查询（看板重建冲到 20s+ 的帮凶）。
// 原始审计全文仍完整保留在 capture/*.jsonl（磁盘流水不删），这里只清 DB 里的
// 旧行；access_log 只用于"最近访问"展示与时间线定位，3 天窗口足够。
//
// 策略：每 30min 一轮；DELETE 分批（LIMIT 5000）防长事务锁表；单轮最多
// 40 批（≈20 万行）自我限流。

const (
	retentionAccessLogDays = 1   // access_log 保留窗口（用户要求：一天一清，控表体）
	retentionProbeLogDays  = 3   // probe_log 保留窗口（log.html 同步打点，只增）
	retentionDevicesDays   = 30  // 访客台账保留窗口（受控设备主体在 packet_meta/device_profiles，不受影响）
	retentionBatchSize     = 5000 // 单批删除行数（避免长锁）
	retentionBatchMax      = 40   // 单轮最大批数（自我限流）
	retentionEvery         = 30 * time.Minute
)

// StartRetentionWorker 启动保留窗口清理（每 30min 一轮；进程内单 goroutine）。
func StartRetentionWorker() {
	go func() {
		defer func() { _ = recover() }()
		ticker := time.NewTicker(retentionEvery)
		defer ticker.Stop()
		runRetentionOnce() // 启动即先跑一轮（服务启动后台执行，不阻塞）
		for range ticker.C {
			runRetentionOnce()
		}
	}()
}

func runRetentionOnce() {
	db := GetDB()
	if db == nil {
		return
	}
	now := time.Now()

	// ① access_log：ts_epoch 为 DOUBLE unix 秒
	accCutoff := float64(now.Add(-retentionAccessLogDays * 24 * time.Hour).Unix())
	purgeBatched(db,
		"DELETE FROM access_log WHERE ts_epoch < ? LIMIT ?",
		accCutoff, retentionBatchSize)

	// ② devices 访客台账：last_seen 为 'YYYY-MM-DD HH:MM:SS' 字符串（字典序即可比）
	devCutoff := now.Add(-retentionDevicesDays * 24 * time.Hour).Format(tsFmt)
	purgeBatched(db,
		"DELETE FROM devices WHERE last_seen < ? LIMIT ?",
		devCutoff, retentionBatchSize)

	// ③ probe_log：ts 同为 'YYYY-MM-DD HH:MM:SS' 字符串（log.html 同步打点，只增）
	probeCutoff := now.Add(-retentionProbeLogDays * 24 * time.Hour).Format(tsFmt)
	purgeBatched(db,
		"DELETE FROM probe_log WHERE ts < ? LIMIT ?",
		probeCutoff, retentionBatchSize)
}

// purgeBatched 分批删除直到清完或达到单轮上限。
func purgeBatched(db *DB, sql string, cutoff any, batch int) {
	for i := 0; i < retentionBatchMax; i++ {
		res, err := db.ExecResult(sql, cutoff, batch)
		if err != nil {
			return
		}
		n, nerr := res.RowsAffected()
		if nerr != nil || n < int64(batch) {
			return
		}
		time.Sleep(100 * time.Millisecond) // 批间让路，别跟主链抢
	}
}
