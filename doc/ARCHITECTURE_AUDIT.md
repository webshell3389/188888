# Mock C2 架构审计与企业级改造方案

> 目标：**去耦、读写分离、有界数据、可观测、幂等运维** —— 让系统能多天/多周稳定运行，而不是“两天一维护”。
> 范围：Go 后端（`internal/`）、部署（nginx/systemd/MySQL）、运行时数据面。

---

## 0. 结论摘要

现状是**单体 + 上帝包 + 热路径混重活 + 无界大表 + 进程内缓存**，任一环节劣化都会把 DB 拖死，进而全站卡死（已发生：`/__dash/api` 30s、nginx 502、设备连不上）。

三条改造主线：

1. **读写分离 + 物化读模型**：写入走轻量追加；所有聚合（进度/设备统计/端点计数）**增量物化**，读接口只查小表 → 消除全表扫。
2. **模块解耦 + 依赖注入**：拆 `store` 上帝包；`server` 依赖 repo 接口而非全局单例；后台任务独立 worker。
3. **有界数据 + 可观测 + 幂等运维**：表分区/保留、查询超时、健康/版本端点、结构化日志、systemd 硬化、脚本化发布回滚。

---

## 1. 现状证据（生产实测，2026-10-03）

**运行时数据面**

| 表 | 行数 | 体积 | 问题 |
|---|---|---|---|
| `access_log` | 637万 | 数据 2.1GB + 索引 1.2GB | 无 `ts_epoch` 主序索引 → 全表 filesort |
| `probe_log` | 374万 | 374MB | 只增 |
| `apps` | 26万 | 58MB | — |
| `memos` | 6.8万 | 13MB | — |
| `keychain_items` | 494 | — | — |
| `capture/`（磁盘） | — | ~11GB | `nb` 3.0G / `notes_db` 3.3G / 备份 jsonl 3.2G |

**已发生的故障与根因**

| 现象 | 根因 | 现状 |
|---|---|---|
| 全站 502、设备连不上 | `ClientStageProgress` 对 `access_log` 全表 `GROUP BY`（60s+） | 已改 `client_stage` 增量表 |
| `/__dash/api` 30s | `RecentAccess` `ORDER BY ts_epoch` 无索引 filesort | 已加 `ix_access_epoch` |
| 设备“重复上线” | beacon 去重刷 `recv_ts` 污染 `first_seen=MIN(recv_ts)` | 已加不可变 `first_recv_ts` |
| DB 连接 `i/o timeout` 刷屏 | 慢查询占满连接 → 30s 读超时 | 随慢查询消除而消失 |

**代码结构**

| 维度 | 数值 | 评价 |
|---|---|---|
| `internal/store` | 5739 行 | **上帝包**：db/ingest/query/capture/chunk/wallet/schema/bip39/notes 全在一起 |
| DB 调用点 | 221 处 | 散落，无 repo 边界 |
| 路由 | 62 条 | 集中 `server` 包，可接受 |
| 后台 goroutine | ~10 个（各 `go func`） | 无统一生命周期/优雅停机 |
| 包级单例/缓存 | ~15 个（`globalDB`/`globalStore`/`listDevicesCache`/`progCache`/`dashAPICache`/`idMapCache`…） | 多实例不一致 + 测试困难 |

---

## 2. 问题清单（按严重度）

### P0 — 稳定性（直接导致“两天一救火”）
- **P0-1 热路径混重聚合**：`ListDevices` 一次跑 6+ 组聚合；`/beacon` 若回落全量查询会放大。→ 物化 + 轻量身份映射（已部分：`deviceIdentityPairs`）。
- **P0-2 无界大表**：`access_log` / `probe_log` 无分区；保留策略是 `DELETE LIMIT`（慢且留碎片）。→ 按天分区 + `DROP PARTITION`。
- **P0-3 DB 无查询超时/上下文**：慢查询会一直占连接，放大抖动。→ 所有查询带 `context` deadline；连接池加 `readTimeout`。
- **P0-4 进程内 SWR 缓存**：多实例不一致、内存常驻且随设备量增长。→ 改为“物化表 + 极短 TTL”，或统一缓存层。
- **P0-5 Python 工具在请求路径**：`war/analyze` 同步 exec；检测应全程走队列（`forensics.Enqueue` 已建一半）。

### P1 — 可维护/解耦
- **P1-1 `store` 上帝包**：拆为 `db`（连接池/迁移）、`ingest`（写）、`query`（读模型）、`capture`（留存/分块）、`wallet`/`notes`（领域）。
- **P1-2 全局单例**：`GetDB()/GetStore()/GetQueue()` → 显式注入到 `service`/`repo`，提升可测性、可多实例。
- **P1-3 迁移与运行耦合**：迁移与业务同进程启动；无 up/down、无版本端点。→ 独立迁移命令 + `schema_migrations` 已具雏形，补版本端点与回滚。

### P2 — 企业级运维
- **P2-1 systemd 硬化 + 资源限制**（`MemoryMax`/`CPUQuota`/`NoNewPrivileges`/`PrivateTmp`）。
- **P2-2 结构化日志 + 关键事件**（慢查询、丢弃、背压、DB 重连）。
- **P2-3 发布/回滚脚本化 + 冒烟**（现为手工 scp + swap）。
- **P2-4 多实例一致性收尾**：会话/指令/分块已外置；残留进程内缓存需清理。

---

## 3. 目标架构

```
            ┌──────────── cmd/mockc2 (入口/CLI/信号) ────────────┐
            │                                                    │
  HTTP ──► server(路由/中间件/鉴权) ──► service(用例编排) ──► repo(接口)
  交付链      │            │                 │                    │
  管理台      │            │                 │            ┌───────┴────────┐
  C2回传      │            │                 │            │  store 拆包     │
            │            │                 │            │  db / ingest    │
            │            │                 │            │  query / capture│
            │            │                 │            │  wallet / notes │
            │            │                 │            └───────┬────────┘
            │            │                 │                    │
         worker(独立) ◄── queue ───────────┘               MySQL / capture/
         forensics / retention / materializer
```

**两条铁律**
1. **写路径（beacon/a/u/nb/war/result）永远不做全量聚合**；只做：留存 + 实体 UPSERT + 物化计数增量 + 入队。
2. **读路径只读物化小表/缓存**；任何 `GROUP BY` 大表都必须在后台物化，不在请求里跑。

---

## 4. 分阶段计划（含验收）

### Phase 0 — 可观测与护栏（半天，低风险，先止血）
- `GET /__health`（DB ping + 连接池 + goroutine + 队列积压 + 物化滞后）
- `GET /__version`（git/构建时间/迁移版本）
- DB 层：所有查询加 `context` 超时（如 5s）+ `slow_query_total` 指标
- 索引审计脚本 + `access_log(ts_epoch)`（已）/`probe_log(ts_epoch)`
- 优雅停机：信号 → 停止接收 → 排空队列/审计 → 关连接
- **验收**：health 200 且含关键指标；慢查询可在 `/__metrics` 观测；重启不丢在途请求。

### Phase 1 — 读写分离 + 物化读模型（1–2 天）
- 物化表：`client_stage`（已）、`device_stats`（设备计数/进度/钱包数）、`endpoint_daily`（端点按天计数）
- `ListDevices` 改为「读 `device_stats`+`device_profiles`」，聚合全部后台增量
- 保留策略改**分区 + DROP PARTITION**（`access_log`/`probe_log` 按天）
- 检测/编排/回填全部走 worker 队列
- **验收**：`/panel/api/devices` P99 < 100ms；`/__dash/api` < 50ms；无 >1s 的 SQL。

### Phase 2 — 模块解耦（2–4 天）
- `store` 拆包：`db` / `ingest` / `query` / `capture` / `schema`（迁移）/ `wallet` / `notes`
- 引入 repo 接口；`server` 通过构造函数注入
- 迁移独立为 `mockc2 migrate`（up/down + dry-run）
- **验收**：`go build ./...`、`tools/smoke9001` 全绿、关键路径单测覆盖、无跨包全局单例。

### Phase 3 — 企业化运行时（2–3 天）
- systemd 硬化 + cgroup 限制 + 健康探针（配合 nginx `proxy_next_upstream` 与超时）
- 结构化日志（JSON）+ 请求 ID 贯穿
- 发布/回滚脚本 + 冒烟门禁（CI）
- 多实例一致性收尾（无状态会话已具；清理残留进程内缓存）
- **验收**：单命令发布/回滚；故障自愈（重启/切流）；72h 无人工干预。

---

## 5. 非目标与风险

- **不引入重型框架**：保持标准库为主（与现有风格一致）。
- **迁移期向后兼容**：旧表只增 migration、不回改；旧接口保留兼容层。
- **生产变更**：每次部署**备份二进制/前端 + 可回滚**（已具备 `*.bak-*`），先灰度后全量。
- **数据迁移**：`access_log` 历史大表分区需停机窗口或在线重建（`pt-online-schema-change` 风格），单独排期。

---

## 6. 立刻可做的 3 个安全项（Phase 0 子集）

1. `GET /__health` + `GET /__version`（无侵入，便于监控/探针）
2. DB 查询 `context` 超时 + 慢查询计数（防止慢 SQL 占满连接）
3. `access_log` / `probe_log` 索引与保留强化（杜绝再次全表扫）

> 建议：**先做这 3 项**（半天内，风险最低、收益最直接），再进入 Phase 1 物化读模型。
