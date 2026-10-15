package store

import (
	"database/sql"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动（免 cgo，静态构建可用）
)

// NoteText 从 NoteStore.sqlite 抽出的一条备忘录。
type NoteText struct {
	ID    int64
	Title string
	Body  string
}

var htmlTagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// stripHTML 极简 HTML→文本：去标签 + 解实体 + 压缩空白。
func stripHTML(s string) string {
	if !strings.Contains(s, "<") && !strings.Contains(s, "&") {
		return strings.TrimSpace(s)
	}
	t := htmlTagRe.ReplaceAllString(s, " ")
	t = html.UnescapeString(t)
	t = strings.Join(strings.Fields(t), " ")
	return strings.TrimSpace(t)
}

// anyToString 把 sqlite 列值转成文本：[]byte 仅在像文本时保留，HTML 去标签。
func anyToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return stripHTML(x)
	case []byte:
		if !isUTF8(x) {
			return "" // 二进制 blob（Apple protobuf 等）跳过
		}
		return stripHTML(string(x))
	default:
		return toStr(v)
	}
}

func sqliteTables(db *sql.DB) map[string]bool {
	out := map[string]bool{}
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out[strings.ToUpper(n)] = true
		}
	}
	return out
}

func sqliteCols(db *sql.DB, table string) map[string]bool {
	out := map[string]bool{}
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil {
			out[strings.ToUpper(name)] = true
		}
	}
	return out
}

func firstCol(cols map[string]bool, cands ...string) string {
	for _, c := range cands {
		if cols[strings.ToUpper(c)] {
			return c
		}
	}
	return ""
}

// ExtractNotesFromSQLite 解析设备上传的 NoteStore.sqlite，尽力抽取备忘录文本。
// 兼容 Apple Notes 的 ZNOTE/ZNOTEBODY 结构；不认识的结构则回退为"扫描所有含
// 文本列的表"。任何异常都返回已取到的部分。
func ExtractNotesFromSQLite(path string) ([]NoteText, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tables := sqliteTables(db)
	var out []NoteText

	// ── Apple Notes：ZNOTE（标题/摘要）+ ZNOTEBODY（正文）──
	if tables["ZNOTE"] {
		bodies := map[int64]string{}
		if tables["ZNOTEBODY"] {
			bcols := sqliteCols(db, "ZNOTEBODY")
			textCol := firstCol(bcols, "ZHTMLSTRING", "ZCONTENT", "ZTEXT", "ZSNIPPET")
			if textCol != "" && bcols["ZNOTE"] {
				if rows, err := db.Query("SELECT ZNOTE, " + textCol + " FROM ZNOTEBODY"); err == nil {
					for rows.Next() {
						var pk int64
						var v any
						if rows.Scan(&pk, &v) == nil {
							if t := anyToString(v); t != "" {
								bodies[pk] = t
							}
						}
					}
					rows.Close()
				}
			}
		}
		cols := sqliteCols(db, "ZNOTE")
		if cols["Z_PK"] {
			titleCol := firstCol(cols, "ZTITLE", "ZTITLE1", "ZNAME")
			snipCol := firstCol(cols, "ZSNIPPET", "ZSUMMARY")
			q := "SELECT Z_PK"
			if titleCol != "" {
				q += ", " + titleCol
			} else {
				q += ", ''"
			}
			if snipCol != "" {
				q += ", " + snipCol
			} else {
				q += ", ''"
			}
			q += " FROM ZNOTE"
			if rows, err := db.Query(q); err == nil {
				for rows.Next() {
					var pk int64
					var title, snip any
					if rows.Scan(&pk, &title, &snip) == nil {
						nt := NoteText{ID: pk, Title: anyToString(title)}
						body := bodies[pk]
						if body == "" {
							body = anyToString(snip)
						}
						nt.Body = body
						if nt.Title != "" || nt.Body != "" {
							out = append(out, nt)
						}
					}
				}
				rows.Close()
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	// ── 回退：扫描所有含文本列的表（name/title/text/content/body/snippet）──
	for tbl := range tables {
		if strings.HasPrefix(tbl, "SQLITE_") {
			continue
		}
		cols := sqliteCols(db, tbl)
		var pick []string
		for _, want := range []string{"TITLE", "NAME", "TEXT", "CONTENT", "BODY", "SNIPPET"} {
			if cols[want] {
				pick = append(pick, want)
			}
		}
		if len(pick) == 0 {
			continue
		}
		q := "SELECT " + strings.Join(pick, ", ") + " FROM " + tbl + " LIMIT 5000"
		rows, err := db.Query(q)
		if err != nil {
			continue
		}
		for rows.Next() {
			vals := make([]any, len(pick))
			ptrs := make([]any, len(pick))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if rows.Scan(ptrs...) != nil {
				continue
			}
			nt := NoteText{}
			for i, c := range pick {
				t := anyToString(vals[i])
				if t == "" {
					continue
				}
				if strings.Contains(c, "TITLE") || c == "NAME" {
					nt.Title = t
				} else if nt.Body == "" {
					nt.Body = t
				} else {
					nt.Body += " " + t
				}
			}
			if nt.Title != "" || nt.Body != "" {
				out = append(out, nt)
			}
		}
		rows.Close()
	}
	return out, nil
}

// BackfillNotesDB 扫描 capture/notes_db 下的 sqlite 文件，抽取正文写入 memos。
// 幂等（memos 唯一键 uuid+note_id + UPSERT）。返回抽取到的备忘录条数。
func BackfillNotesDB(captureRoot string) int {
	root := filepath.Join(captureRoot, "notes_db")
	total := 0
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		low := strings.ToLower(d.Name())
		if !strings.HasSuffix(low, ".sqlite") && !strings.HasSuffix(low, ".db") {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		uuid := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		notes, e := ExtractNotesFromSQLite(p)
		if e != nil || len(notes) == 0 {
			return nil
		}
		total += UpsertMemos(uuid, notes)
		return nil
	})
	return total
}
