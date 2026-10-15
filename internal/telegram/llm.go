package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mockc2/internal/store"
)

// ── 大模型对接（窄口径）──
//
// 刻意不做工具调用、不读文件/代码、不暴露敏感明细、无控制权：
// AI 只能基于后端注入的**受限快照**（KPI + 少量 Top 列表，≤1.5KB）做问答。
// 目的：AI 是「读数播报员」，不是能四处漫游/理解系统的代理。

type llmClient struct {
	base  string
	key   string
	model string
	hc    *http.Client
}

func newLLMClient(c LLMConfig) *llmClient {
	return &llmClient{
		base:  strings.TrimRight(c.BaseURL, "/"),
		key:   c.APIKey,
		model: c.Model,
		hc:    &http.Client{Timeout: time.Duration(c.Timeout) * time.Second},
	}
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (c *llmClient) chat(ctx context.Context, msgs []chatMsg) (string, error) {
	payload := map[string]any{"model": c.model, "messages": msgs, "temperature": 0.2}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", res.StatusCode, clip(string(raw), 300))
	}
	var out struct {
		Choices []struct {
			Message chatMsg `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("响应解析失败: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("%s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("空响应")
	}
	return out.Choices[0].Message.Content, nil
}

// askAI 单轮问答：system + 受限快照 + 用户问题。无工具、无多轮漫游。
func (b *Bot) askAI(ctx context.Context, prompt string) string {
	st := b.snapshot()
	if !st.LLM.Enabled {
		return "AI 接管未启用（/set llm_enabled true 开启，或发送 /help 用内置命令）。"
	}
	if strings.TrimSpace(st.LLM.APIKey) == "" &&
		!strings.Contains(st.LLM.BaseURL, "localhost") && !strings.Contains(st.LLM.BaseURL, "127.0.0.1") {
		return "AI 接管缺少 llm.api_key（本地推理端点可留空）。"
	}
	client := newLLMClient(st.LLM)
	sys := strings.TrimSpace(st.LLM.System)
	if sys == "" {
		sys = defaultSystemPrompt()
	}
	msgs := []chatMsg{
		{Role: "system", Content: sys},
		{Role: "system", Content: "以下是本次问答唯一允许依据的靶场快照（其余数据不可访问，不得臆测）：\n" + b.buildContext()},
		{Role: "user", Content: clip(prompt, 1000)},
	}
	content, err := client.chat(ctx, msgs)
	if err != nil {
		return "AI 调用失败: " + err.Error()
	}
	if strings.TrimSpace(content) == "" {
		return "(AI 无回复)"
	}
	return content
}

func defaultSystemPrompt() string {
	return "你是「Mock C2」授权内网靶场的播报助手，用简体中文、简洁回答。\n" +
		"你只能依据随附的「靶场快照」作答；快照里没有的，直接说“快照未包含”。\n" +
		"不得访问代码、文件或其它系统数据，不得执行任何操作，不得声称已下发指令。"
}

// buildContext 受限快照：聚合 KPI + 少量 Top 列表，硬截断到 ~1.5KB。
// 刻意不含备忘录正文 / 应用清单 / 钥匙串条目 / 文件路径等敏感明细。
func (b *Bot) buildContext() string {
	var s strings.Builder
	st := store.AggStats()
	fmt.Fprintf(&s, "KPI: 设备 %d（在线 %d）· 来访 %d（今日 +%d）· 备忘录 %d · war %d · App 包 %d\n",
		store.ToInt(st["devices"]), store.ToInt(st["online"]), store.ToInt(st["visits"]),
		store.ToInt(st["visits_today"]), store.ToInt(st["notes"]),
		store.ToInt(st["war"]), store.ToInt(st["apps_pkg"]))

	if devs := asRows(store.ListDevicesPaged(1, 5, "", "all")["devices"]); len(devs) > 0 {
		s.WriteString("设备(最近5):\n")
		for _, d := range devs {
			name := store.ToStr(d["deviceName"])
			if name == "" {
				name = store.ToStr(d["machine"])
			}
			fmt.Fprintf(&s, "- %s %s iOS%s\n", clip(store.ToStr(d["uuid"]), 18), orDash(name), orDash(store.ToStr(d["ios"])))
		}
	}
	if ws := store.Summary("", 5); len(ws) > 0 {
		s.WriteString("钱包命中(Top5): ")
		parts := make([]string, 0, len(ws))
		for _, w := range ws {
			rule := store.ToStr(w["rule_name"])
			parts = append(parts, fmt.Sprintf("%s×%v", store.Display(rule), w["count"]))
		}
		s.WriteString(strings.Join(parts, " ") + "\n")
	}
	return clip(s.String(), 1500)
}
