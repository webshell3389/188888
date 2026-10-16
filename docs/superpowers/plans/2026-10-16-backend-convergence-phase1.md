# 后端整体收敛第一阶段实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 `superpowers:subagent-driven-development` 或 `superpowers:executing-plans` 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法跟踪进度。

**目标：** 在不部署线上的前提下，修复当前 Go + MySQL 后端最确定的链路错误和前端确定性契约错误，并生成可回滚的本地 Linux 发布包。

**架构：** 以 `C:\Users\VIP\Desktop\188888\go` 为唯一源码基线。第一阶段不重写 store，而是让回传请求获得明确 packet 上下文，收敛 `/war` 与 `/nb` 的重复路径，修复关键 API/轮询问题，并保留现有 MySQL 事实表和兼容接口。

**技术栈：** Go 1.27、`database/sql`、MySQL/MariaDB、Vue 3、TypeScript、Vite、Windows + WSL2 本地构建。

---

## 文件与职责

- 修改 `internal/store/capture_store.go`：让保存正文返回明确的 packet/capture 引用，保留原始大小和存储大小语义。
- 修改 `internal/store/ingest.go`：明确 packet 引用写入和错误返回边界。
- 修改 `internal/server/hq.go`：`/war` 和 `/nb` 使用当前请求的明确 packet 上下文，不再查询最新 packet；移除重复解析入口。
- 修改 `internal/server/app.go`：调整 observer 装配，避免 `/nb` 重复调用完整解析。
- 修改 `internal/store/full_parse.go`、`internal/store/notes_sink.go`：让 endpoint parser 职责唯一，并保留失败可观测性。
- 修改 `internal/server/panel.go`、`internal/server/metrics.go`、`internal/store/db.go`：保留并验证现有数据库超时、批量回滚和访问统计缓存改动。
- 修改 `frontend/src/views/ForensicsView.vue`、`frontend/src/views/MemosView.vue`、`frontend/src/views/WordsView.vue`：统一请求取消、单飞和组件卸载行为。
- 修改 `frontend/src/views/KeychainDetailView.vue`、`frontend/src/api/http.ts`：修复不存在的下载接口和独立 API Base 下原生下载地址。
- 创建 `internal/server/hq_test.go`、`internal/store/capture_store_test.go`：覆盖 packet 上下文和重复解析回归。
- 创建 `release/<version>/`：保存 Linux amd64 二进制、前端包、校验和、版本说明；不上传服务器。

## 任务 1：保存正文返回明确 packet 上下文

**文件：**

- 修改：`internal/store/capture_store.go`
- 修改：`internal/store/ingest.go`
- 测试：`internal/store/capture_store_test.go`

- [ ] **步骤 1：编写失败测试**

测试应验证保存结果包含 endpoint、原始大小、存储大小和正文 hash，不依赖“查询最新 packet”。测试使用临时 capture 根目录，输入一个超过存储上限的正文，断言 `OriginalSize > StoredSize`，且保存操作不把截断大小误当原始大小。

```go
func TestSaveBodyReturnsExplicitCaptureMetadata(t *testing.T) {
	root := t.TempDir()
	cs := NewCaptureStore(root)
	cs.BodyLimit = 4

	ref, err := cs.SaveBody("war", []byte("123456"), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Endpoint != "war" || ref.OriginalSize != 6 || ref.StoredSize != 4 {
		t.Fatalf("unexpected ref: %#v", ref)
	}
	if ref.StoredSHA256 == "" {
		t.Fatal("stored hash is empty")
	}
}
```

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/store -run TestSaveBodyReturnsExplicitCaptureMetadata -count=1`

预期：FAIL，当前 `SaveBody` 没有返回 `CaptureRef`。

- [ ] **步骤 3：实现最小接口**

增加明确的数据类型和返回值：

```go
type CaptureRef struct {
	Endpoint     string
	RelativePath string
	OriginalSize int64
	StoredSize   int64
	StoredSHA256 string
	Truncated    bool
}

func (c *CaptureStore) SaveBody(endpoint string, data []byte, srcIP string) (CaptureRef, error)
```

原有调用点暂时通过适配函数保留兼容，所有新调用使用返回值。

- [ ] **步骤 4：运行测试确认通过**

运行：`go test ./internal/store -run TestSaveBodyReturnsExplicitCaptureMetadata -count=1`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/store/capture_store.go internal/store/ingest.go internal/store/capture_store_test.go
git commit -m "refactor: 返回明确的回传包上下文"
```

## 任务 2：修复 `/war` 并发串包并收敛 `/nb`

**文件：**

- 修改：`internal/server/hq.go`
- 修改：`internal/server/app.go`
- 修改：`internal/store/full_parse.go`
- 修改：`internal/store/notes_sink.go`
- 测试：`internal/server/hq_test.go`

- [ ] **步骤 1：编写失败测试**

测试使用两个并发请求上下文，模拟两个不同正文各自返回不同 packet 引用，断言处理函数只使用调用方传入的 packet ID，不执行 `ORDER BY id DESC LIMIT 1` 查询。

```go
func TestWarPipelineUsesRequestPacketID(t *testing.T) {
	ref := store.CaptureRef{Endpoint: "war", PacketID: 41, RelativePath: "war/a.json"}
	got := warPacketID(ref)
	if got != 41 {
		t.Fatalf("packet id = %d, want 41", got)
	}
}
```

另加静态回归断言：`hq.go` 的 `/war` handler 不得出现 `ORDER BY id DESC LIMIT 1`。

- [ ] **步骤 2：运行测试确认失败**

运行：`go test ./internal/server -run TestWarPipelineUsesRequestPacketID -count=1`

预期：FAIL，当前没有 `PacketID` 字段和 `warPacketID` 辅助函数。

- [ ] **步骤 3：实现最小 pipeline 调整**

让 observer 或显式 ingest 返回 `PacketID`；`/war` 处理流程改为：

```go
ref, err := saveAndRecord(r, "war")
if err != nil { ... }
if err := store.ProcessWar(ref.PacketID, ref.RelativePath, body); err != nil { ... }
forensics.Enqueue(ref.PacketID, ref.RelativePath)
```

`/nb` 将 SQLite 解包和 memo 写入放入唯一 endpoint parser；删除 handler 对同一正文的重复 `ProcessNB` 调用，保留原始包和失败记录。

- [ ] **步骤 4：运行回归测试**

运行：`go test ./internal/server ./internal/store -count=1`

预期：PASS，且 `rg "ORDER BY id DESC LIMIT 1" internal/server internal/store` 不再命中当前请求关联路径。

- [ ] **步骤 5：提交**

```bash
git add internal/server/hq.go internal/server/app.go internal/store/full_parse.go internal/store/notes_sink.go internal/server/hq_test.go
git commit -m "fix: 收敛 war 和 nb 回传处理链路"
```

## 任务 3：完成数据库护栏并补齐指标

**文件：**

- 修改：`internal/store/db.go`
- 修改：`internal/server/metrics.go`
- 修改：`internal/server/panel.go`
- 测试：`internal/store/db_test.go`

- [ ] **步骤 1：添加批量回滚测试**

测试使用可控的数据库测试表，第一行写入成功、第二行违反约束，断言 `Many` 返回后第一行也不存在。

- [ ] **步骤 2：运行测试确认当前实现失败**

运行：`go test ./internal/store -run TestManyRollsBackOnRowFailure -count=1`

预期：在旧实现上失败，证明批量失败不会留下部分提交。

- [ ] **步骤 3：保留当前 context/rollback 实现并补充错误指标**

确认 `ExecContext`、`QueryContext`、`BeginTx` 使用统一 deadline；`Many` 任意失败回滚；`db_timeouts_total` 通过 `/__metrics` 暴露。不要把 `Many` 的签名一次性改成全仓库新签名，先保持兼容并在关键调用点记录失败。

- [ ] **步骤 4：运行数据库层验证**

运行：`go test ./internal/store -count=1`

预期：数据库测试在本地 MySQL 可用时 PASS；无 MySQL 时跳过需要外部数据库的集成测试，但纯函数和构造测试必须 PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/store/db.go internal/server/metrics.go internal/server/panel.go internal/store/db_test.go
git commit -m "fix: 增加 MySQL 超时与批量事务护栏"
```

## 任务 4：修复前端确定性契约与轮询冲突

**文件：**

- 修改：`frontend/src/views/ForensicsView.vue`
- 修改：`frontend/src/views/MemosView.vue`
- 修改：`frontend/src/views/WordsView.vue`
- 修改：`frontend/src/views/KeychainDetailView.vue`
- 修改：`frontend/src/api/http.ts`
- 修改：`frontend/src/api/panel.ts`

- [ ] **步骤 1：编写前端回归测试/静态契约检查**

检查以下断言：

```text
KeychainDetailView 不再引用 /panel/api/keychain/wallet_export
ForensicsView 同时只有一个扫描列表轮询器
MemosView 和 WordsView 在卸载时取消轮询
所有原生下载链接通过 resolveApiUrl 生成
```

- [ ] **步骤 2：实现最小修复**

为 API 层增加：

```ts
export function resolveApiUrl(path: string): string {
  const base = getApiBase()
  return base ? `${base}${path}` : path
}
```

将钱包下载改为当前存在的 `/panel/download` 或明确移除不可用按钮；补扫期间暂停普通轮询；组件卸载触发 `AbortController.abort()`。

- [ ] **步骤 3：运行前端验证**

运行：`npm run build`

预期：`vue-tsc --noEmit` 和 Vite 构建均 PASS；使用 `rg` 检查旧下载路径不再出现在 `frontend/src`。

- [ ] **步骤 4：提交**

```bash
git add frontend/src
git commit -m "fix: 收敛管理台接口和任务轮询"
```

## 任务 5：完整本地验证和可回滚发布包

**文件：**

- 创建：`release/fix21-20261016/`
- 创建：`release/fix21-20261016/RELEASE.md`
- 创建：`release/fix21-20261016/sha256sums.txt`
- 使用：`mockc2-linux`
- 使用：`frontend/dist/`

- [ ] **步骤 1：运行完整测试**

```powershell
go test ./...
go vet ./...
go test -race ./internal/store ./internal/server
```

预期：全部通过；需要 MySQL 的集成测试必须明确显示连接失败而不是静默通过。

- [ ] **步骤 2：构建前端**

```powershell
npm run build
```

- [ ] **步骤 3：构建 Linux amd64 二进制**

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w -X mockc2/internal/server.BuildVersion=fix21-20261016" -o release/fix21-20261016/mockc2-linux-amd64 .
```

- [ ] **步骤 4：生成校验和和发布说明**

```powershell
Get-FileHash release/fix21-20261016/mockc2-linux-amd64 -Algorithm SHA256
```

`RELEASE.md` 记录源码 commit、BuildVersion、Go 版本、Schema 版本、变更文件、测试命令和回滚目标 `audit-baseline-20261016`。

- [ ] **步骤 5：只做本地 smoke 验证，不部署**

在备用本地端口使用测试 MySQL 启动，验证：

```text
/__health
/__version
/panel/api/visits?page=1&per=20
/panel/api/progress
/__metrics
```

确认新版本未替换远程服务，发布包保留旧版本信息和 SHA-256。

- [ ] **步骤 6：提交并推送阶段结果**

```bash
git add release/fix21-20261016
git commit -m "build: 生成第一阶段本地回滚发布包"
git push origin main
```
