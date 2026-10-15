// Package telegram 实现与 Telegram Bot API 的最小对接（纯标准库，无第三方依赖）。
//
// 设计目标：
//   - 长轮询 getUpdates：靶场无需公网 HTTPS 回调，部署零门槛
//   - 通知 + 只读查询 + 指令控制 全量能力；运行时可在管理后台收紧（共享 kv，多实例一致）
//   - 任何异常都不影响主链路：观察者内只做非阻塞入队，发送独立 worker + 限速
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client Bot API 最小封装。
type Client struct {
	base string
	hc   *http.Client
}

// NewClient 用 bot token 构造客户端。
func NewClient(token string) *Client {
	return &Client{
		base: "https://api.telegram.org/bot" + token,
		hc:   &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("telegram: 响应解析失败: %w", err)
	}
	if !env.OK {
		return fmt.Errorf("telegram: %s", env.Description)
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// User Bot 自身信息。
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat 会话。
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Message 入站消息。
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
}

// Update 长轮询更新项。
type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// GetMe 校验 token 并取 bot 信息。
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.do(ctx, "getMe", map[string]any{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// SendMessage 发送纯文本消息（不用 parse_mode，避免转义踩坑）。
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	return c.do(ctx, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text, "disable_web_page_preview": true,
	}, nil)
}

// GetUpdates 长轮询拉取更新。
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var ups []Update
	err := c.do(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": timeout, "allowed_updates": []string{"message"},
	}, &ups)
	return ups, err
}
