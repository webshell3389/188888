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
  UNIQUE KEY ux_packets_file (file_path),
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
func (d *DB) migrate() error {
	if _, err := d.conn.Exec(
		"CREATE TABLE IF NOT EXISTS schema_migrations(" +
			"version INT PRIMARY KEY, applied_at VARCHAR(32) NOT NULL)" +
			" ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"); err != nil {
		return err
	}

	// 跨实例迁移互斥：拿不到锁（超时）直接报错，避免并发 DDL 竞态。
	var locked *int64
	if err := d.conn.QueryRow("SELECT GET_LOCK(?, 60)", migrationLockName).Scan(&locked); err != nil {
		return fmt.Errorf("migrate lock: %w", err)
	}
	if locked == nil || *locked != 1 {
		return errors.New("migrate lock timeout (another instance is migrating)")
	}
	defer func() { _, _ = d.conn.Exec("SELECT RELEASE_LOCK(?)", migrationLockName) }()

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
