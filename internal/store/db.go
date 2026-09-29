// Package store 数据与服务层（企业级）：MySQL/MariaDB 连接池、版本化迁移、
// 参数化查询执行、批量写、事务。
//
// 设计要点（对齐企业部署）：
//   - 驱动：github.com/go-sql-driver/mysql（纯 Go，CGO_ENABLED=0 静态编译不受影响）
//   - 连接池：MaxOpenConns / MaxIdleConns / ConnMaxLifetime 可配；MySQL 自身处理并发写，
//     不再需要 SQLite 时代的"全局写锁"
//   - 迁移：schema_migrations 版本表 + GET_LOCK 命名锁（多实例首启并发安全）
//   - 原子语义：领指令等读改写用 SELECT ... FOR UPDATE（行锁）替代旧单写者串行化
//   - 可观测：查询/写/错误计数，经 server 层 /__metrics 暴露
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
)

// ── 连接配置 ──

// DBConfig MySQL/MariaDB 连接配置（由 config.Settings 装配，见 server.InitServices）。
type DBConfig struct {
	Host            string        // 主机（默认 127.0.0.1）
	Port            int           // 端口（默认 3306）
	User            string        // 用户名
	Password        string        // 密码
	Name            string        // 库名
	Params          string        // 额外 DSN 参数（k=v&k2=v2；charset 固定 utf8mb4）
	MaxOpenConns    int           // 连接池上限（默认 32）
	MaxIdleConns    int           // 空闲连接（默认 8）
	ConnMaxLifetime time.Duration // 连接最长复用（默认 30m，避开 wait_timeout 断连）
}

func (c *DBConfig) normalize() {
	if c.Host == "" {
		c.Host = "127.0.0.1"
	}
	if c.Port <= 0 {
		c.Port = 3306
	}
	if c.MaxOpenConns <= 0 {
		c.MaxOpenConns = 32
	}
	if c.MaxIdleConns < 0 {
		c.MaxIdleConns = 0
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = 8
	}
	if c.MaxIdleConns > c.MaxOpenConns {
		c.MaxIdleConns = c.MaxOpenConns
	}
	if c.ConnMaxLifetime <= 0 {
		c.ConnMaxLifetime = 30 * time.Minute
	}
}

// DSN 生成 go-sql-driver 标准 DSN（使用驱动 Config 结构，密码/库名特殊字符自动转义）。
func (c DBConfig) DSN() string {
	c.normalize()
	cfg := mysql.NewConfig()
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = c.Name
	cfg.ParseTime = false
	cfg.Loc = time.Local
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 30 * time.Second
	cfg.MultiStatements = false
	cfg.InterpolateParams = false
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	for _, kv := range strings.Split(c.Params, "&") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		cfg.Params[strings.TrimSpace(kv[:i])] = strings.TrimSpace(kv[i+1:])
	}
	return cfg.FormatDSN()
}

// ── DB 句柄 ──

// DB MySQL 连接池句柄。所有方法并发安全（database/sql 语义）。
type DB struct {
	conn    *sql.DB
	queries atomic.Int64 // SELECT 计数
	execs   atomic.Int64 // 写语句计数
	errs    atomic.Int64 // 执行错误计数
}

// OpenDB 打开连接池并执行迁移。
func OpenDB(cfg DBConfig) (*DB, error) {
	cfg.normalize()
	conn, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(cfg.MaxOpenConns)
	conn.SetMaxIdleConns(cfg.MaxIdleConns)
	conn.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	conn.SetConnMaxIdleTime(10 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mysql ping %s:%d/%s: %w", cfg.Host, cfg.Port, cfg.Name, err)
	}

	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return d, nil
}

// Close 关闭连接池。
func (d *DB) Close() error {
	if d == nil || d.conn == nil {
		return nil
	}
	return d.conn.Close()
}

// Ping 健康检查（/__health 与运维巡检用）。
func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.conn == nil {
		return errors.New("db not initialized")
	}
	return d.conn.PingContext(ctx)
}

// Stats 连接池运行态（运维可观测）。
func (d *DB) Stats() sql.DBStats {
	if d == nil || d.conn == nil {
		return sql.DBStats{}
	}
	return d.conn.Stats()
}

// ── 查询/写入 ──

// Exec 写操作（单行），返回 lastrowid；失败返回 0（与旧行为一致，调用方按 0 判失败）。
func (d *DB) Exec(query string, args ...any) int64 {
	id, _ := d.ExecResult(query, args...)
	if id == nil {
		return 0
	}
	v, err := id.LastInsertId()
	if err != nil {
		return 0
	}
	return v
}

// ExecResult 写操作原始结果（需要 RowsAffected / 错误分类时使用）。
func (d *DB) ExecResult(query string, args ...any) (sql.Result, error) {
	d.execs.Add(1)
	res, err := d.conn.Exec(query, args...)
	if err != nil {
		d.logErr("exec", query, err)
	}
	return res, err
}

// SQLArgs 批量写元素。
type SQLArgs struct {
	SQL  string
	Args []any
}

// manyChunk 多值 INSERT 单批行数（包体上限内，避免超 max_allowed_packet / 占满重做日志）。
const manyChunk = 200

// Many 批量写：相同 SQL 的连续行合并为多值 INSERT（大幅减少往返），
// 在单事务内执行。与旧 SQLite 版签名兼容；返回值仅保证非空结果的
// lastrowid 语义可用（调用方均不使用逐行 id，见各调用点）。
func (d *DB) Many(rows []SQLArgs) []int64 {
	ids := make([]int64, 0, len(rows))
	if len(rows) == 0 {
		return ids
	}
	tx, err := d.conn.Begin()
	if err != nil {
		d.logErr("many-begin", "", err)
		return ids
	}
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].SQL == rows[i].SQL {
			j++
		}
		group := rows[i:j]
		for s := 0; s < len(group); s += manyChunk {
			e := s + manyChunk
			if e > len(group) {
				e = len(group)
			}
			chunk := group[s:e]
			if len(chunk) == 1 {
				res, err := tx.Exec(chunk[0].SQL, chunk[0].Args...)
				if err != nil {
					d.logErr("many", chunk[0].SQL, err)
					continue
				}
				if id, err := res.LastInsertId(); err == nil {
					ids = append(ids, id)
				}
				continue
			}
			q, args := buildMultiInsert(chunk)
			res, err := tx.Exec(q, args...)
			if err != nil {
				// 多值批失败：退化为单行执行，保证部分成功（幂等键场景）。
				d.logErr("many-multi", q, err)
				for _, r := range chunk {
					if _, err := tx.Exec(r.SQL, r.Args...); err != nil {
						d.logErr("many-fallback", r.SQL, err)
					}
				}
				continue
			}
			if id, err := res.LastInsertId(); err == nil {
				ids = append(ids, id)
			}
		}
		i = j
	}
	if err := tx.Commit(); err != nil {
		d.logErr("many-commit", "", err)
	}
	return ids
}

// buildMultiInsert 把同构单行 INSERT 改造成多值 INSERT：
//
//	INSERT INTO t(a,b) VALUES(?,?)  × N  ->  INSERT INTO t(a,b) VALUES(?,?),(?,?),...
func buildMultiInsert(chunk []SQLArgs) (string, []any) {
	base := chunk[0].SQL
	idx := strings.Index(base, "VALUES")
	if idx < 0 {
		// 无 VALUES 子句（理论不可达）：退化为单行
		return base, chunk[0].Args
	}
	head := strings.TrimRight(base[:idx], " ") // "INSERT INTO t(a,b)"
	rest := strings.TrimSpace(base[idx+len("VALUES"):])
	if !strings.HasPrefix(rest, "(") {
		return base, chunk[0].Args
	}
	// 取第一个括号组作为模版 "(?,?)"，并保留其后缀（如 ODKU 子句），
	// 否则批量语句会丢失 ON DUPLICATE KEY UPDATE 语义（幂等键场景 1062 报错）。
	depth, end := 0, -1
	for i, r := range rest {
		if r == '(' {
			depth++
		} else if r == ')' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return base, chunk[0].Args
	}
	tuple := rest[:end+1]
	suffix := strings.TrimSpace(rest[end+1:])
	args := make([]any, 0, len(chunk)*len(chunk[0].Args))
	parts := make([]string, 0, len(chunk))
	for _, r := range chunk {
		parts = append(parts, tuple)
		args = append(args, r.Args...)
	}
	stmt := head + " VALUES " + strings.Join(parts, ",")
	if suffix != "" {
		stmt += " " + suffix
	}
	return stmt, args
}

// Query 返回行集（每行 map）。读操作走连接池并行。
func (d *DB) Query(query string, args ...any) []map[string]any {
	d.queries.Add(1)
	rows, err := d.conn.Query(query, args...)
	if err != nil {
		d.logErr("query", query, err)
		return nil
	}
	defer rows.Close()
	return scanRows(rows)
}

// QueryOne 返回首行，无则 nil。
func (d *DB) QueryOne(query string, args ...any) map[string]any {
	rows := d.Query(query, args...)
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

// Tx 在事务内执行（原子读改写场景需自行在 SELECT 上加 FOR UPDATE 行锁）。
func (d *DB) Tx(fn func(tx *sql.Tx) error) error {
	tx, err := d.conn.Begin()
	if err != nil {
		d.logErr("tx-begin", "", err)
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// logErr 记录数据层错误（限流采样：前 10 条全打，此后每 100 条打一条，避免错误风暴刷屏）。
func (d *DB) logErr(op, query string, err error) {
	n := d.errs.Add(1)
	if n <= 10 || n%100 == 0 {
		fmt.Printf("[db] %s err#%d: %v :: %s\n", op, n, err, firstLine(query))
	}
}

func scanRows(rows *sql.Rows) []map[string]any {
	cols, err := rows.Columns()
	if err != nil {
		return nil
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				m[c] = string(v)
			default:
				m[c] = v
			}
		}
		out = append(out, m)
	}
	return out
}

// ── 错误分类 ──

// IsDupErr 是否 MySQL 唯一键冲突（Error 1062）。
func IsDupErr(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

// ── 指标快照（/__metrics）──

// MetricsSnapshot 数据层计数（Prometheus 文本由 server 层渲染）。
func MetricsSnapshot() map[string]int64 {
	d := GetDB()
	out := map[string]int64{}
	if d != nil {
		out["db_queries_total"] = d.queries.Load()
		out["db_execs_total"] = d.execs.Load()
		out["db_errors_total"] = d.errs.Load()
		if st := d.Stats(); true {
			out["db_open_connections"] = int64(st.OpenConnections)
			out["db_in_use"] = int64(st.InUse)
			out["db_idle"] = int64(st.Idle)
			out["db_wait_count"] = st.WaitCount
			out["db_wait_duration_ms"] = st.WaitDuration.Milliseconds()
		}
	}
	out["expand_fastfail_total"] = expandFastFail.Load()
	out["idmap_reload_total"] = idMapReload.Load()
	return out
}

// ── 单例 ──

var (
	dbMu     sync.RWMutex
	globalDB *DB
)

// InitDB 初始化全局连接池（mysql DSN 由 cfg 装配）。
func InitDB(cfg DBConfig) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	d, err := OpenDB(cfg)
	if err != nil {
		return err
	}
	globalDB = d
	return nil
}

// GetDB 返回全局库。
func GetDB() *DB {
	dbMu.RLock()
	defer dbMu.RUnlock()
	return globalDB
}

// MigrateOnce 仅执行迁移 + 规则种子后关闭（多 worker 父进程先行调用，
// 避免 N 个子进程并发首建库/迁移产生竞争）。
func MigrateOnce(cfg DBConfig) error {
	d, err := OpenDB(cfg)
	if err != nil {
		return err
	}
	seedRulesOn(d)
	return d.Close()
}

// Now 返回 (格式化时间, epoch)。
func Now() (string, float64) {
	t := time.Now()
	return t.Format("2006-01-02 15:04:05"), float64(t.UnixNano()) / 1e9
}
