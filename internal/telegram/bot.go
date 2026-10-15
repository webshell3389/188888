package telegram

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"mockc2/internal/store"
)

// Bot Telegram bot 运行时（单例）。既做事件推送，也做「命令版前端」。
type Bot struct {
	client *Client
	base   Config

	mu        sync.RWMutex
	state     runtimeState
	username  string
	startedAt time.Time
	lastErr   string
	sent      int64
	skipped   int64

	offset   int64
	notifyCh chan notifyItem
}

var (
	mu      sync.RWMutex
	current *Bot
)

// Get 返回运行中的 bot（未启动返回 nil）。
func Get() *Bot {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Start 启动 bot（自管配置；token 为空则静默不启动）。幂等。
func Start(root string) *Bot {
	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		return current
	}
	base := Load(root)
	if strings.TrimSpace(base.BotToken) == "" {
		log.Printf("[telegram] 未配置 bot_token（telegram_settings.json / MOCKC2_TELEGRAM_BOT_TOKEN），模块未启动")
		return nil
	}
	b := &Bot{
		client:    NewClient(base.BotToken),
		base:      base,
		state:     compile(loadRuntime(base)),
		startedAt: time.Now(),
		notifyCh:  make(chan notifyItem, 256),
	}
	current = b

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if me, err := b.client.GetMe(ctx); err != nil {
		b.setErr(err.Error())
		log.Printf("[telegram] getMe 失败: %v", err)
	} else {
		b.username = me.Username
		log.Printf("[telegram] bot @%s 已连接（enabled=%v chats=%d）",
			me.Username, b.snapshot().Enabled, len(b.snapshot().ChatIDs))
	}
	cancel()

	b.attachNotifier()
	go b.sendLoop()
	go b.pollLoop()
	go b.reloadLoop()
	return b
}

// Status 返回模块状态（未启动也有兜底）。
func Status() map[string]any {
	if b := Get(); b != nil {
		return b.Status()
	}
	return map[string]any{"running": false, "hint": "未配置 bot_token 或模块未启动"}
}

func (b *Bot) snapshot() runtimeState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

func (b *Bot) setState(c Config) {
	b.mu.Lock()
	b.state = compile(c)
	b.mu.Unlock()
}

func (b *Bot) setErr(s string) {
	b.mu.Lock()
	b.lastErr = s
	b.mu.Unlock()
}

func (b *Bot) getErr() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastErr
}

// Status 单实例状态。
func (b *Bot) Status() map[string]any {
	st := b.snapshot()
	b.mu.RLock()
	user, started, sent, skipped := b.username, b.startedAt, b.sent, b.skipped
	b.mu.RUnlock()
	return map[string]any{
		"running":    true,
		"username":   user,
		"started_at": started.Format("2006-01-02 15:04:05"),
		"last_error": b.getErr(),
		"queue":      len(b.notifyCh),
		"sent":       sent,
		"skipped":    skipped,
		"config":     st.Config,
	}
}

// reloadLoop 周期回读共享 kv：运行时 /set 的改动（含多实例）跟随生效。
func (b *Bot) reloadLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		b.setState(loadRuntime(b.base))
	}
}

// pollLoop 长轮询 getUpdates（永不退出；错误退避）。
func (b *Bot) pollLoop() {
	for {
		st := b.snapshot()
		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(st.PollTimeout+20)*time.Second)
		ups, err := b.client.GetUpdates(ctx, b.offset, st.PollTimeout)
		cancel()
		if err != nil {
			b.setErr(err.Error())
			time.Sleep(3 * time.Second)
			continue
		}
		if b.getErr() != "" {
			b.setErr("")
		}
		for _, u := range ups {
			if u.UpdateID >= b.offset {
				b.offset = u.UpdateID + 1
			}
			if !b.snapshot().Enabled {
				continue // 关闭时仍推进 offset，重新开启不补发旧消息
			}
			b.handleUpdate(u)
		}
	}
}

func (b *Bot) handleUpdate(u Update) {
	m := u.Message
	if m == nil || strings.TrimSpace(m.Text) == "" {
		return
	}
	st := b.snapshot()
	if !st.allowed[m.Chat.ID] {
		return // 非白名单静默忽略
	}
	text := strings.TrimSpace(m.Text)
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return
	}

	var reply string
	if strings.HasPrefix(fields[0], "/") {
		cmd := strings.ToLower(fields[0])
		if i := strings.IndexByte(cmd, '@'); i >= 0 {
			cmd = cmd[:i] // 支持 /cmd@botname
		}
		if cmd == "/ai" {
			prompt := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
			if prompt == "" {
				reply = "用法: /ai <问题>"
			} else {
				ctx, cancel := context.WithTimeout(context.Background(),
					time.Duration(st.LLM.Timeout+15)*time.Second)
				reply = b.askAI(ctx, prompt)
				cancel()
			}
		} else {
			reply = b.dispatch(cmd, fields[1:])
		}
	} else {
		// 自由文本 → 大模型接管（命令版前端的「自然语言入口」）
		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(st.LLM.Timeout+15)*time.Second)
		reply = b.askAI(ctx, text)
		cancel()
	}
	if strings.TrimSpace(reply) == "" {
		return
	}
	b.reply(m.Chat.ID, reply)
}

// reply 分片发送（Telegram 单条上限 4096）。
func (b *Bot) reply(chatID int64, text string) {
	for _, chunk := range chunkText(text, 3800) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := b.client.SendMessage(ctx, chatID, chunk); err != nil {
			b.setErr(err.Error())
		}
		cancel()
	}
}

// attachNotifier 把入库观察者接到通知队列（非阻塞；队列满即丢弃，绝不拖慢主链路）。
func (b *Bot) attachNotifier() {
	store.GetStore().AddObserver(func(ep, rel string, size int, raw []byte, srcIP string) {
		st := b.snapshot()
		if !st.Enabled || !st.events[ep] {
			return
		}
		select {
		case b.notifyCh <- notifyItem{ep: ep, rel: rel, size: size, raw: raw, ts: time.Now()}:
		default:
			b.mu.Lock()
			b.skipped++
			b.mu.Unlock()
		}
	})
}

// sendLoop 通知发送 worker：限速 + 广播到白名单会话。
func (b *Bot) sendLoop() {
	for it := range b.notifyCh {
		st := b.snapshot()
		if !st.Enabled || !st.events[it.ep] {
			continue
		}
		text := formatNotify(it)
		if text == "" {
			continue
		}
		for id := range st.allowed {
			b.reply(id, text)
		}
		b.mu.Lock()
		b.sent++
		b.mu.Unlock()
		time.Sleep(time.Duration(st.RateMs) * time.Millisecond)
	}
}

// chunkText 按行分片，避免超过单条上限。
func chunkText(s string, limit int) []string {
	if len(s) <= limit {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if cur.Len()+len(line)+1 > limit && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimRight(cur.String(), "\n"))
	}
	return out
}
