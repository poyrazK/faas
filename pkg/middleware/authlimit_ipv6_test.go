package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A subscriber is usually delegated a whole IPv6 /64. Keying the auth
// limiter on the full address let a client rotate its interface ID and
// get a fresh failure budget on every attempt.
func TestAuthLimitBucketsIPv6By64(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := AuthLimit(AuthLimitConfig{Window: time.Minute, MaxFailures: 3, Now: func() time.Time { return now }})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	try := func(remote string) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 1; i <= 3; i++ {
		if code := try(fmt.Sprintf("[2001:db8:1:2::%x]:443", i)); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i, code)
		}
	}
	if code := try("[2001:db8:1:2:ffff::9]:443"); code != http.StatusTooManyRequests {
		t.Fatalf("fourth attempt from the same /64 with a new interface ID = %d, want 429", code)
	}
	if code := try("[2001:db8:1:3::1]:443"); code != http.StatusUnauthorized {
		t.Fatalf("a different /64 = %d, want its own budget (401)", code)
	}
	if code := try("198.51.100.7:443"); code != http.StatusUnauthorized {
		t.Fatalf("IPv4 client = %d, want its own budget (401)", code)
	}
}

func TestLimiterKey(t *testing.T) {
	for in, want := range map[string]string{
		"198.51.100.7":            "198.51.100.7",
		"::ffff:198.51.100.7":     "198.51.100.7",
		"2001:db8:1:2::1":         "2001:db8:1:2::/64",
		"2001:0db8:0001:0002:0::": "2001:db8:1:2::/64",
		"unknown":                 "unknown",
	} {
		if got := limiterKey(in); got != want {
			t.Errorf("limiterKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// One-shot failures from many clients were only pruned when the same
// client returned, so the map grew without bound.
func TestAuthLimiterSweepsExpiredKeys(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := &authLimiter{cfg: AuthLimitConfig{Window: time.Minute, MaxFailures: 10}}
	for i := 0; i < limiterSweepThreshold; i++ {
		l.recordFailure(fmt.Sprintf("198.51.%d.%d", i/250, i%250), now)
	}
	later := now.Add(2 * time.Minute)
	l.recordFailure("203.0.113.1", later)
	if len(l.failures) != 1 {
		t.Fatalf("tracked keys after the window = %d, want 1", len(l.failures))
	}
}
