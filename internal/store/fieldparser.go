package store

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Extract 按 endpoint 提炼基本字段 —— 供 packet_meta 入库与 dash 卡片共用。
//
// 单来源原则：dash 的展示解析也走这里，避免两套规则漂移。
// 返回 (content_kind, fields)；content_kind: 'json' | 'raw' | 'binary'。
func Extract(endpoint string, raw []byte) (string, [][2]string) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return kindOf(raw), metaFromRaw(raw)
	}
	j, ok := v.(map[string]any)
	if !ok {
		// 顶层数组/标量 JSON：无结构化字段（Python 版在此处异常，Go 侧稳健处理）
		return "json", nil
	}

	fields := make([][2]string, 0, 8)
	add := func(k string, val any) {
		if val == nil {
			return
		}
		s := clip(val, false)
		if s != "" {
			fields = append(fields, [2]string{k, s})
		}
	}
	sizeOf := func(val any) (string, bool) {
		switch t := val.(type) {
		case []any:
			return fmt.Sprintf("%d", len(t)), true
		case map[string]any:
			return fmt.Sprintf("%d", len(t)), true
		}
		return "", false
	}

	switch endpoint {
	case "sync":
		add("deviceVersion", j["deviceVersion"])
		add("domain", j["domain"])
	case "beacon":
		add("uuid", j["uuid"])
		add("status", j["status"])
		add("type", j["type"])
		add("machine", j["machine"])
		if di, ok := j["device_info"].(map[string]any); ok {
			add("machine", di["machine"])
			add("hostname", di["hostname"])
			add("ios_version", di["ios_version"])
			add("build_version", di["build_version"])
		}
	case "result":
		add("command_id", j["command_id"])
		add("status", j["status"])
		add("_task_type", j["_task_type"])
		add("filename", j["filename"])
		add("error", j["error"])
	case "heartbeat":
		s := decodeReplace(raw)
		if len(s) > 64 {
			s = s[:64]
		}
		add("signal", s)
	case "a", "u", "nb", "event":
		add("lhu", j["lhu"])
		add("type", j["type"])
		add("machine", j["machine"])
		add("source", j["source"])
		for k, val := range j {
			if n, ok := sizeOf(val); ok {
				add(k+"_count", n)
			}
		}
		if endpoint == "u" {
			apps := asAnySlice(j["apps"])
			for i, a := range apps {
				if i >= 4 {
					break
				}
				if m, ok := a.(map[string]any); ok {
					add("app_sample", m["name"])
				} else {
					add("app_sample", nil)
				}
			}
		} else if endpoint == "nb" {
			notes := asAnySlice(j["list"])
			for i, n := range notes {
				if i >= 3 {
					break
				}
				if m, ok := n.(map[string]any); ok {
					add("note_title", m["title"])
				}
			}
			for _, f := range asAnySlice(j["db_files"]) {
				if m, ok := f.(map[string]any); ok {
					add("db_file", m["name"])
				}
			}
		}
	case "war":
		lhu := j["lhu"]
		if lhu == nil {
			lhu = j["device_uuid"]
		}
		add("lhu", lhu)
		add("type", j["type"])
		add("source", j["source"])
		for k, val := range j {
			if n, ok := sizeOf(val); ok {
				add(k+"_count", n)
			}
		}
	case "sync_plus":
		// noop
	default:
		// 未知端点：顶层标量字段兜底
		for k, val := range j {
			switch val.(type) {
			case string, float64, bool, int64:
				add(k, val)
			}
		}
	}
	return "json", fields
}

func kindOf(raw []byte) string {
	if isUTF8(raw) {
		return "raw"
	}
	return "binary"
}

func metaFromRaw(raw []byte) [][2]string {
	n := 96
	if kindOf(raw) != "raw" {
		n = 64
	}
	if len(raw) < n {
		n = len(raw)
	}
	// 对齐 Python 意图：raw[:n].hex()[:96]
	h := hex.EncodeToString(raw[:n])
	if len(h) > 96 {
		h = h[:96]
	}
	return [][2]string{{"_hint", h}}
}

func clip(v any, hexify bool) string {
	switch t := v.(type) {
	case []any:
		return fmt.Sprintf("list[%d]", len(t))
	case map[string]any:
		return fmt.Sprintf("dict{%d}", len(t))
	}
	s := toStr(v)
	if hexify {
		h := hex.EncodeToString([]byte(s))
		if len(h) > 96 {
			h = h[:96]
		}
		return h
	}
	if len(s) > 256 {
		return s[:256]
	}
	return s
}

func asAnySlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
