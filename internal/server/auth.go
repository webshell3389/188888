package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mockc2/internal/config"
)

// 管理后台授权：口令登录 → 无状态签名 token（HMAC-SHA256）。
//
// 多实例要点：会话不再放进程内 map，而是签名 token（sid|exp + HMAC）。
// 所有实例用同一密钥（config.SessionKey：session_secret 或由 admin_password 派生）
// 即可互相校验，因此经负载均衡打到任意 worker 都不会 401。
// 滑动续期：校验通过且已过半程时重发 cookie。

const adminCookie = "c2mock_admin"

func signToken(sid string, exp int64) string {
	payload := sid + "|" + strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, config.Get().SessionKey())
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func parseToken(tok string) (sid string, exp int64, ok bool) {
	i := strings.LastIndex(tok, ".")
	if i < 0 {
		return "", 0, false
	}
	payload, sig := tok[:i], tok[i+1:]
	mac := hmac.New(sha256.New, config.Get().SessionKey())
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return "", 0, false
	}
	parts := strings.SplitN(payload, "|", 2)
	if len(parts) != 2 {
		return "", 0, false
	}
	e, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return parts[0], e, true
}

// AdminLogin 校验账号+口令，成功返回签名 token。
// 兼容面：username 为空按配置账号处理（仅口令的历史调用方/smoke 工具不受影响）。
func AdminLogin(username, password string) (string, bool) {
	s := config.Get()
	u := strings.TrimSpace(username)
	if u == "" {
		u = s.AdminUser
	}
	if u != s.AdminUser || password != s.AdminPassword {
		return "", false
	}
	sid := randomSessionID()
	exp := time.Now().Add(time.Duration(s.AdminSessionTTL) * time.Second).Unix()
	return signToken(sid, exp), true
}

// AdminValidate 校验签名与有效期（无状态，任意实例可校验）。
func AdminValidate(token string) bool {
	_, exp, ok := parseToken(token)
	if !ok {
		return false
	}
	return time.Now().Unix() < exp
}

// AdminLogout 无状态会话无需服务端注销（客户端清 cookie 即可）。
func AdminLogout(token string) {}

// setSessionCookie 下发/刷新会话 cookie。
func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   config.Get().AdminSessionTTL,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie 清除会话 cookie。
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func randomSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// 受守卫前缀。入库解析面（照片/统计）属于敏感数据，必须与会话一致地要求会话；
// 交付链的 /api/ip-sync/sync 不在此列，不能被守卫。
var protectedPrefixes = []string{
	"/__dash", "/__q", "/__control", "/__auth/me", "/panel", "/__metrics",
	"/api/data", "/api/file", "/api/stats",
}

// adminAuthMiddleware 守卫前置，命中 401 JSON；/__auth/login 放行；滑动续期。
func adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		protected := false
		for _, p := range protectedPrefixes {
			if path == p || strings.HasPrefix(path, p+"/") || strings.HasPrefix(path, p+"?") {
				protected = true
				break
			}
		}
		loginAllowed := strings.HasPrefix(path, "/__auth/login") || strings.HasPrefix(path, config.Get().AdminPath)
		if protected && !loginAllowed {
			tok := ""
			if c, err := r.Cookie(adminCookie); err == nil {
				tok = c.Value
			}
			sid, exp, ok := parseToken(tok)
			if !ok || time.Now().Unix() >= exp {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": "unauthorized", "hint": "POST /__auth/login"})
				return
			}
			// 滑动续期：过半程即重发
			ttl := int64(config.Get().AdminSessionTTL)
			if exp-time.Now().Unix() < ttl-ttl/4 {
				setSessionCookie(w, signToken(sid, time.Now().Unix()+ttl))
			}
		}
		next.ServeHTTP(w, r)
	})
}
