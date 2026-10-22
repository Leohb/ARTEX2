//go:build ignore

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 走真实 HTTP 入口验证限流接线：authLogin 在解析完请求后、查库之前就会检查限流，
// 所以这里不需要数据库。连续打满阈值后必须开始回 429，且带 Retry-After。
func TestAuthLoginRateLimitsOverHTTP(t *testing.T) {
	// 只装配 authLogin 需要的字段；pg 为 nil 会在限流之后返回 503，
	// 这恰好证明「限流在查库之前生效」。
	s := &Server{m: &Manager{}}

	do := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/auth/login",
			strings.NewReader(`{"username":"ARTEX","password":"wrongpassword"}`))
		req.RemoteAddr = "203.0.113.9:40000"
		w := httptest.NewRecorder()
		s.authLogin(w, req)
		return w
	}

	// 先把计数打到阈值：前几次因 pg 为 nil 返回 503，但仍应记录失败吗？
	// 实际上 503 分支在 fail() 之前返回，所以不会计数——这正是我们要的语义：
	// 数据源故障不该被算作密码错误。这里直接驱动限流器状态来验证接线。
	for i := 0; i < loginMaxFailures; i++ {
		s.loginThrottle.fail("203.0.113.9")
	}

	w := do()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after throttle engaged, got %d %s", w.Code, w.Body.String())
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Fatal("429 response missing Retry-After header")
	}
}

// 数据源故障（503）不得计入登录失败次数——否则数据库抖动会把管理员锁在门外。
func TestAuthLoginDataSourceFailureDoesNotCountAsBadPassword(t *testing.T) {
	s := &Server{m: &Manager{}}
	for i := 0; i < loginMaxFailures*2; i++ {
		req := httptest.NewRequest("POST", "/api/auth/login",
			strings.NewReader(`{"username":"ARTEX","password":"whatever1"}`))
		req.RemoteAddr = "203.0.113.10:40000"
		w := httptest.NewRecorder()
		s.authLogin(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 with no DB, got %d", w.Code)
		}
	}
	// 全程没被限流 = 503 没有计数
	if _, ok := s.loginThrottle.allow("203.0.113.10"); !ok {
		t.Fatal("DB outage wrongly counted as failed login attempts")
	}
}
