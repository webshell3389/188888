// Package config 集中配置：路径、缓存策略、根路径别名、beacon 响应形态、sink 行为。
//
// 覆盖优先级（低 -> 高）：默认值 -> mockc2_settings.json -> 环境变量 MOCKC2_*
// （与 Python 版 c2mock/core/config.py 行为对齐）
package config

import (
	"crypto/sha256"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Settings 与 Python Settings dataclass 一一对应。
type Settings struct {
	// ── 路径 ──
	RootDir      string // 项目根（go/）
	PagesRoot    string // 落盘样本根
	CaptureRoot  string // 审计留存根
	CertsDir     string // 自签证书目录
	DataDir      string // SQLite 目录
	FrontendDir  string // 浅色管理台静态根

	// ── 监听 ──
	Host      string
	HTTPPort  int
	HTTPSPort int

	// ── 静态 ──
	CacheControl string
	ServerHeader string
	// worker / sbx1 getJS 会从 origin 根拉取 stage 文件（无 /B10/ 前缀）
	RootAlias map[string]bool

	// ── 日志 sink（原站行为: 200 空）──
	LogSinkStatus int

	// ── /beacon 响应模板（%PEER_IP% 占位）──
	BeaconNoop map[string]any
	// ops 端点 token（为空则不校验；生产建议设置）
	OpsToken string
	// 管理后台授权：登录账号（env MOCKC2_ADMIN_USER）
	AdminUser string
	// 管理后台授权：登录口令（MOCKC2_ADMIN_PASSWORD 覆盖；默认 lab 值仅供内网）
	AdminPassword string
	// 管理后台入口路径（env MOCKC2_ADMIN_PATH；默认 /admin；建议改为随机密径，避免被扫）
	AdminPath string
	// 会话有效期（秒）
	AdminSessionTTL int
	// HTTPS 自签证书 SAN
	CertDomains []string
	// lab 模式：真机/模拟器直连本地 IP
	LabMode bool
	LabIP   string
	// 单次请求体审计截断上限
	BodyCaptureLimit int

	// ── 数据库（MySQL/MariaDB，企业部署）──
	DBHost            string // 主机（默认 127.0.0.1）
	DBPort            int    // 端口（默认 3306）
	DBUser            string // 用户名
	DBPassword        string // 密码
	DBName            string // 库名
	DBParams          string // 额外 DSN 参数（k=v&k2=v2）
	DBMaxOpenConns    int    // 连接池上限（默认 32）
	DBMaxIdleConns    int    // 空闲连接（默认 8）
	DBConnMaxLifetime int    // 连接最长复用（分钟，默认 30）

	// ── 多实例/多 worker 横向扩展 ──
	// 无状态会话签名密钥（多实例必须一致；为空则由 admin_password 派生，保证同配置可互通）
	SessionSecret string
	// 实例标识（审计/日志区分来源；--workers 子进程自动注入）
	InstanceID string
}

// SessionKey 返回会话签名密钥（多实例共享同一 key 即可互通）。
func (s *Settings) SessionKey() []byte {
	seed := s.SessionSecret
	if seed == "" {
		seed = "c2mock-session|" + s.AdminPassword
	}
	sum := sha256.Sum256([]byte(seed))
	return sum[:]
}

func defaultRootAlias() map[string]bool {
	names := []string{
		"log.html", "pe_worker.js", "sbx0_main_18.4.js", "sbx1_main.js",
		"rce_worker_18.4.js", "rce_worker_18.6.js", "rce_worker_18.7.js",
		"rce_module.js", "rce_module_18.6.js", "rce_module_18.7.js",
		"rce_loader.js", "group.html",
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Default 返回默认配置（根目录由 ResolveRoot 决定）。
func Default() *Settings {
	root := ResolveRoot()
	return &Settings{
		RootDir:          root,
		PagesRoot:        filepath.Join(root, "www"),
		CaptureRoot:      filepath.Join(root, "capture"),
		CertsDir:         filepath.Join(root, "certs"),
		DataDir:          filepath.Join(root, "data"),
		FrontendDir:      filepath.Join(root, "adminui"),
		Host:             "0.0.0.0",
		HTTPPort:         9100,
		HTTPSPort:        9443,
		CacheControl:     "no-store, no-cache, must-revalidate, max-age=0",
		ServerHeader:     "nginx/1.22.0",
		RootAlias:        defaultRootAlias(),
		LogSinkStatus:    200,
		BeaconNoop:       map[string]any{"client_ip": "%PEER_IP%", "type": "noop"},
		OpsToken:         "",
		AdminUser:        "admin",
		AdminPassword:    "lab888",
		AdminPath:        "/admin",
		AdminSessionTTL:  12 * 3600,
		CertDomains:      []string{},
		LabMode:          false,
		LabIP:            "",
		BodyCaptureLimit: 8 * 1024 * 1024,

		DBHost:            "127.0.0.1",
		DBPort:            3306,
		DBUser:            "mockc2",
		DBPassword:        "mockc2pass",
		DBName:            "mockc2",
		DBParams:          "",
		DBMaxOpenConns:    32,
		DBMaxIdleConns:    8,
		DBConnMaxLifetime: 30,
	}
}

// ResolveRoot 决定项目根：MOCKC2_ROOT > 当前工作目录。
func ResolveRoot() string {
	if r := os.Getenv("MOCKC2_ROOT"); r != "" {
		if abs, err := filepath.Abs(r); err == nil {
			return abs
		}
		return r
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// LabOrigin lab 入口 URL（改写目标）。
func (s *Settings) LabOrigin() string {
	ip := s.LabIP
	if ip == "" {
		ip = DetectLocalIP()
	}
	return "http://" + ip + ":" + strconv.Itoa(s.HTTPPort)
}

// loadOverrides 加载 mockc2_settings.json + MOCKC2_* env（小写化 key，env 覆盖 json）。
func loadOverrides(root string) map[string]any {
	out := map[string]any{}
	cfgFile := filepath.Join(root, "mockc2_settings.json")
	if b, err := os.ReadFile(cfgFile); err == nil {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			for k, v := range m {
				out[strings.ToLower(k)] = v
			}
		}
	}
	const prefix = "MOCKC2_"
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		key := strings.ToLower(k[len(prefix):])
		var val any = v
		if strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") {
			var jv any
			if json.Unmarshal([]byte(v), &jv) == nil {
				val = jv
			}
		}
		out[key] = val
	}
	return out
}

// Load 装配默认值 + 覆盖。
func Load() *Settings {
	s := Default()
	applyOverrides(s, loadOverrides(s.RootDir))
	return s
}

func applyOverrides(s *Settings, o map[string]any) {
	for k, v := range o {
		switch k {
		case "host":
			s.Host = asString(v)
		case "http_port":
			s.HTTPPort = asInt(v, s.HTTPPort)
		case "https_port":
			s.HTTPSPort = asInt(v, s.HTTPSPort)
		case "cache_control":
			s.CacheControl = asString(v)
		case "server_header":
			s.ServerHeader = asString(v)
		case "log_sink_status":
			s.LogSinkStatus = asInt(v, s.LogSinkStatus)
		case "ops_token":
			s.OpsToken = asString(v)
		case "admin_user":
			s.AdminUser = asString(v)
		case "admin_password":
			s.AdminPassword = asString(v)
		case "admin_path":
			s.AdminPath = asString(v)
		case "admin_session_ttl":
			s.AdminSessionTTL = asInt(v, s.AdminSessionTTL)
		case "cert_domains":
			s.CertDomains = asStringSlice(v)
		case "lab_mode":
			s.LabMode = asBool(v)
		case "lab_ip":
			s.LabIP = asString(v)
		case "body_capture_limit":
			s.BodyCaptureLimit = asInt(v, s.BodyCaptureLimit)
		case "db_host":
			s.DBHost = asString(v)
		case "db_port":
			s.DBPort = asInt(v, s.DBPort)
		case "db_user":
			s.DBUser = asString(v)
		case "db_password":
			s.DBPassword = asString(v)
		case "db_name":
			s.DBName = asString(v)
		case "db_params":
			s.DBParams = asString(v)
		case "db_max_open_conns":
			s.DBMaxOpenConns = asInt(v, s.DBMaxOpenConns)
		case "db_max_idle_conns":
			s.DBMaxIdleConns = asInt(v, s.DBMaxIdleConns)
		case "db_conn_max_lifetime":
			s.DBConnMaxLifetime = asInt(v, s.DBConnMaxLifetime)
		case "db_max_conns":
			// 兼容旧键：等价于连接池上限
			s.DBMaxOpenConns = asInt(v, s.DBMaxOpenConns)
		case "session_secret":
			s.SessionSecret = asString(v)
		case "instance_id":
			s.InstanceID = asString(v)
		case "beacon_noop":
			if m, ok := v.(map[string]any); ok {
				s.BeaconNoop = m
			}
		case "root_alias":
			if sl := asStringSlice(v); len(sl) > 0 {
				m := map[string]bool{}
				for _, n := range sl {
					m[n] = true
				}
				s.RootAlias = m
			}
		}
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func asInt(v any, def int) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return def
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, asString(e))
		}
		return out
	case []string:
		return t
	case string:
		if t == "" {
			return []string{}
		}
		return []string{t}
	}
	return []string{}
}

// DetectLocalIP 按 UDP 连外探测本机 LAN IP。
func DetectLocalIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return "127.0.0.1"
}

// ── 单例 ──
var (
	mu       sync.Mutex
	instance *Settings
)

// Get 返回进程内单例配置。
func Get() *Settings {
	mu.Lock()
	defer mu.Unlock()
	if instance == nil {
		instance = Load()
	}
	return instance
}
