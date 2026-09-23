// ratelimit_central_test.go pins the fleet-wide token bucket contract.
// adr: 104
package gateway

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeCentral is a CentralBackend test double. Round-trip counters
// let the tests assert "did the limiter actually call the backend".
type fakeCentral struct {
	consumeCalls atomic.Int64
	peekCalls    atomic.Int64
	invalCalls   atomic.Int64

	// Peek remains on the interface for diagnostic compatibility, while
	// admission tests exercise ConsumeToken exclusively.
	peekResult    func() (int, error)
	consumeResult func() (int, bool, error)
}

func newFakeCentral() *fakeCentral {
	return &fakeCentral{}
}

func (f *fakeCentral) ConsumeToken(_ context.Context, _, _, _ string, _, _ float64) (int, bool, error) {
	f.consumeCalls.Add(1)
	if f.consumeResult != nil {
		return f.consumeResult()
	}
	return 0, true, nil
}

func (f *fakeCentral) PeekToken(_ context.Context, _, _, _ string) (int, error) {
	f.peekCalls.Add(1)
	if f.peekResult != nil {
		return f.peekResult()
	}
	return 0, nil
}

func (f *fakeCentral) Invalidate(_, _, _ string) {
	f.invalCalls.Add(1)
}

func TestLimiter_NoopBackend_NeverConsultsCentral(t *testing.T) {
	// Production default — Limiter built with NewLimiter has a
	// noopCentralBackend that never reaches Postgres. The
	// isNoopBackend shortcut must short-circuit the central
	// consume so a single-box dev cluster never pays a
	// goroutine-y cost on the central path.
	l := NewLimiter()
	for i := 0; i < 100; i++ {
		if !l.Allow(context.Background(), "appid", api.PlanHobby) {
			t.Fatalf("hobby plan rejected admit #%d (back-compat: noop backend never blocks)", i)
		}
	}
	// Pin: the default backend is the noop.
	if !l.isNoopBackend() {
		t.Error("isNoopBackend: false on NewLimiter (default must be noop)")
	}
	// Pin: a Limiter built with NewLimiterWithCentral(nil)
	// also defaults to the noop — nil backend MUST NOT cause a
	// nil-pointer dereference.
	l2 := NewLimiterWithCentral(nil)
	if !l2.isNoopBackend() {
		t.Error("isNoopBackend: false on NewLimiterWithCentral(nil) (nil backend must fall back to noop)")
	}
}

func TestLimiter_RealBackend_ConsumesEveryRequest(t *testing.T) {
	fake := newFakeCentral()
	frozen := time.Unix(1_700_000_000, 0)
	l := NewLimiterWithCentralAndClock(fake, func() time.Time { return frozen })
	const rps, burst = 20.0, 100.0
	const centralKey = "app:00000000-0000-0000-0000-000000000001:hobby"
	for i := 0; i < 100; i++ {
		if !l.AllowWithCentralParams(context.Background(), "appid", rps, burst, centralKey) {
			t.Fatalf("central admit #%d rejected", i)
		}
	}
	if got := fake.consumeCalls.Load(); got != 100 {
		t.Fatalf("consume calls=%d, want 100", got)
	}
	fake.consumeResult = func() (int, bool, error) { return 0, false, nil }
	if l.AllowWithCentralParams(context.Background(), "appid", rps, burst, centralKey) {
		t.Error("request admitted despite authoritative central rejection")
	}
}

func TestLimiter_RealBackend_MirrorsAuthoritativeRemainingForHeaders(t *testing.T) {
	fake := newFakeCentral()
	fake.consumeResult = func() (int, bool, error) { return 7, true, nil }
	frozen := time.Unix(1_700_000_000, 0)
	l := NewLimiterWithCentralAndClock(fake, func() time.Time { return frozen })

	if !l.AllowWithCentralParams(context.Background(), "route", 10, 20, "rule:00000000-0000-0000-0000-000000000001:hobby") {
		t.Fatal("authoritative central admit rejected")
	}
	limit, remaining, _, ok := l.PeekWithParams("route", 10, 20)
	if !ok || limit != 20 || remaining != 7 {
		t.Fatalf("header mirror=(limit=%d remaining=%d ok=%v), want (20,7,true)", limit, remaining, ok)
	}
}

func TestLimiter_RealBackend_PGErrorDegradesSoft(t *testing.T) {
	// Postgres unreachable: ConsumeToken returns an error, so the limiter
	// returns false (preserves local reject decision) without panicking.
	fake := newFakeCentral()
	fake.consumeResult = func() (int, bool, error) { return 0, false, errors.New("postgres down") }
	// Frozen clock so the burst test doesn't refill between
	// iterations — we need a deterministic drain.
	frozen := time.Unix(1_700_000_000, 0)
	l := NewLimiterWithCentralAndClock(fake, func() time.Time { return frozen })
	var observed atomic.Int64
	l.centralErrorObserver = func(_ context.Context, scope string, err error) {
		if scope != rateLimitScopeApp || err == nil {
			t.Errorf("central fallback observation = (scope=%q, err=%v), want (app, non-nil)", scope, err)
		}
		observed.Add(1)
	}

	// Scale-shaped bucket: 1500 rps, 3000 burst (per
	// pkg/api/limits.go Scale plan). Drain under frozen clock,
	// then a single call must reject (degraded posture
	// preserves the local reject).
	const rps, burst = 1500.0, 3000.0
	const centralKey = "app:00000000-0000-0000-0000-000000000002:scale"
	for i := 0; i < 3000; i++ {
		l.AllowWithCentralParams(context.Background(), "appid", rps, burst, centralKey)
	}
	if l.AllowWithCentralParams(context.Background(), "appid", rps, burst, centralKey) {
		t.Error("scale admit accepted despite PG error (degraded posture must preserve local reject)")
	}
	if got := fake.consumeCalls.Load(); got == 0 {
		t.Error("consume calls during PG-error test = 0")
	}
	if got, want := observed.Load(), fake.consumeCalls.Load(); got != want {
		t.Errorf("degraded observations=%d, want one per failed consume (%d)", got, want)
	}
}

func TestHandler_CentralFallbackIsObservableAndAuditCooledDown(t *testing.T) {
	fake := newFakeCentral()
	fake.consumeResult = func() (int, bool, error) { return 0, false, errors.New("postgres down") }
	m := NewMetrics()
	var logs bytes.Buffer
	audit := &captureAuditor{}
	h := NewHandlerWith(nil, m, slog.New(slog.NewJSONHandler(&logs, nil))).
		WithRequireAuthn(nil, audit).
		WithCentralBackend(fake)
	const centralKey = "app:00000000-0000-0000-0000-000000000002:hobby"
	for i := 0; i < 2; i++ {
		if !h.limiter.AllowWithCentralParams(t.Context(), "app-1", 10, 10, centralKey) {
			t.Fatalf("local fallback rejected request %d", i+1)
		}
	}

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if want := `gateway_ratelimit_degraded_total{scope="app"} 2`; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("metrics missing %q:\n%s", want, rec.Body.String())
	}
	if got := strings.Count(logs.String(), "gateway rate limiter fell back"); got != 1 {
		t.Errorf("degraded warning count=%d, want 1 within cooldown; logs=%s", got, logs.String())
	}
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if len(audit.captured) != 1 || audit.captured[0].kind != "ratelimit_degraded" {
		t.Fatalf("degraded audits=%+v, want one ratelimit_degraded event", audit.captured)
	}
}

func TestLimiter_TwoReplicasShareOneRouteBurst(t *testing.T) {
	tokens := atomic.Int64{}
	tokens.Store(1)
	fake := newFakeCentral()
	fake.consumeResult = func() (int, bool, error) {
		for {
			current := tokens.Load()
			if current <= 0 {
				return 0, false, nil
			}
			if tokens.CompareAndSwap(current, current-1) {
				return int(current - 1), true, nil
			}
		}
	}
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	replicas := []*Limiter{
		NewLimiterWithCentralAndClock(fake, now),
		NewLimiterWithCentralAndClock(fake, now),
	}
	admitted := 0
	for i, limiter := range replicas {
		if limiter.AllowWithCentralParams(context.Background(), "route", 1, 1, "rule:00000000-0000-0000-0000-000000000001:hobby") {
			admitted++
		}
		_ = i
	}
	if admitted != 1 {
		t.Fatalf("admitted=%d across two replicas, want 1", admitted)
	}
}

func TestSplitCentralKey_AcceptsValidTriples(t *testing.T) {
	cases := []struct {
		in        string
		scope     string
		subjectID string
		plan      string
	}{
		{"app:00000000-0000-0000-0000-000000000001:hobby", "app", "00000000-0000-0000-0000-000000000001", "hobby"},
		{"account:00000000-0000-0000-0000-000000000002:scale", "account", "00000000-0000-0000-0000-000000000002", "scale"},
		{"rule:00000000-0000-0000-0000-000000000003:pro", "rule", "00000000-0000-0000-0000-000000000003", "pro"},
	}
	for _, c := range cases {
		s, sid, p, ok := splitCentralKey(c.in)
		if !ok {
			t.Errorf("splitCentralKey(%q): !ok", c.in)
			continue
		}
		if s != c.scope || sid != c.subjectID || p != c.plan {
			t.Errorf("splitCentralKey(%q): got (%q, %q, %q), want (%q, %q, %q)", c.in, s, sid, p, c.scope, c.subjectID, c.plan)
		}
	}
}

func TestSplitCentralKey_RejectsMalformed(t *testing.T) {
	bad := []string{
		"",          // empty
		"app",       // one segment
		"app:hobby", // two segments
		":00000000-0000-0000-0000-000000000001:hobby", // empty scope
		"app::hobby", // empty subject_id
		"app:00000000-0000-0000-0000-000000000001:",        // empty plan
		"app:00000000-0000-0000-0000-000000000001:unknown", // unknown plan
		"route:00000000-0000-0000-0000-000000000001:hobby", // unknown scope (typo: 'route' is Phase 3 *action* vocab)
	}
	for _, c := range bad {
		if _, _, _, ok := splitCentralKey(c); ok {
			t.Errorf("splitCentralKey(%q): want !ok", c)
		}
	}
}

func TestSplitCentralKey_RejectsUnknownPlan(t *testing.T) {
	// Defence-in-depth: even if a caller passes a closed-vocab
	// scope, an unknown plan must be rejected so a future
	// "enterprise" plan addition isn't silently bypassed.
	for _, plan := range []string{"", "starter", "business", "ENTERPRISE"} {
		key := "app:00000000-0000-0000-0000-000000000001:" + strings.ToLower(plan)
		if _, _, _, ok := splitCentralKey(key); ok {
			t.Errorf("splitCentralKey(%q): accepted unknown plan %q", key, plan)
		}
	}
}

func TestLimiter_AllowWithCentralParams_NoCentralKey_NoConsult(t *testing.T) {
	// Empty centralKey preserves local-only behavior for callers that have not
	// opted into a centrally coordinated scope.
	fake := newFakeCentral()
	l := NewLimiterWithCentral(fake)
	for i := 0; i < 50; i++ {
		l.AllowWithCentralParams(context.Background(), "appid", 50, 100, "")
	}
	if got := fake.consumeCalls.Load(); got != 0 {
		t.Errorf("consume calls with empty centralKey = %d, want 0", got)
	}
}
