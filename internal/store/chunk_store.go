package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IDLE_SECS 空闲阈值：一个归并组 10s 无新增 chunk 就尝试重组（即便 partial）。
const IDLE_SECS = 10.0

const partsDirname = ".parts"

var unsafeFnameRe = regexp.MustCompile(`[^\w.\-+()\[\] ]`)

// safeFname 文件名白名单清洗：只留最后一段可打印字符，防止路径穿越。
func safeFname(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = unsafeFnameRe.ReplaceAllString(name, "_")
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unnamed"
	}
	if len(name) > 128 {
		name = name[:128]
	}
	return name
}

// ChunkStore 分块文件归并/重组服务（磁盘驱动，多实例共享）。
//
// 与 Python 版差异：归并态不再放进程内 map，而是以 capture/.parts/<cid>/
// 下的 part 文件 + meta.json 为唯一真源。任意实例写入的 part 对全部实例可见，
// 重组用 O_EXCL 锁文件跨进程互斥。因此 N 个 worker 可共同接收/重组同一大文件。
type ChunkStore struct {
	root string
	mu   sync.Mutex // 仅保护本进程内的重组互斥（跨进程靠锁文件）
}

// NewChunkStore 构造。
func NewChunkStore(root string) *ChunkStore {
	_ = ensureDir(filepath.Join(root, "files"))
	_ = ensureDir(filepath.Join(root, partsDirname))
	return &ChunkStore{root: root}
}

// ProcessResult 识别 body 是否为分块/单文件回传包，归并并按需重组。
func (c *ChunkStore) ProcessResult(body []byte, srcPath string) map[string]any {
	c.SweepIdle()
	info := map[string]any{"src": srcPath, "kind": "ignored"}

	var raw any
	parsed := false
	var j map[string]any
	if json.Unmarshal(body, &raw) == nil {
		if m, ok := raw.(map[string]any); ok {
			j = m
			parsed = true
		}
	}

	if !parsed || j["data"] == nil || toStr(j["data"]) == "" {
		if !parsed && len(body) > 0 {
			name := safeFname(filepath.Base(srcPath))
			if name == "" || name == "." {
				name = "nb.txt"
			}
			return c.ingestWhole(body, name, "", info)
		}
		return info
	}

	cid := toStr(j["command_id"])
	fname := safeFname(toStr(j["filename"]))
	if cid == "" || fname == "" {
		return info
	}
	data, err := b64decodeLenient(toStr(j["data"]))
	if err != nil {
		info["kind"] = "bad_b64"
		return info
	}
	if _, ok := j["chunk_index"]; ok {
		return c.ingestChunk(cid, fname, int(toInt(j["chunk_index"])), int(toInt(j["total_chunks"])), data, info)
	}
	if _, ok := j["total_chunks"]; ok {
		return c.ingestChunk(cid, fname, int(toInt(j["chunk_index"])), int(toInt(j["total_chunks"])), data, info)
	}
	return c.ingestWhole(data, fname, cid, info)
}

func (c *ChunkStore) ingestChunk(cid, fname string, idx, total int, data []byte, info map[string]any) map[string]any {
	if idx < 0 || idx > 10000 || len(data) > 8*1024*1024 {
		info["kind"] = "chunk_out_of_range"
		return info
	}
	c.writePart(cid, fname, idx, data)
	c.mergeTotal(cid, fname, total)
	SafeCall(func() { RecordChunk(cid, fname, idx, len(data), nil) })
	c.flow(cid, map[string]any{"event": "chunk", "command_id": cid, "filename": fname,
		"part": idx, "size": len(data), "total": c.readTotal(cid, fname), "have": len(c.scanParts(cid, fname))})

	parts := c.scanParts(cid, fname)
	totalNow := c.readTotal(cid, fname)
	if totalNow > 0 && len(parts) == totalNow && isContiguous(parts, totalNow) {
		return c.finalize(cid, fname, true, info)
	}
	return map[string]any{"src": info["src"], "kind": "chunk", "part": idx,
		"have": len(parts), "total": totalNow}
}

func (c *ChunkStore) ingestWhole(data []byte, fname, cid string, info map[string]any) map[string]any {
	if len(data) == 0 {
		return info
	}
	c.writePart(cid, fname, 0, data)
	c.mergeTotal(cid, fname, 1)
	rcid := cid
	if rcid == "" {
		rcid = "@" + fname
	}
	SafeCall(func() { RecordChunk(rcid, fname, 0, len(data), nil) })
	c.flow(cid, map[string]any{"event": "chunk", "command_id": cid, "filename": fname,
		"part": 0, "size": len(data), "total": 1, "have": 1})
	extra := map[string]any{"src": info["src"], "kind": "whole", "size": len(data)}
	return c.finalize(cid, fname, true, extra)
}

// SweepIdle 清理空闲超 10s 的组：full 则重组，partial 也落库（complete=0）。
func (c *ChunkStore) SweepIdle() {
	base := filepath.Join(c.root, partsDirname)
	dirs, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		cidDir := filepath.Join(base, d.Name())
		files, err := os.ReadDir(cidDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".meta.json") {
				continue
			}
			fi, err := f.Info()
			if err != nil {
				continue
			}
			if time.Since(fi.ModTime()).Seconds() <= IDLE_SECS {
				continue
			}
			fname := strings.TrimSuffix(f.Name(), ".meta.json")
			cid := d.Name()
			if m := c.readMeta(cidDir, fname); m != nil && toStr(m["cid"]) != "" {
				cid = toStr(m["cid"])
			}
			c.finalize(cid, fname, false, nil)
		}
	}
}

// finalize 把一个组的现有 parts 重组为成品文件 + files 表登记 + 流水行。
// complete 表示调用方认为已收齐；仍需索引从 0 连续覆盖才判为完整。
func (c *ChunkStore) finalize(cid, fname string, complete bool, extra map[string]any) map[string]any {
	dir := c.partsDir(cid)
	// 跨进程互斥：O_EXCL 锁文件
	lockPath := filepath.Join(dir, safeFname(fname)+".lock")
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return map[string]any{"kind": "finalizing"}
	}
	_ = lf.Close()
	defer os.Remove(lockPath)

	c.mu.Lock()
	parts := c.scanParts(cid, fname)
	total := c.readTotal(cid, fname)
	c.mu.Unlock()
	if len(parts) == 0 {
		c.clearGroup(cid, fname, nil)
		if extra != nil {
			return extra
		}
		return map[string]any{"kind": "noop"}
	}
	ordered := make([]int, 0, len(parts))
	for i := range parts {
		ordered = append(ordered, i)
	}
	sort.Ints(ordered)
	contiguous := complete && total > 0 && len(ordered) == total && isContiguous(parts, total)

	digest, outRel, size := "", "", 0
	if contiguous {
		var buf []byte
		for _, i := range ordered {
			buf = append(buf, c.readPart(cid, fname, i)...)
		}
		digest = sha256Hex(buf)
		size = len(buf)
		outRel = "files/" + digest + "-" + fname
		outPath := filepath.Join(c.root, filepath.FromSlash(outRel))
		_ = ensureDir(filepath.Dir(outPath))
		_ = os.WriteFile(outPath, buf, 0o644)
	}
	c.clearGroup(cid, fname, ordered)

	comp := 0
	if contiguous {
		comp = 1
	}
	SafeCall(func() { RecordFile(cid, fname, digest, size, outRel, comp) })
	c.flow(cid, map[string]any{"event": "file", "command_id": cid, "filename": fname,
		"sha256": digest, "size": size, "path": outRel, "complete": comp})

	done := map[string]any{}
	for k, v := range extra {
		done[k] = v
	}
	if contiguous {
		done["kind"] = "file"
	} else {
		done["kind"] = "partial"
	}
	done["sha256"] = digest
	done["size"] = size
	done["path"] = outRel
	done["complete"] = comp
	return done
}

// ── parts 临时文件 ──
func (c *ChunkStore) partsDir(cid string) string {
	name := cid
	if name == "" {
		name = "nocmd"
	}
	d := filepath.Join(c.root, partsDirname, safeFname(name))
	_ = ensureDir(d)
	return d
}

func (c *ChunkStore) writePart(cid, fname string, idx int, data []byte) {
	p := filepath.Join(c.partsDir(cid), safeFname(fname)+"."+strconv.Itoa(idx)+".part")
	_ = os.WriteFile(p, data, 0o644)
}

func (c *ChunkStore) readPart(cid, fname string, idx int) []byte {
	p := filepath.Join(c.partsDir(cid), safeFname(fname)+"."+strconv.Itoa(idx)+".part")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// scanParts 扫描该组已落盘的 part（跨实例共享）。
func (c *ChunkStore) scanParts(cid, fname string) map[int]int {
	out := map[int]int{}
	entries, err := os.ReadDir(c.partsDir(cid))
	if err != nil {
		return out
	}
	prefix := safeFname(fname) + "."
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, ".part") {
			continue
		}
		mid := n[len(prefix) : len(n)-len(".part")]
		idx, err := strconv.Atoi(mid)
		if err != nil {
			continue
		}
		size := 0
		if fi, err := e.Info(); err == nil {
			size = int(fi.Size())
		}
		out[idx] = size
	}
	return out
}

// ── meta（total / cid）──
func (c *ChunkStore) metaPath(cid, fname string) string {
	return filepath.Join(c.partsDir(cid), safeFname(fname)+".meta.json")
}

func (c *ChunkStore) readMeta(dir, fname string) map[string]any {
	b, err := os.ReadFile(filepath.Join(dir, safeFname(fname)+".meta.json"))
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func (c *ChunkStore) readTotal(cid, fname string) int {
	m := c.readMeta(c.partsDir(cid), fname)
	if m == nil {
		return 0
	}
	return int(toInt(m["total"]))
}

// mergeTotal 更新 total（取最大值），并记录原始 cid（供 idle sweep 复原）。
func (c *ChunkStore) mergeTotal(cid, fname string, total int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := c.metaPath(cid, fname)
	cur := 0
	origCID := cid
	if b, err := os.ReadFile(path); err == nil {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			cur = int(toInt(m["total"]))
			if toStr(m["cid"]) != "" {
				origCID = toStr(m["cid"])
			}
		}
	}
	if total > cur {
		cur = total
	}
	rec := map[string]any{"total": cur, "cid": origCID, "fname": fname, "updated_ts": nowStr()}
	b, _ := json.Marshal(rec)
	_ = os.WriteFile(path, b, 0o644)
}

func (c *ChunkStore) clearGroup(cid, fname string, idxs []int) {
	dir := c.partsDir(cid)
	if idxs == nil {
		idxs = []int{}
		for i := range c.scanParts(cid, fname) {
			idxs = append(idxs, i)
		}
	}
	for _, i := range idxs {
		_ = os.Remove(filepath.Join(dir, safeFname(fname)+"."+strconv.Itoa(i)+".part"))
	}
	_ = os.Remove(c.metaPath(cid, fname))
}

// flow 归并流水：capture/files_<command_id>.jsonl（无数据库正文）。
func (c *ChunkStore) flow(cid string, obj map[string]any) {
	defer func() { _ = recover() }()
	name := cid
	if name == "" {
		name = "nocmd"
	}
	name = "files_" + safeFname(name) + ".jsonl"
	rec := map[string]any{"ts": nowStr()}
	for k, v := range obj {
		rec[k] = v
	}
	line, _ := json.Marshal(rec)
	f, err := os.OpenFile(filepath.Join(c.root, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// ── 辅助 ──
func isContiguous(parts map[int]int, total int) bool {
	if len(parts) != total {
		return false
	}
	for i := 0; i < total; i++ {
		if _, ok := parts[i]; !ok {
			return false
		}
	}
	return true
}

// b64decodeLenient 等价 Python base64.b64decode(validate=False)。
func b64decodeLenient(s string) ([]byte, error) {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '+' || r == '/' || r == '=' {
			b.WriteRune(r)
		}
	}
	clean := b.String()
	if m := len(clean) % 4; m != 0 {
		clean += strings.Repeat("=", 4-m)
	}
	return b64StdDecode(clean)
}

// ── 单例 ──
var (
	chunkMu     sync.Mutex
	globalChunk *ChunkStore
)

// InitChunkStore 初始化全局分块归并器（含后台空闲清扫）。
func InitChunkStore(root string) *ChunkStore {
	chunkMu.Lock()
	defer chunkMu.Unlock()
	globalChunk = NewChunkStore(root)
	go func(cs *ChunkStore) {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for range t.C {
			func() {
				defer func() { _ = recover() }()
				cs.SweepIdle()
			}()
		}
	}(globalChunk)
	return globalChunk
}

// GetChunkStore 返回全局分块归并器。
func GetChunkStore() *ChunkStore {
	chunkMu.Lock()
	defer chunkMu.Unlock()
	return globalChunk
}
