//go:build ignore

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 连续失败达到阈值后必须开始退避。
func TestLoginThrottleBacksOffAfterFailures(t *testing.T) {
	var th loginThrottle
	const addr = "10.0.0.1"

	// 阈值之前：允许。
	for i := 0; i < loginMaxFailures-1; i++ {
		if _, ok := th.allow(addr); !ok {
			t.Fatalf("blocked too early at failure %d", i)
		}
		th.fail(addr)
	}
	// 第 loginMaxFailures 次失败触发退避。
	th.fail(addr)
	wait, ok := th.allow(addr)
	if ok {
		t.Fatal("still allowed after reaching failure threshold")
	}
	if wait <= 0 {
		t.Fatalf("expected positive wait, got %v", wait)
	}
	if wait > loginMaxBackoff {
		t.Fatalf("wait %v exceeds cap %v", wait, loginMaxBackoff)
	}
}

// 退避时长必须随失败次数增长，且封顶。
//
// 注意：allow() 返回的是「还需等多久」（now → nextAllowed 的剩余），它会随真实
// 时间流逝而变小，不能直接当作退避档位比较。这里直接读状态里的 nextAllowed。
func TestLoginThrottleBackoffGrowsAndCaps(t *testing.T) {
	var th loginThrottle
	const addr = "10.0.0.2"

	// 2s → 4 → 8 → 16 → 32 → 64 → 128 → 256(4m16s) → 封顶 5m。
	// 需要走到第 loginMaxFailures+9 次失败才能确认真的停在封顶值上。
	const extra = 12
	prevBackoff := time.Duration(0)
	started := false
	for i := 0; i < loginMaxFailures+extra; i++ {
		th.fail(addr)

		th.mu.Lock()
		st := th.byIP[addr]
		if st == nil {
			th.mu.Unlock()
			t.Fatal("state missing after fail")
		}
		failures := st.failures
		// nextAllowed 零值 = 尚未进入退避，不能拿来相减。
		hasBackoff := !st.nextAllowed.IsZero()
		backoff := st.nextAllowed.Sub(st.lastSeen)
		th.mu.Unlock()

		if failures < loginMaxFailures {
			if hasBackoff {
				t.Fatalf("backoff applied too early at failure %d", failures)
			}
			continue
		}
		started = true
		if backoff <= 0 {
			t.Fatalf("expected positive backoff at failure %d, got %v", failures, backoff)
		}
		if backoff > loginMaxBackoff {
			t.Fatalf("backoff %v exceeds cap %v", backoff, loginMaxBackoff)
		}
		if backoff < prevBackoff {
			t.Fatalf("backoff shrank at failure %d: %v < %v", failures, backoff, prevBackoff)
		}
		prevBackoff = backoff
	}
	if !started {
		t.Fatal("backoff never engaged")
	}
	if prevBackoff != loginMaxBackoff {
		t.Fatalf("expected to reach cap %v, got %v", loginMaxBackoff, prevBackoff)
	}
}

// 成功登录必须清空计数，正常用户不该被永久锁死。
func TestLoginThrottleResetClearsFailures(t *testing.T) {
	var th loginThrottle
	const addr = "10.0.0.3"
	for i := 0; i < loginMaxFailures+3; i++ {
		th.fail(addr)
	}
	if _, ok := th.allow(addr); ok {
		t.Fatal("expected to be throttled")
	}
	th.reset(addr)
	if _, ok := th.allow(addr); !ok {
		t.Fatal("reset did not clear throttle")
	}
}

// 不同来源地址互不影响。
func TestLoginThrottleIsPerAddress(t *testing.T) {
	var th loginThrottle
	for i := 0; i < loginMaxFailures+2; i++ {
		th.fail("10.0.0.4")
	}
	if _, ok := th.allow("10.0.0.4"); ok {
		t.Fatal("attacker address not throttled")
	}
	if _, ok := th.allow("10.0.0.5"); !ok {
		t.Fatal("unrelated address wrongly throttled")
	}
}

// clientAddr 不能信任 X-Forwarded-For（否则限流可被请求头绕过）。
func TestClientAddrIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "192.0.2.10:54321"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "5.6.7.8")
	if got := clientAddr(r); got != "192.0.2.10" {
		t.Fatalf("clientAddr = %q, want 192.0.2.10 (must not trust proxy headers)", got)
	}
}

// 无端口形式的 RemoteAddr 也要能用。
func TestClientAddrHandlesBareHost(t *testing.T) {
	r := &http.Request{RemoteAddr: "192.0.2.11"}
	if got := clientAddr(r); got != "192.0.2.11" {
		t.Fatalf("clientAddr = %q", got)
	}
}
