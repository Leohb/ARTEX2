package server

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	jwtKeyFilename = "jwt.key"
	authPassKey    = "auth.password_hash"
	jwtTTL         = 7 * 24 * time.Hour
	keyChars       = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// 下限与 setup 页的前端校验一致——校验只放在前端等于没放，直接打 API 就能
	// 绕过。上限是 bcrypt 的硬限制：超过 72 字节 GenerateFromPassword 会返回
	// ErrPasswordTooLong，提前挡掉好过让用户收到一句含义不明的「密码加密失败」。
	minPasswordRunes = 8
	maxPasswordBytes = 72
)

// errDataSourceUnavailable 是密码相关读操作失败时统一的回复。这些 handler 绝不能
// 把"读不到"当成"没有设置"：authInit 曾因此在数据库报错时放行，让未认证请求覆盖
// 掉已有的管理员密码。
const errDataSourceUnavailable = "数据源暂时不可用，请稍后重试"

// validatePassword 返回空串表示通过，否则返回可直接展示给用户的中文原因。
func validatePassword(pw string) string {
	if utf8.RuneCountInString(pw) < minPasswordRunes {
		return fmt.Sprintf("密码长度至少 %d 位", minPasswordRunes)
	}
	if len(pw) > maxPasswordBytes {
		return fmt.Sprintf("密码长度不能超过 %d 字节", maxPasswordBytes)
	}
	return ""
}

// loadOrCreateJWTKey reads the 32-byte signing key from keyDir/jwt.key. keyDir is
// the project base dir (next to the executable), NOT the browsable workspace root
// (dataDir) — the signing key must never be listable/downloadable via the file
// manager. Legacy installs kept it at dataDir/jwt.key; if present there and not yet
// at the new location, it is migrated (key preserved, so sessions stay valid) and
// the old file removed so it disappears from the workspace. On first run a random
// key is generated and persisted.
func loadOrCreateJWTKey(keyDir, dataDir string) ([]byte, error) {
	path := filepath.Join(keyDir, jwtKeyFilename)
	// one-time migration out of the old in-workspace location.
	if legacy := filepath.Join(dataDir, jwtKeyFilename); legacy != path {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if data, rerr := os.ReadFile(legacy); rerr == nil {
				if werr := os.WriteFile(path, data, 0o600); werr == nil {
					_ = os.Remove(legacy)
					log.Printf("[auth] JWT key 已从 %s 迁移到 %s（移出可浏览工作区）", legacy, path)
				}
			}
		}
	}
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) >= 32 {
		return []byte(strings.TrimSpace(string(data))), nil
	}
	buf := make([]byte, 32)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(keyChars))))
		if err != nil {
			return nil, fmt.Errorf("generate jwt key: %w", err)
		}
		buf[i] = keyChars[n.Int64()]
	}
	if err := os.WriteFile(path, buf, 0600); err != nil {
		return nil, fmt.Errorf("write jwt key: %w", err)
	}
	log.Printf("[auth] 新 JWT key 已写入 %s", path)
	return buf, nil
}

// signJWT issues a 7-day HS256 token for user ARTEX.
func signJWT(key []byte) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "ARTEX",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(jwtTTL)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}).SignedString(key)
}

// verifyJWT returns true when tokenStr is a valid, non-expired HS256 token.
func verifyJWT(tokenStr string, key []byte) bool {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return key, nil
	})
	return err == nil && t.Valid
}

// extractToken reads the JWT from Authorization: Bearer header,
// artex_token cookie, or ?token= query param (for SSE connections).
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie("artex_token"); err == nil && c.Value != "" {
		return c.Value
	}
	return r.URL.Query().Get("token")
}

// requireAuth wraps h with JWT validation.
// /api/auth/* and /api/health are exempt.
func (s *Server) requireAuth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/auth/") || p == "/api/health" {
			h.ServeHTTP(w, r)
			return
		}
		tok := extractToken(r)
		if tok == "" {
			writeErr(w, 401, "未授权")
			return
		}
		if !verifyJWT(tok, s.jwtKey) {
			writeErr(w, 401, "token 无效或已过期")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// GET /api/auth/status — reports whether the admin password has been initialised.
// 读失败必须回 503 而不是 initialized:false：前端在 initialized:false 时会把用户
// 送到 /setup 去设置密码（login/page.tsx），把数据库故障包装成 200 等于把用户往
// 覆盖已有密码的路上推。
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	hash, _, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	writeJSON(w, 200, map[string]any{"initialized": hash != ""})
}

// POST /api/auth/init — sets the password for the first time; rejected if already set.
func (s *Server) authInit(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	existing, _, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if existing != "" {
		writeErr(w, 403, "密码已设置")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil || req.Password == "" {
		writeErr(w, 400, "密码不能为空")
		return
	}
	if msg := validatePassword(req.Password); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "密码加密失败")
		return
	}
	// 用 INSERT ... ON CONFLICT DO NOTHING 而不是 upsert：上面那次 GetSetting 只是
	// 快速失败路径，真正"仅首次可设"的保证落在主键约束上。bcrypt 要跑几十毫秒，
	// 这期间别的请求完全可能先把密码设好，而读检查本身也可能因故障而失效。
	inserted, err := pg.InsertSettingIfAbsent(authPassKey, string(hash))
	if err != nil {
		writeErr(w, 500, "保存失败: "+err.Error())
		return
	}
	if !inserted {
		writeErr(w, 403, "密码已设置")
		return
	}
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		writeErr(w, 500, "token 生成失败")
		return
	}
	writeJSON(w, 200, map[string]any{"token": tok})
}

// POST /api/auth/change-password — changes the admin password. Requires a valid
// token (this route is under /api/auth/* which requireAuth exempts, so the token
// is validated here) AND the current password.
func (s *Server) authChangePassword(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if !verifyJWT(extractToken(r), s.jwtKey) {
		writeErr(w, 401, "未授权")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if req.NewPassword == "" {
		writeErr(w, 400, "新密码不能为空")
		return
	}
	if msg := validatePassword(req.NewPassword); msg != "" {
		writeErr(w, 400, msg)
		return
	}
	hash, ok, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if !ok || hash == "" {
		writeErr(w, 403, "密码未初始化，请先设置密码")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.OldPassword)); err != nil {
		writeErr(w, 401, "当前密码错误")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "密码加密失败")
		return
	}
	if err := pg.SetSetting(authPassKey, string(newHash)); err != nil {
		writeErr(w, 500, "保存失败: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// POST /api/auth/login — validates username/password and returns a JWT.
//
// 失败尝试按客户端地址计数并退避：用户名固定为 ARTEX、密码下限 8 位，bcrypt
// 只挡住了离线爆破，在线撞库此前可以无限次尝试。计数在成功登录后清零，因此
// 正常用户不会因为别人的错误尝试被永久锁在外面。
func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	// 限流必须在查库之前：一是否则数据库故障时 503 会绕过限流，让攻击者
	// 在 DB 抖动期间获得无限次尝试（虽然那时本来也验不了密码）；二是限流
	// 不该依赖后端可用性——它是纯粹的本地资源保护。
	addr := clientAddr(r)
	if retryAfter, ok := s.loginThrottle.allow(addr); !ok {
		secs := int(retryAfter.Seconds() + 0.5)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeErr(w, 429, fmt.Sprintf("登录尝试过于频繁，请 %d 秒后再试", secs))
		return
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if req.Username != "ARTEX" {
		s.loginThrottle.fail(addr)
		writeErr(w, 401, "用户名或密码错误")
		return
	}
	hash, ok, err := pg.GetSetting(authPassKey)
	if err != nil {
		writeErr(w, 503, errDataSourceUnavailable)
		return
	}
	if !ok || hash == "" {
		writeErr(w, 403, "密码未初始化，请先设置密码")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		s.loginThrottle.fail(addr)
		writeErr(w, 401, "用户名或密码错误")
		return
	}
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		writeErr(w, 500, "token 生成失败")
		return
	}
	s.loginThrottle.reset(addr)
	writeJSON(w, 200, map[string]any{"token": tok})
}

// --- 登录尝试限流 ---------------------------------------------------------
//
// 目标不是挡住分布式撞库（那需要账号级或全局配额，单机无法可靠区分），而是
// 让「从一台机器连续猜密码」这件事从「无限次」变成「有成本」。策略：同一地址
// 连续失败 loginMaxFailures 次后开始退避，退避时长按失败次数线性增长并封顶。

const (
	// loginMaxFailures 是触发退避前允许的连续失败次数。留几次余量是为了不让
	// 手误输错密码的正常用户立刻被拦。
	loginMaxFailures = 5
	// loginBaseBackoff 是第一次触发退避时的等待时长，之后每次失败翻倍。
	loginBaseBackoff = 2 * time.Second
	// loginMaxBackoff 封顶。上限存在的意义：退避不该长到让攻击者「等一会再来」
	// 变成一件划算的事，也不该长到需要管理员介入才能恢复。
	loginMaxBackoff = 5 * time.Minute
	// loginAttemptTTL 后没有任何新失败就清掉这条记录，避免 map 无限增长。
	loginAttemptTTL = 30 * time.Minute
)

// loginAttemptState 记录单个客户端地址的连续失败情况。
type loginAttemptState struct {
	failures int
	// nextAllowed 之前不接受新的尝试；零值表示立即可试。
	nextAllowed time.Time
	// lastSeen 用于惰性清理。
	lastSeen time.Time
}

// loginThrottle 是按客户端地址计数的登录限流器。零值可用。
type loginThrottle struct {
	mu   sync.Mutex
	byIP map[string]*loginAttemptState
}

// clientAddr 取限流用的客户端标识。刻意不信任 X-Forwarded-For：那是攻击者
// 可控的请求头，信任它等于把限流变成可绕过的装饰。反向代理部署下所有请求会
// 共用一个地址计数，这是保守方向的偏差（宁可误伤也不漏放）。
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allow 返回是否放行当前尝试；不放行时同时给出建议的等待时长。
func (t *loginThrottle) allow(addr string) (time.Duration, bool) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	st := t.byIP[addr]
	if st == nil {
		return 0, true
	}
	if now.Before(st.nextAllowed) {
		return st.nextAllowed.Sub(now), false
	}
	return 0, true
}

// fail 记一次失败，并在超过阈值后设置退避窗口。
func (t *loginThrottle) fail(addr string) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(now)
	if t.byIP == nil {
		t.byIP = map[string]*loginAttemptState{}
	}
	st := t.byIP[addr]
	if st == nil {
		st = &loginAttemptState{}
		t.byIP[addr] = st
	}
	st.failures++
	st.lastSeen = now
	if st.failures >= loginMaxFailures {
		// 指数退避：首次触发等 base，之后每次翻倍，封顶 maxBackoff。
		// 用循环而不是移位：移位在次数很大时会在 int64 上溢出成负数，还得额外
		// 写一段“溢出就当封顶”的特例；循环每一档都实际可达，封顶值也真能碰上。
		back := loginBaseBackoff
		for i := 0; i < st.failures-loginMaxFailures; i++ {
			if back >= loginMaxBackoff {
				break
			}
			back *= 2
		}
		if back > loginMaxBackoff {
			back = loginMaxBackoff
		}
		st.nextAllowed = now.Add(back)
	}
}

// reset 在登录成功后清掉该地址的失败记录。
func (t *loginThrottle) reset(addr string) {
	t.mu.Lock()
	delete(t.byIP, addr)
	t.mu.Unlock()
}

// pruneLocked 惰性清理过期记录。调用方必须已持有 t.mu。
func (t *loginThrottle) pruneLocked(now time.Time) {
	if len(t.byIP) == 0 {
		return
	}
	for addr, st := range t.byIP {
		if now.Sub(st.lastSeen) > loginAttemptTTL && !now.Before(st.nextAllowed) {
			delete(t.byIP, addr)
		}
	}
}
