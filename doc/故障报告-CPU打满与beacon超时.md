> # ⛔ 本文档已作废 — 请勿据此修复
>
> **作废时间**：2026-09-29 01:20 EDT
> **作废原因**：本文 §2.4 声称的根因「SQLite `busy_timeout=0`」**是错误的**。
> `busy_timeout` 是连接级设置、不持久化到数据库文件；用 `sqlite3` CLI 新开连接读到的 `0`
> 只是 CLI 自身默认值，**与应用实际配置无关**。已用
> `PRAGMA busy_timeout=5000` → 重开连接 → 读回 `0` 的实验证实。
>
> 本文建议的「改单进程」「回收 WAL」「补 `db_max_conns`」三项措施**部分有效**
> （内存 1.7 GB→73 MB，WAL 218 MB→0），但**均未解决 CPU 打满**。
>
> **真实根因**：`ListDevices()` 的 N+1 查询 + **缺乏并发上限**，导致 76 个 goroutine
> 堆积在同一条调用链上，GC 被强制连续执行 3 分钟，形成正反馈死亡螺旋。
>
> 👉 **请以 `故障报告-后端高并发修复建议-企业级.md`（编号 INC-2026-0929-001）为准。**

---

<details>
<summary>⚠️ 以下为已作废内容，仅供追溯，勿据以修复</summary>

# MockC2 服务故障报告：CPU 打满 + `/beacon` 持续 504

| 项目 | 内容 |
|---|---|
| 报告时间 | 2026-09-29 01:05 EDT |
| 受影响服务 | `mockc2.service`（systemd） |
| 严重级别 | **P1 — 服务实质不可用** |
| 现象 | 两个 worker 子进程各占满 1 核（100%），`/beacon` 成功率 0% |
| 根因 | SQLite `busy_timeout=0` + 多进程共享同一 WAL 库 → 写锁忙等活锁（livelock） |

---

## 1. 现象摘要

1. `mockc2` 的两个 worker 子进程持续各占满一个 CPU 核，已连续 **3 小时 50 分钟**。
2. systemd `CPUQuota=200%` 被持续打满，**98.06% 的调度周期被限流**。
3. `POST /beacon` 几乎全部返回 **504**，接口实质不可用。
4. 内存并非泄漏（RSS 在 690–920 MB 之间震荡），磁盘占用正常（24%）。

---

## 2. 关键证据

### 2.1 CPU 与限流

```
PID     USER   %CPU  ELAPSED   COMMAND
1960083 mockc2  99.5  03:50:06  mockc2 --worker-child --no-https --http-port 9101
1960084 mockc2  99.5  03:50:06  mockc2 --worker-child --no-https --http-port 9102
1960076 mockc2   0.1  03:50:06  mockc2 --workers 2 --no-https   ← LB 父进程，本身正常
```

cgroup 限流统计（`/sys/fs/cgroup/system.slice/mockc2.service/cpu.stat`）：

```
nr_periods     139560
nr_throttled   136842      → 限流率 98.06%
throttled_usec 21896523488 → 累计被限流 21896 秒
```

**这就是你看到的「一直溢出」**——`CPUQuota=200%` 几乎每个调度周期都被触发。

### 2.2 负载极低，排除容量问题

`POST /beacon` 每分钟请求数：

```
00:55  27 次
00:56  28 次
00:57  24 次
00:58  24 次
00:59  23 次
01:00  30 次
01:01  29 次
```

约 **25–30 次/分钟（0.45 req/s）**。用 2 个 CPU 核去处理 0.45 req/s，属于完全不可能的正常开销。
**结论：这不是流量压垮的，是代码空转。**

### 2.3 `/beacon` 失败率

最近 5000 条 `/beacon` 请求的状态码分布：

```
4912  504   (98.2%)   ← nginx 等 60 秒后 upstream 超时
  88  499   (1.8%)    ← 客户端主动断开
   0  200
```

对照组：同时段的 `POST /event` **100% 返回 200**。
`/event` 正常而 `/beacon` 全挂，说明问题出在 `/beacon` 的**写库路径**上，而非 HTTP 层或网络层。

### 2.4 根因：SQLite busy_timeout = 0

```
$ sqlite3 "file:/opt/mockc2/data/c2mock.db?mode=ro" "PRAGMA busy_timeout;"
0                       ← 致命
$ ... "PRAGMA journal_mode;"
wal
$ ... "PRAGMA wal_autocheckpoint;"
1000
```

`busy_timeout=0` 意味着：一旦遇到写锁冲突，SQLite **立即返回 `SQLITE_BUSY`，不等待、不退避**。
应用层若对 `SQLITE_BUSY` 做 `for { retry() }` 重试且中间无 sleep/deadline，就是一个**纯忙等死循环**。

### 2.5 佐证：连接泄漏与 WAL 膨胀

```
9101/9102 连接状态：
  66 ESTAB        39 CLOSE-WAIT     34 FIN-WAIT-2
                                ↑              ↑
                    收到 FIN 不关闭   客户端已 FIN，服务端不收
                    → handler goroutine 永久卡死

data/c2mock.db-wal   216 MB，持续增长中（2.9 KB/s，checkpoint 饥饿）
进程 RSS             690–920 MB 反复震荡（Go GC churn，非单调泄漏）
系统态 CPU           24.6%（远高于正常值，典型的 fcntl/flock 系统调用风暴）
```

CPU 分布不是单个线程失控，而是分散在多个线程（各累计 20–35 分钟 CPU 时间），且 `sy` 占比高——
这正是**多进程在同一 SQLite 上互相抢 fcntl 文件锁**的典型特征。

### 2.6 完整因果链

```
--workers 2 启动 2 个独立进程，共享同一份 c2mock.db / -wal / -shm
        │
        ├─ LB 父进程(1960076) + 2 个 worker(1960083/1960084) = 3 个写入方
        │
        ▼
SQLite WAL 模式下写锁互斥；busy_timeout=0 → 冲突时立即返回 SQLITE_BUSY
        │
        ▼
应用层无退避重试 → 三个进程互相忙等 → 100% CPU 空转
        │
        ▼
事务永远提交不进去 → /beacon handler 永不返回
        │
        ▼
nginx 未配 proxy_read_timeout（默认 60s）→ 等满 60 秒 → 504
        │
        ▼
客户端收到 504 后重试 → 请求堆积在 60 秒窗口内 → 进一步加剧
        │
        ▼
CPUQuota=200% 被打满 → 98% 周期被限流 → 全站（含 /__dash 管理台）一起变慢
```

---

## 3. 加重问题的配置问题

### 3.1 `mockc2_settings.json` 缺失 `db_max_conns`

`/opt/mockc2/README.md` 文档中列出了该配置项（默认 `8`），但实际配置文件里**没有这一行**：

```json
{
  "host": "0.0.0.0",
  "http_port": 9100,
  "https_port": 9443,
  "admin_password": "CHANGE-ME-strong-password",   ← 见 3.3
  "admin_session_ttl": 43200,
  "ops_token": "",
  "server_header": "nginx/1.22.0"
}
```

结果：3 个进程 × 默认 8 连接 = 24 个连接同时争抢同一个库锁，进一步放大竞争。

### 3.2 nginx 站点未设置任何超时与 upstream 策略

`/www/server/panel/vhost/nginx/test.dhropjayk57qg3g.lat.conf` 第 42 行仅有一行：

```nginx
proxy_pass http://127.0.0.1:9100;
```

缺少 `proxy_read_timeout`（因此退化成 60s 默认值）、`proxy_connect_timeout`、
`proxy_next_upstream`、`keepalive`。后端卡死时，每个请求都要占住连接 60 秒不退，
连接数与 goroutine 数持续累积（已观测到 39 CLOSE-WAIT / 34 FIN-WAIT-2）。

### 3.3 管理口令仍是占位符

`admin_password` 为字面量 `CHANGE-ME-strong-password`，README 明确要求「部署后立即改」。

### 3.4 WAL 从不回收

`c2mock.db-wal` 已 216 MB。`wal_autocheckpoint=1000` 是默认值，但只要存在常驻读事务，
checkpoint 就永远等不到时机，WAL 无限增长。当前增速 2.9 KB/s（≈250 MB/天）。

---

## 4. 处置建议

### 4.1 立即止血（不��代码，5 分钟内可恢复）

```bash
# 1) 重启服务——当前处于活锁状态，重启可立即恢复服务（但会复发）
systemctl restart mockc2

# 2) 确认恢复
systemctl show mockc2 -p NRestarts
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:9100/boot.html?c=1   # 期望 200
```

### 4.2 短期规避（改配置，立刻降低复发概率）

**A. 关闭多 worker，改单进程**（SQLite 多进程共享是本次根因，规避成本最低）：

```ini
# /etc/systemd/system/mockc2.service
ExecStart=/opt/mockc2/mockc2 --no-https      # 去掉 --workers 2
```

单进程 Go 原生并发足以支撑当前 0.45 req/s 的负载，且彻底消除跨进程锁竞争。

**B. 补齐 `mockc2_settings.json`**：

```json
{
  "host": "0.0.0.0",
  "http_port": 9100,
  "https_port": 9443,
  "admin_password": "<改成真实强口令>",
  "admin_session_ttl": 43200,
  "ops_token": "",
  "server_header": "nginx/1.22.0",
  "db_max_conns": 2
}
```

**C. nginx 加超时与故障切换**：

```nginx
location / {
    proxy_pass http://127.0.0.1:9100;
    proxy_connect_timeout 3s;
    proxy_read_timeout    10s;                    # 原为默认 60s
    proxy_next_upstream error timeout http_502 http_504;
    proxy_set_header Host              $host;      # 勿删：后端按 Host 分域
    proxy_set_header X-Forwarded-For  $proxy_add_x_forwarded_for;   # 勿删
}
```

### 4.3 长期修复（需改代码）

1. **写入路径必须带退避**：所有 SQLite 写操作设置 `PRAGMA busy_timeout=5000`（毫秒），
   或在应用层用带 sleep 的指数退避重试，并设总 deadline。**绝不允许无 sleep 的忙等重试。**
2. **不要跨进程共享 SQLite**：要么回到单进程，要么每个 worker 用独立 DB 文件。
   如果必须共享，退回 `DELETE` journal 模式 + 单写者架构。
3. **手动回收 WAL**（服务停止后执行）：

   ```bash
   systemctl stop mockc2
   sqlite3 /opt/mockc2/data/c2mock.db "PRAGMA wal_checkpoint(TRUNCATE);"
   systemctl start mockc2
   ```

4. **加入可观测性**：二进制当前**未编译 pprof**（`strings` 验证无 `net/http/pprof` 路径），
   导致本次故障无法直接抓 goroutine 栈、只能靠旁证推断。建议重新构建时加 `-tags netgo`
   并导入 pprof，或至少暴露 `/__health` 显示各 worker 在途请求数与锁等待计数。

---

## 5. 排查过程与局限

**已排除的可能：**

| 假设 | 排除依据 |
|---|---|
| 内存泄漏 / OOM | `dmesg` 无 OOM 记录；RSS 690–920MB 震荡非单调；MemAvailable 10.5 GB |
| 磁盘写满 | 根分区 24% 使用率，剩余 56 GB |
| 流量洪峰 | `/beacon` 仅 25–30 次/分钟 |
| 网络 / Cloudflare 层问题 | `/event` 走同一条链路 100% 返回 200 |
| 进程崩溃重启 | `NRestarts=0`，自 21:10 起持续运行 |

**局限：** 二进制未编译 pprof，无法在不终止进程的前提下抓取 goroutine 栈。
上述根因由 `PRAGMA busy_timeout=0` 的直接读数 + CPU/连接/WAL/状态码四类旁证交叉推断得出，
置信度高但非直接栈证据。**若按 4.2 规避后 CPU 仍打满，请执行 `kill -QUIT <worker-pid>`
抓取 goroutine 栈（会终止该进程），据此进一步定位。**

---

*报告生成时间：2026-09-29 01:05 EDT | 采集主机：server-ozno.novalocal (4 核 / 16 GB)*


---

</details>
