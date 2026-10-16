# Go 后端整体收敛设计

## 目标

把当前多阶段演进形成的 Go + MySQL 后端收敛为一套可解释、可观测、可回滚的运行模型，优先解决数据链路重复、统计口径冲突、多 worker 后台任务重复执行和线上版本不可确认问题。

本设计以以下唯一基线为准：

- 源码：`C:\Users\VIP\Desktop\188888\go`
- 线上对应产物候选：`C:\Users\VIP\Desktop\188888\go\mockc2-linux`
- 版本：`fix20-20261004-1942-rpt`
- 存储：MySQL/MariaDB；设备上传的 `NoteStore.sqlite` 仅作为输入文件解析格式，不作为后端主数据库。

## 非目标

- 不在第一阶段重写整个 `internal/store`。
- 不立即改变前端所有页面的视觉和业务功能。
- 不直接在线上修改数据库结构或替换二进制。
- 不把 `go-work_bak`、WSL 旧 SQLite 副本或 `trigger.translatevv.com/mockc2-go` 混入当前实现。

## 目标运行模型

```text
HTTP 接入
  -> 请求上下文与大小限制
  -> 单一 ingest pipeline
  -> 原始留存 + MySQL 事实表 + 可重建读模型
  -> 持久任务表/队列
  -> 有租约的后台 worker
  -> 统一分页/摘要 API
```

HTTP 请求不再通过“查询最新一行”推断当前 packet，也不在 handler 和 observer 中重复执行业务解析。所有异步任务必须有持久状态、领取者、租约、幂等键和可观测终态。

## 事实模型

### 设备

- `devices`：访客台账，记录 IP、UA、版本、域名和来访时间。
- `device_profiles`：设备自报或服务端观测的身份档案。
- `device_stats`：按 UUID 的物化读模型，保存最近时间、各类计数和在线所需字段。
- `packet_meta`/`packets`：原始接收事实，不作为每次请求的全量聚合来源。

新接口的设备数、在线数和设备列表统一由读模型服务提供；兼容接口可以继续存在，但必须标记为兼容口径并覆盖测试。

### 钱包与取证

- `keychain_items`：解析出的钥匙串条目事实。
- `wallet_seen`：钱包规则命中事件/快照，必须明确来源字段。
- `device_wallet_flags`：设备是否命中钱包的派生读模型。
- `war_scan`：取证扫描结果，不直接替代钥匙串事实。

设备列表展示“有钱包”使用 `device_wallet_flags`；钱包明细使用 `keychain_items`；扫描统计使用 `war_scan`。接口字段命名区分 `wallet_count`、`wallet_hits`、`keychain_items`，不再复用含义模糊的 `count`。

### 指令

- `device_commands`：新的按设备 FIFO 指令事实和状态机。
- `commands`：兼容历史审计记录，逐步停止作为新页面统计来源。
- `command_queue`：旧单槽 mailbox，仅保留兼容读取/回退，不再作为新功能默认写入。

所有新控制入口统一写 `device_commands`，通过 `command_id` 和业务幂等键防止重复创建。统计统一读取 `device_commands`，并在兼容接口中明确标注旧口径。

## Ingest Pipeline

### 统一流程

每个回传请求在 handler 内创建请求上下文，保存正文后得到明确的 `PacketRef`：

```text
SaveBody -> PacketRef(packet_id, endpoint, original_size, stored_size, hash)
        -> RecordPacket(PacketRef)
        -> endpoint parser
        -> derived read-model updates
        -> persistent background jobs
```

`SaveBody`/`RecordPacket` 必须返回 packet ID，禁止通过 `ORDER BY id DESC LIMIT 1` 关联当前请求。

### `/nb`

`/nb` 的 SQLite 解包、正文提取和结构化入库作为同一 pipeline 的显式步骤执行。observer 不再重复调用第二套 `ProcessFull("nb")`；解析失败写入结构化失败事件并保留原始 packet，后续可重试。

### `/war`

`/war` 使用同一 `PacketRef` 触发钱包事实写入和取证任务入队。取证只入持久任务表，由 worker 原子领取；扫描结果以 `packet_id` 建立唯一关联，报告文件以 packet ID 或内容 hash 命名并原子替换。

### 请求体大小与截断

保存以下字段：

- `original_size`：原始请求大小。
- `stored_size`：实际保存大小。
- `original_sha256`：完整正文 hash；若未读取完整则记录状态。
- `stored_sha256`：保存内容 hash。

展示截断不影响原始事实。超过策略上限的请求进入明确的 `truncated` 状态，不以截断内容冒充完整数据。

## 后台任务模型

所有可跨进程执行的任务统一使用状态：

```text
pending -> running -> succeeded
                  -> failed
                  -> retryable
```

必要字段：`job_id`、`kind`、`dedupe_key`、`payload`、`attempt`、`worker_id`、`lease_until`、`started_at`、`finished_at`、`error`。

领取必须是数据库原子条件更新。租约过期后允许重新领取。任务写入和业务状态推进必须检查错误；失败不能提前标记为 done。

适用范围：

- orchestrator 延迟动作。
- forensics war 扫描。
- balance 查询。
- backfill 和读模型重建。
- retention 清理。

Telegram 在多 worker 模式下只允许运行一个实例；第一阶段采用配置约束或父进程单独启动，后续再考虑数据库选主。

## Worker 生命周期

`InitServices` 收敛为显式生命周期对象，所有 worker 接受 `context.Context` 并返回停止函数或由统一 `ServiceGroup` 管理。

关闭顺序：

1. 停止接受新 HTTP 请求。
2. 停止任务生产者和 observer 派发。
3. 停止领取新任务。
4. 排空审计和已领取任务，等待有限时间。
5. 取消外部进程和网络请求。
6. 关闭文件句柄和 MySQL 连接池。

重复初始化必须返回已有服务或明确错误；observer 注册返回注销函数，并使用锁保护 observer 快照。

## 数据库访问与连接池

- 所有运行时 Query/Exec/Tx 使用 context。
- HTTP 查询默认有短超时；后台任务使用独立超时。
- 关键写入返回 `error`，不再用 `0` 和空切片同时表示失败/无数据。
- `Many` 任意失败回滚整批；需要容错时显式设计逐项状态。
- 连接池按 worker 数量计算预算，后台重任务使用并发信号量。
- 指标包含 query/exec/error/timeout、pool wait、慢查询和各 worker 队列深度。

## API 收敛

### 当前页面使用的主接口

- `/__health`、`/__version`：运行状态和版本。
- `/__dash/summary` 或现有 `/__dash/api` 的轻量化版本：仪表台摘要。
- `/panel/api/devices`：设备分页。
- `/panel/api/visits`：访问分页。
- `/panel/api/*`：业务实体分页和详情。
- `/__q/*`：指令查询。
- `/__control`：指令控制。

`/api/*` 仅作为兼容层，不再由新页面新增依赖。全局看板不再在所有页面持续拉取重型全量快照；页面轮询统一由 store 管理，任务轮询统一由 task store 管理。

## 发布与回滚

每次本地完成后生成不可变发布目录：

```text
release/<version>/
  mockc2-linux-amd64
  adminui.tar.gz
  sha256sums.txt
  mockc2.service
  mockc2_settings.example.json
  RELEASE.md
```

构建必须注入 `BuildVersion`，并记录 Go 版本、源码 commit、Schema 版本和 SHA-256。服务器发布使用临时文件、健康检查和旧版本目录保留；失败自动恢复旧二进制和前端资源。未获得明确部署确认前，只在本地构建和验证。

## 分阶段验收

### 阶段一：确定性错误和性能护栏

- 修复 `/war` packet 关联。
- 收敛 `/nb` 重复解析。
- 保留 DB context、批量回滚和访问页优化。
- 修复前端已知 404 下载接口和请求轮询冲突。
- `go test ./...`、`go vet ./...`、前端构建通过。

### 阶段二：任务可靠性

- 持久任务表与原子领取。
- orchestrator/forensics/balance/backfill/retention 迁移到租约模型。
- worker 生命周期和优雅停机测试。

### 阶段三：口径收敛与读模型

- 统一设备、钱包、指令统计服务。
- 看板轻量摘要和分页接口。
- 读模型校准/重建工具。

### 阶段四：清理与发布治理

- 清理旧接口、旧前端、SQLite 后端残留说明。
- 固化唯一源码和发布目录。
- 本地构建 Linux 二进制、生成校验和回滚包，再安排线上部署。
