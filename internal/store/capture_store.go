package store

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// CaptureStore 审计存储服务：请求体留存 + JSONL 流水，线程安全。
type CaptureStore struct {
	root           string
	mu             sync.Mutex
	recentCapacity int
	recent         map[string][]map[string]any
	observers      []func(endpoint, relPath string, size int, raw []byte)
	handles        map[string]*os.File // JSONL 持久句柄（避免每请求 open/close）
}

// internalFolders 内部派生目录（chunk_store 重组产物等），不属于回传端点面。
var internalFolders = map[string]bool{"files": true, ".parts": true}

// NewCaptureStore 构造。
func NewCaptureStore(root string) *CaptureStore {
	_ = ensureDir(root)
	return &CaptureStore{
		root:           root,
		recentCapacity: 100,
		recent:         map[string][]map[string]any{},
		handles:        map[string]*os.File{},
	}
}

// AddObserver 观察者签名 fn(endpoint, relPath, size, raw)；异常不影响主链。
func (c *CaptureStore) AddObserver(fn func(endpoint, relPath string, size int, raw []byte)) {
	c.observers = append(c.observers, fn)
}

// WriteJSONL 结构化流水追加（复用持久句柄）。
func (c *CaptureStore) WriteJSONL(name string, obj any) {
	line, err := json.Marshal(obj)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.handleLocked(name)
	if f == nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}

// WriteJSONLLines 批量追加已序列化行（异步批量落盘用）。
func (c *CaptureStore) WriteJSONLLines(name string, lines [][]byte) {
	if len(lines) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.handleLocked(name)
	if f == nil {
		return
	}
	buf := make([]byte, 0, len(lines)*64)
	for _, ln := range lines {
		buf = append(buf, ln...)
		buf = append(buf, '\n')
	}
	_, _ = f.Write(buf)
}

func (c *CaptureStore) handleLocked(name string) *os.File {
	if f, ok := c.handles[name]; ok {
		return f
	}
	f, err := os.OpenFile(filepath.Join(c.root, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	c.handles[name] = f
	return f
}

// CloseHandles 关闭所有持久句柄（进程退出前调用，best-effort）。
func (c *CaptureStore) CloseHandles() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, f := range c.handles {
		_ = f.Close()
		delete(c.handles, name)
	}
}

// SaveBody 原始请求体留存，返回落盘路径。
func (c *CaptureStore) SaveBody(endpoint string, data []byte, ext string, limit int) string {
	folder := filepath.Join(c.root, endpoint)
	_ = ensureDir(folder)
	if limit > 0 && len(data) > limit {
		data = data[:limit]
	}
	ts := time.Now().Format("20060102-150405")
	if ext == "" {
		ext = "bin"
	}
	name := ts + "-" + randomHex(8) + "." + ext
	path := filepath.Join(folder, name)

	c.mu.Lock()
	_ = os.WriteFile(path, data, 0o644)
	c.appendRecentLocked(endpoint, name, data)
	c.mu.Unlock()

	rel := endpoint + "/" + name
	for _, fn := range c.observers {
		func() {
			defer func() { _ = recover() }()
			fn(endpoint, rel, len(data), data)
		}()
	}
	return path
}

// DeleteBody 删除一份已落盘回传体（相对路径如 "sync/xx.json" 或绝对路径）。
// 供重复体去重使用（同指纹回传只在首见保留正文）。
func (c *CaptureStore) DeleteBody(rel string) {
	if c == nil || rel == "" {
		return
	}
	p := filepath.FromSlash(rel)
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.root, p)
	}
	_ = os.Remove(p)
}

func (c *CaptureStore) appendRecentLocked(endpoint, fname string, data []byte) {
	bucket := c.recent[endpoint]
	bucket = append(bucket, map[string]any{
		"ts":   nowStr(),
		"file": fname,
		"size": len(data),
		"body": preview(data),
	})
	if len(bucket) > c.recentCapacity {
		bucket = bucket[len(bucket)-c.recentCapacity:]
	}
	c.recent[endpoint] = bucket
}

// Recent 看板直接读内存，不扫盘。
func (c *CaptureStore) Recent(endpoint string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	src := c.recent[endpoint]
	out := make([]map[string]any, len(src))
	copy(out, src)
	return out
}

// Folders 列出根下端点目录（排除内部派生目录），排序。
func (c *CaptureStore) Folders() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return []string{}
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || internalFolders[e.Name()] {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// FileEntry 文件条目。
type FileEntry struct {
	Rel  string
	Size int64
}

// IterFiles 遍历留存文件。
func (c *CaptureStore) IterFiles() []FileEntry {
	var out []FileEntry
	_ = filepath.WalkDir(c.root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(c.root, path)
		if rerr != nil {
			return nil
		}
		info, ierr := d.Info()
		var size int64
		if ierr == nil {
			size = info.Size()
		}
		out = append(out, FileEntry{Rel: filepath.ToSlash(rel), Size: size})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out
}

// preview 请求体预览：JSON 尽量 parse；二进制留 hex 前缀 + 长度提示。
func preview(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if isUTF8(data) {
		var v any
		if json.Unmarshal(data, &v) == nil {
			if b, err := json.Marshal(v); err == nil {
				return string(b)
			}
		}
		return string(data)
	}
	head := data
	if len(head) > 64 {
		head = head[:64]
	}
	return "<binary " + strconv.Itoa(len(data)) + " bytes> " + hex.EncodeToString(head)
}

// ── 单例 ──
var (
	storeMu     sync.Mutex
	globalStore *CaptureStore
)

// InitStore 初始化全局留存。
func InitStore(root string) {
	storeMu.Lock()
	defer storeMu.Unlock()
	globalStore = NewCaptureStore(root)
}

// GetStore 返回全局留存。
func GetStore() *CaptureStore {
	storeMu.Lock()
	defer storeMu.Unlock()
	return globalStore
}
