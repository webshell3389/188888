package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func isUTF8(b []byte) bool { return utf8.Valid(b) }

// decodeReplace 等价 Python bytes.decode("utf-8", "replace")。
func decodeReplace(b []byte) string { return strings.ToValidUTF8(string(b), "\uFFFD") }

// itoa 整数转十进制字符串。
func itoa(n int) string { return strconv.Itoa(n) }

// itoa64 int64 转十进制字符串。
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// b64StdDecode 标准 base64 解码（入参已清洗/补齐）。
func b64StdDecode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// ── 路径 ──
func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// ── 类型转换 ──
func toInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	case nil:
		return 0
	}
	return 0
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	case nil:
		return 0
	}
	return 0
}

func toStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case []byte:
		return string(t)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ── 导出类型转换（供 server 层使用）──

// ToStr 任意值转字符串。
func ToStr(v any) string { return toStr(v) }

// ToInt 任意值转 int64。
func ToInt(v any) int64 { return toInt(v) }

// ToFloat 任意值转 float64。
func ToFloat(v any) float64 { return toFloat(v) }

// ── 杂项 ──
func randomHex(n int) string {
	buf := make([]byte, (n+1)/2)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)[:n]
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

func nowEpoch() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// fileMTimeStr 供看板时间戳对齐使用。
func fileMTimeStr(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}
