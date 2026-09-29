# Mock C2 控制台 · 前端工程

> 后端（Go `mockc2` / Python FastAPI）的运维前端。Vue 3 + Vite + TypeScript + Element Plus，
> 深色科技 / 浅色企业双主题，信息架构按**业务实体**（入库解析后的语义）组织。
>
> 遵循后端契约（`boot-analysis/backend/DATA_CONTRACT.md` v2.0 + `FRONTEND_CONTRACT.md`）。

---

## 1. 快速开始

```bash
cd frontend
npm install
npm run dev        # http://127.0.0.1:5173（已代理后端，见 .env.local）
```

后端起法：

```bash
cd ../go && .\mockc2.exe --no-https --http-port 9100   # Go 版（推荐）：http://127.0.0.1:9100/admin
cd ../boot-analysis/backend && python run.py           # Python 版：http://127.0.0.1:9001/admin3389i
```

`.env.local`（默认已配好，指向 Go 版 9100）：

```ini
VITE_PROXY_TARGET=http://127.0.0.1:9100
```

构建与部署：

```bash
npm run build                    # 类型检查 + 构建
npm run deploy                   # 构建 + 同步到 ../go/www/admin
npm run sync -- --target all     # 同步到 Go + Python 两处静态目录
```

---

## 2. 信息架构（按业务实体，不按 capture 端点分组）

| 分组 | 视图 | 路由 | 数据源 |
|---|---|---|---|
| 运营 | 仪表台 | `#/dashboard` | `/__dash/api` + `/api/stats` + `/api/activity` + 队列总览 |
| 运营 | 设备列表 | `#/devices` | `/panel/api/devices`+`progress`，**可多选批量下发指令** |
| 运营 | 设备控制台 | `#/console` | `/__q/device_command_targets` + `/__q/device_commands` |
| 运营 | 访问统计 | `#/access` | `/panel/api/access`（全量审计）+ `/panel/api/visits`（来访去重） |
| 数据 | 备忘录 | `#/memos` | `/api/data/memos`（nb 解析入库） |
| 数据 | 钱包钥匙串 | `#/keychain` | `/api/data/wallets`（war keychain：设备→钱包→条目） |
| 数据 | 照片 | `#/photos` | `/api/data/photos` + `/api/file/{packet_id}` 预览/下载 |
| 数据 | 应用资产 | `#/apps` | `/panel/api/apps`（u 解析，分页检索） |
| 数据 | 日志 | `#/logs` | `/__dash/api` log 窗口 + 前端累积缓冲 |
| 系统 | 系统设置 | `#/settings` | 视图偏好 / 运行时 / 后端能力探测 / 接口地图 |

**为什么不再按端点分组**：端点（`/beacon /war /nb /ph /u …`）是传输层概念；
入库解析后应呈现业务实体（备忘录、钥匙串条目、照片、应用清单）。端点计数只作为仪表台的"回传量"统计维度保留。

---

## 3. 指令：批量异步、按设备独立队列

后端原模型是**单行全局队列**（`command_queue` id=1）：一条指令占住全局，任意设备领走才释放；
一台设备不心跳 → 后面全堵。

改造后（Go v7，新增 `device_commands` 表）：

- 每台设备一条 FIFO，`TakeFor([uuid, ip, '*'])` 按身份匹配领取
- 一台不在线**不影响**其它设备
- 一次请求可给 N 台各写一条（批量异步）：`POST /__control {"type","params","once","targets":[...]}`
- 可只取消某台：`POST /__control {"command":null,"target":"..."}`
- `command_id` 形如 `lab-<毫秒>-<PID>-<序号>`，跨 worker 不撞号
- 老语义完全兼容：不带 `target`/`targets` 仍写全局队列；beacon 先查设备队列，取不到再回落全局

前端降级：探测 `/__q/device_command_targets` 失败（404）→ `perDevice=false`，
设备控制台与批量弹窗显式提示"退化为全局单队列"，不会静默错语义。

---

## 4. 工程分层

```
src/
├── api/          契约层：types.ts + http.ts（拦截器/401→登录门）
│                 dash / query / panel / control / feapi 五个接口模块
├── stores/       Pinia：auth / dash(单一数据源轮询) / command(含按设备队列) / ui
├── components/   StatCard / EmptyState / JsonViewer / PacketDrawer / EChart / CommandDialog
├── layouts/      AdminLayout + Sidebar(分组) + Topbar
├── views/        10 个业务视图
├── utils/        format（展示格式化，不做时区换算）/ bus（事件总线，解耦循环依赖）
└── styles/       tokens.css（设计令牌 + 双主题）+ global.css
```

| 层 | 选型 |
|---|---|
| 框架 | Vue 3.5（`<script setup>` + TS） |
| 构建 | Vite 6（相对 base + hash 路由 + 手工分包） |
| UI | Element Plus 2.9（按需组件 + 中文语言包） |
| 状态 | Pinia |
| 路由 | Vue Router 4（hash：后端静态挂点无 SPA fallback，后端零改动） |
| 图表 | ECharts 5（按需引入 core + 4 种图表） |
| HTTP | axios（同源相对地址，Cookie 由浏览器托管） |

---

## 5. 契约红线（代码里已固化）

1. **只访问只读运维接口**：`/__dash/*`、`/__q/*`、`/panel/api/*`、`/api/{data,file,activity,stats,list,device,tasks}`、`/__control`、`/__auth/*`
2. **禁止 fetch 回传端点**：`/beacon /war /p /result /event /u /nb /ph /a`
3. **不代理交付链**：`/boot.html`、`/B10/*`、`/A8/*`、`/api/ip-sync/sync`
4. **指令只做三件事**：武装 / 清空 / peek；分发由各设备自己的 beacon 领走；`exit` 不下发
5. **不做时区换算**：`ts` 只切片展示（契约 §4.5）
6. **401 → 登录门**；5xx / 网络失败 → 顶栏红条 + 指数退避
7. **单一数据源**：全站唯一轮询点在 `stores/dash.ts`（单飞 + 可见性暂停），其它页面只做一次性拉取

---

## 6. 部署

产物为纯静态（`dist/index.html` + `dist/assets/*`）：

```bash
npm run deploy                      # -> ../go/www/admin（Go /admin）
npm run sync -- --target python     # -> ../boot-analysis/backend/frontend（/admin3389i）
npm run sync -- --target all
```

nginx 托管时把第 5 节白名单前缀 `proxy_pass` 到后端即可（会话是 HttpOnly Cookie，需同源或透传）。
