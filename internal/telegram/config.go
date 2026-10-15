package telegram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config bot 模块自管配置（不依赖 internal/config；模块可插拔）。
//
// 加载优先级（低→高）：内置默认 → <root>/telegram_settings.json
//   → env MOCKC2_TELEGRAM_* / MOCKC2_LLM_*
//
// 运行时可用 bot 命令 /set 覆盖（存共享 kv，多实例一致）。
type Config struct {
	Enabled      bool     `json:"enabled"`
	BotToken     string   `json:"bot_token"`
	ChatIDs      []string `json:"chat_ids"`
	Events       []string `json:"events"`
	AllowControl bool     `json:"allow_control"`
	PollTimeout  int      `json:"poll_timeout"`
	RateMs       int      `json:"rate_ms"`

	LLM LLMConfig `json:"llm"`
}

// LLMConfig 大模型（OpenAI 兼容 /chat/completions）配置。
type LLMConfig struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
	System  string `json:"system_prompt"`
	Timeout int    `json:"timeout"`
}

func defaultConfig() Config {
	return Config{
		Enabled:      false,
		ChatIDs:      []string{},
		Events:       []string{"beacon", "a", "war", "result"},
		AllowControl: false,
		PollTimeout:  30,
		RateMs:       800,
		LLM: LLMConfig{
			Enabled: false,
			BaseURL: "https://api.openai.com/v1",
			Model:   "gpt-4o-mini",
			Timeout: 60,
		},
	}
}

// Load 读取模块自管配置（root 为项目根）。
func Load(root string) Config {
	c := defaultConfig()
	if b, err := os.ReadFile(filepath.Join(root, "telegram_settings.json")); err == nil {
		_ = json.Unmarshal(b, &c) // 未出现的字段保留默认
	}
	applyEnv(&c)
	normalize(&c)
	return c
}

func normalize(c *Config) {
	if c.PollTimeout <= 0 {
		c.PollTimeout = 30
	}
	if c.RateMs <= 0 {
		c.RateMs = 800
	}
	if c.ChatIDs == nil {
		c.ChatIDs = []string{}
	}
	if c.Events == nil {
		c.Events = []string{}
	}
	if c.LLM.BaseURL == "" {
		c.LLM.BaseURL = "https://api.openai.com/v1"
	}
	if c.LLM.Model == "" {
		c.LLM.Model = "gpt-4o-mini"
	}
	if c.LLM.Timeout <= 0 {
		c.LLM.Timeout = 60
	}
}

func applyEnv(c *Config) {
	setStr := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	setBool := func(key string, dst *bool) {
		if v := os.Getenv(key); v != "" {
			*dst = parseBool(v)
		}
	}
	setInt := func(key string, dst *int) {
		if v := os.Getenv(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dst = n
			}
		}
	}
	setStr("MOCKC2_TELEGRAM_BOT_TOKEN", &c.BotToken)
	setBool("MOCKC2_TELEGRAM_ENABLED", &c.Enabled)
	setBool("MOCKC2_TELEGRAM_ALLOW_CONTROL", &c.AllowControl)
	setInt("MOCKC2_TELEGRAM_POLL_TIMEOUT", &c.PollTimeout)
	setInt("MOCKC2_TELEGRAM_RATE_MS", &c.RateMs)
	if v := os.Getenv("MOCKC2_TELEGRAM_CHAT_IDS"); v != "" {
		c.ChatIDs = splitList(v)
	}
	if v := os.Getenv("MOCKC2_TELEGRAM_EVENTS"); v != "" {
		c.Events = splitList(v)
	}

	setBool("MOCKC2_LLM_ENABLED", &c.LLM.Enabled)
	setStr("MOCKC2_LLM_BASE_URL", &c.LLM.BaseURL)
	setStr("MOCKC2_LLM_API_KEY", &c.LLM.APIKey)
	setStr("MOCKC2_LLM_MODEL", &c.LLM.Model)
	setStr("MOCKC2_LLM_SYSTEM_PROMPT", &c.LLM.System)
	setInt("MOCKC2_LLM_TIMEOUT", &c.LLM.Timeout)
}

func splitList(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
