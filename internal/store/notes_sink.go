package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// ProcessNB 解析 /nb 回传体；db 文件解码落盘；返回摘要（写 JSONL）。
func ProcessNB(body []byte, captureRoot, srcFile string) map[string]any {
	var o map[string]any
	if err := json.Unmarshal(body, &o); err != nil {
		return map[string]any{"ok": false, "err": "bad json"}
	}
	lhu := toStr(o["lhu"])
	if lhu == "" {
		lhu = "unknown"
	}
	if len(lhu) > 64 {
		lhu = lhu[:64]
	}
	notes := asAnySlice(o["list"])

	dbDir := filepath.Join(captureRoot, "notes_db", safeDir(lhu))
	_ = ensureDir(dbDir)
	saved := []any{}
	for _, fv := range asAnySlice(o["db_files"]) {
		f, _ := fv.(map[string]any)
		name := toStr(f["name"])
		if name == "" {
			name = "NoteStore.sqlite"
		}
		name = strings.NewReplacer("/", "_", "\\", "_").Replace(name)
		if len(name) > 120 {
			name = name[:120]
		}
		b64 := toStr(f["data"])
		if b64 == "" {
			saved = append(saved, map[string]any{"name": name, "path": "", "size": 0,
				"note": "no-inline-data (HQ_MEMO_DB_ENABLED=false); 用 memo_scan 指令按需拉取"})
			continue
		}
		ts := time.Now().Format("20060102-150405")
		out := filepath.Join(dbDir, ts+"-"+randomHex(6)+"-"+name)
		raw, err := b64decodeLenient(b64)
		if err != nil {
			saved = append(saved, map[string]any{"name": name, "path": "", "err": err.Error()})
			continue
		}
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			saved = append(saved, map[string]any{"name": name, "path": "", "err": err.Error()})
			continue
		}
		saved = append(saved, map[string]any{"name": name,
			"path": "capture/notes_db/" + lhu + "/" + filepath.Base(out), "size": len(raw)})
	}

	if srcFile == "" {
		srcFile = "live"
	}
	rec := map[string]any{
		"ts":         nowStr(),
		"lhu":        lhu,
		"src":        srcFile,
		"notes":      notes,
		"note_count": len(notes),
		"db":         saved,
	}
	if line, err := json.Marshal(rec); err == nil {
		f, err := os.OpenFile(filepath.Join(captureRoot, "notes.jsonl"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
	}
	return map[string]any{"ok": true, "lhu": lhu, "note_count": len(notes), "db": saved}
}

func safeDir(s string) string {
	var b strings.Builder
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-' || c == '.' {
			b.WriteRune(c)
		}
	}
	out := strings.TrimRight(b.String(), ".")
	if len(out) > 64 {
		out = out[:64]
	}
	if out == "" {
		return "unknown"
	}
	return out
}

// TailNotes 读取 notes.jsonl 尾部 n 条（新->旧）。
func TailNotes(captureRoot string, n int) []map[string]any {
	p := filepath.Join(captureRoot, "notes.jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		return []map[string]any{}
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			kept = append(kept, lines[i])
		}
	}
	out := []map[string]any{}
	for _, ln := range kept {
		var m map[string]any
		if json.Unmarshal([]byte(ln), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}
