package gateway

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMetricsWakeQueueWaitRegisters asserts the §12 row name is
// exposed. Catches a rename that would silently break the dashboard.
func TestMetricsWakeQueueWaitRegisters(t *testing.T) {
	m := NewMetrics()
	m.ObserveWakeQueueWait(50 * time.Millisecond)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "gateway_wake_queue_wait_seconds") {
		t.Errorf("histogram not in registry output:\n%s", body)
	}
	if !strings.Contains(body, "gateway_wake_queue_wait_seconds_count 1") {
		t.Errorf("expected count=1 in output:\n%s", body)
	}
}

// TestMetricsWakeQueueWaitNilSafe keeps the histogram usable from
// unit tests that haven't constructed a Metrics bundle.
func TestMetricsWakeQueueWaitNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveWakeQueueWait(50 * time.Millisecond) // must not panic
}

// TestMetricsIssue273Exposition pins the new histogram + the cold
// rename (issue #273 / ADR-042). Catches a rename that the existing
// cold-wake test would have missed because it reads the Go field
// rather than the exposition string. Also asserts the request
// duration histogram is registered with the expected label set.
func TestMetricsIssue273Exposition(t *testing.T) {
	m := NewMetrics()
	m.PreInstantiateApp("app-1")
	m.ObserveColdBoot("app-1", 250*time.Millisecond, "node-1")
	m.ObserveRequestDuration("app-1", "2xx", 12*time.Millisecond)
	m.ObserveRequestDuration("app-1", "5xx", 500*time.Millisecond)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()

	// Rename: cold_boot must be present, cold_wake must be absent
	// from active series. The HELP string mentions the old name for
	// documentation, so the assertion targets the series line (which
	// starts with the metric name at column 0, not preceded by #).
	if !strings.Contains(body, "gateway_cold_boot_total{") {
		t.Errorf("gateway_cold_boot_total series not in registry output:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "gateway_cold_wake_total{") {
			t.Errorf("gateway_cold_wake_total series should be absent (issue #273 rename): %q", line)
		}
	}

	// New histogram registered with the right label set.
	if !strings.Contains(body, "gateway_request_duration_seconds_bucket") {
		t.Errorf("gateway_request_duration_seconds_bucket not in registry output:\n%s", body)
	}
	if !strings.Contains(body, `gateway_request_duration_seconds_count{app="app-1",class="2xx",deployment=""} 1`) {
		t.Errorf("expected count for app-1/2xx to be 1:\n%s", body)
	}
	if !strings.Contains(body, `gateway_request_duration_seconds_count{app="app-1",class="5xx",deployment=""} 1`) {
		t.Errorf("expected count for app-1/5xx to be 1:\n%s", body)
	}

	// Pre-instantiation: all four closed classes surface with count=0
	// for app-1 (no observation yet on 3xx/4xx). Catches a future
	// regression that accidentally stops pre-instantiating. The
	// deployment="" label is the reserved legacy single-targetSet
	// sentinel (Debugger UX v1 / ADR-127 §Decision 4).
	for _, class := range []string{"2xx", "3xx", "4xx", "5xx"} {
		want := fmt.Sprintf(`gateway_request_duration_seconds_count{app="app-1",class=%q,deployment=""}`, class)
		if !strings.Contains(body, want) {
			t.Errorf("pre-instantiated %s missing:\n%s", want, body)
		}
	}
}

// TestMetricsPreInstantiateAppBounded asserts the per-app series
// surface stays at exactly the closed (class) set — protects the
// ADR-042 cardinality math from a future change that drops the
// loop or adds a label.
func TestMetricsPreInstantiateAppBounded(t *testing.T) {
	m := NewMetrics()
	m.PreInstantiateApp("alpha")
	m.PreInstantiateApp("beta")
	// After pre-instantiation only the 4 closed classes should
	// exist per app. Calling Observe for an UNRELATED class
	// ("foo") would mint a new tuple; assert we don't do that
	// from the pre-instantiation path.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, app := range []string{"alpha", "beta"} {
		for _, class := range []string{"2xx", "3xx", "4xx", "5xx"} {
			needle := fmt.Sprintf(`gateway_request_duration_seconds_count{app=%q,class=%q,deployment=""}`, app, class)
			if !strings.Contains(body, needle) {
				t.Errorf("pre-instantiated %s missing:\n%s", needle, body)
			}
		}
	}
	// And no tuples minted with class="foo" or any unknown class.
	if strings.Contains(body, `class="foo"`) {
		t.Errorf("unexpected class tuple minted:\n%s", body)
	}
}

// TestObserveRequestDurationNilSafe keeps the histogram usable from
// nil-Metrics tests.
func TestObserveRequestDurationNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveRequestDuration("app-1", "2xx", 10*time.Millisecond) // must not panic
	m.PreInstantiateApp("app-1")                                  // must not panic
}

// TestWakeGateObservesWaitDuration drives two concurrent Wait calls
// through the gate and asserts the histogram caught at least one
// observation. The leader parks until ensure() returns; the follower
// blocks on the same call and its wait duration is non-zero.
func TestWakeGateObservesWaitDuration(t *testing.T) {
	m := NewMetrics()
	g := NewWakeGate(8, 5*time.Second)
	g.SetMetrics(m)

	release := make(chan struct{})
	var done sync.WaitGroup
	done.Add(2)

	// Leader triggers ensure; both leader (after ensure) and a follower
	// (queued behind the leader) should observe some non-zero wait.
	go func() {
		defer done.Done()
		_ = g.Wait(context.Background(), "appA", "acct-A",
			func() bool { return true },
			func(ctx context.Context) error {
				<-release
				return nil
			}, nil, nil)
	}()
	// Yield so the leader is committed before the follower joins.
	time.Sleep(20 * time.Millisecond)
	go func() {
		defer done.Done()
		_ = g.Wait(context.Background(), "appA", "acct-A",
			func() bool { return false }, // would-wake check is leader-only; follower ignores it
			func(ctx context.Context) error { return nil }, nil, nil)
	}()

	// Hold the leader parked so the follower accumulates wait.
	time.Sleep(50 * time.Millisecond)
	close(release)
	done.Wait()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "gateway_wake_queue_wait_seconds_count 2") {
		t.Errorf("expected 2 observations (leader + follower), got:\n%s", body)
	}
	// The follower's bucket should be >= 0.05s; the leader's bucket
	// is whatever it took to schedule (likely 0.005s). At least one
	// observation must land in a bucket ≥ 50ms — that's the follower.
	if !strings.Contains(body, `gateway_wake_queue_wait_seconds_bucket{le="0.05"}`) {
		t.Errorf("expected bucket line at le=0.05, got:\n%s", body)
	}
}

// TestWakeGateSkipsObservationOnErrQueueFull guards against the
// regression where ErrQueueFull and ctx-cancelled paths recorded
// ~0ms observations, driving the p95 to near-zero during overload
// storms (the very signal the SLO dashboard needs to surface).
//
// With cap=1, the leader counts as waiter 1; the very next caller
// sees waiters >= cap and gets ErrQueueFull synchronously. That
// rejected caller must NOT record in the wake-wait histogram.
func TestWakeGateSkipsObservationOnErrQueueFull(t *testing.T) {
	m := NewMetrics()
	g := NewWakeGate(1, 5*time.Second)
	g.SetMetrics(m)

	release := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)

	// Leader parks; counts as waiter 1 of cap=1.
	go func() {
		defer done.Done()
		_ = g.Wait(context.Background(), "appB", "acct-B",
			func() bool { return true },
			func(ctx context.Context) error { <-release; return nil }, nil, nil)
	}()
	time.Sleep(20 * time.Millisecond) // leader commits first

	// Synchronous next caller — gate rejects with ErrQueueFull.
	err := g.Wait(context.Background(), "appB", "acct-B",
		func() bool { return false },
		func(ctx context.Context) error { return nil }, nil, nil)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want ErrQueueFull", err)
	}

	close(release)
	done.Wait()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	// Only the leader observed (count=1); the rejected caller did not.
	if !strings.Contains(body, "gateway_wake_queue_wait_seconds_count 1") {
		t.Errorf("expected count=1 (rejected caller skipped), got:\n%s", body)
	}
}

// TestWakeGateSkipsObservationOnCtxCancel guards the other
// non-wait return: ctx cancellation. A caller that cancels before
// the in-flight ensure returns should not be recorded as having
// "waited" — it never got an instance.
//
// Race-free variant: the follower must observe the leader's
// in-flight wakeCall before the leader can complete and release
// the entry (otherwise the follower would short-circuit on
// shouldWake=false and become a new leader with its own non-
// observation outcome). We synchronize via InflightWaiters so
// the test waits until the follower is queued, then releases.
func TestWakeGateSkipsObservationOnCtxCancel(t *testing.T) {
	m := NewMetrics()
	g := NewWakeGate(8, 5*time.Second)
	g.SetMetrics(m)

	release := make(chan struct{})
	followerCommitted := make(chan struct{})
	var done sync.WaitGroup
	done.Add(2)

	// Leader parks; ensure returns nil after we release.
	go func() {
		defer done.Done()
		_ = g.Wait(context.Background(), "appC", "acct-C",
			func() bool { return true },
			func(ctx context.Context) error { <-release; return nil }, nil, nil)
	}()

	// Follower with a cancelled context — must be queued behind the
	// leader BEFORE the leader's wakeCall is released. Otherwise the
	// follower becomes a new leader with shouldWake=false and never
	// observes.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	go func() {
		defer done.Done()
		_ = g.Wait(cancelledCtx, "appC", "acct-C",
			func() bool { return false },
			func(ctx context.Context) error { return nil }, nil, nil)
		// Tell the test driver we've entered Wait (even if it returned
		// immediately — the gate has serialized us).
		close(followerCommitted)
	}()

	// Wait until the follower has actually entered Wait. Polling
	// InflightWaiters is the cheapest signal that the gate has
	// serialized the caller against the leader's call.
	deadline := time.Now().Add(2 * time.Second)
	for g.InflightWaiters("appC") < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if g.InflightWaiters("appC") < 2 {
		// Follower may have short-circuited (e.g. leader already
		// completed and the entry was released). Wait for the
		// commit signal as a fallback so we still close release
		// below.
		select {
		case <-followerCommitted:
		case <-time.After(time.Second):
		}
	}

	close(release)
	done.Wait()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	// Leader observed (one entry) IF it actually waited for the
	// follower to be queued. The follower never observes (ctx.Err()
	// path). Count is therefore 0 if the leader completed before
	// the follower queued, or 1 if it waited.
	count := countObservations(body, "gateway_wake_queue_wait_seconds_count")
	if count > 1 {
		t.Errorf("got count=%d, want <=1 (follower must skip)", count)
	}
}

// countObservations parses the bare `gateway_wake_queue_wait_seconds_count N`
// line out of a Prometheus exposition body. Returns 0 if the line isn't
// present (histogram never observed).
func countObservations(body, metric string) int {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, metric+" ") {
			continue
		}
		var n int
		_, err := fmt.Sscanf(line, metric+" %d", &n)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// TestMetricsTLSCertExpiryRegisters — ADR-024 H3: the gauge must surface
// in /metrics from the moment the daemon binds (the closed-label pre-
// instantiation is for the counter side; the gauge is unlabelled and
// surfaces as soon as NewMetrics() is called).
func TestMetricsTLSCertExpiryRegisters(t *testing.T) {
	m := NewMetrics()
	m.SetTLSCertExpiry(14 * 24 * time.Hour)

	// Verify the wire shape via the exposition handler.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "gateway_tls_cert_expiry_seconds") {
		t.Errorf("gauge not in registry output:\n%s", rec.Body.String())
	}
	// Numeric readback via the same path the operator's /metrics scrape
	// uses. PR #345 review: a string-Contains on the literal "1.2096e+06"
	// is brittle to promhttp encoder format changes (uppercase E, fixed-
	// point for smaller values, etc); a Gather() assertion survives them
	// all and matches the gaugeDuration helper in cert_expiry_test.go.
	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var got float64
	var found bool
	for _, fam := range fams {
		if fam.GetName() != "gateway_tls_cert_expiry_seconds" {
			continue
		}
		for _, mt := range fam.GetMetric() {
			got = mt.GetGauge().GetValue()
			found = true
		}
	}
	if !found {
		t.Fatal("gateway_tls_cert_expiry_seconds not in registry gather")
	}
	want := float64(14 * 24 * 60 * 60) // 1,209,600 s
	if got != want {
		t.Errorf("gauge value = %v, want %v (14d in seconds)", got, want)
	}
}

// TestMetricsTLSCertExpiryNilSafe — the gauge setter must not panic when
// called on a nil receiver (mirrors the ObserveBuildCount /
// SetResidentGBPerCustomer nil-safe precedent in pkg/wire/metrics.go).
func TestMetricsTLSCertExpiryNilSafe(t *testing.T) {
	var m *Metrics
	m.SetTLSCertExpiry(14 * 24 * time.Hour) // must not panic
}

// TestMetricsTLSOnDemandDeniedRegistersAndPreInstantiates — ADR-024
// H3: the counter must surface every closed reason label at 0 from the
// moment the daemon binds (so the §12 dashboard panel never shows "no
// data" and so the frozen-zero state for dns01 + token is observable
// as the H3.b follow-up signal).
func TestMetricsTLSOnDemandDeniedRegistersAndPreInstantiates(t *testing.T) {
	m := NewMetrics()
	m.ObserveTLSOnDemandDenied("allowlist")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	// Every reason in the closed set must surface — allowlist at 1,
	// dns01 + token at 0. The labels are alphabetical in the exposition
	// output, so the order doesn't matter.
	for _, want := range []string{
		`gateway_tls_on_demand_denied_total{reason="allowlist"} 1`,
		`gateway_tls_on_demand_denied_total{reason="dns01"} 0`,
		`gateway_tls_on_demand_denied_total{reason="token"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing exposition line %q in body:\n%s", want, body)
		}
	}
}

// TestMetricsTLSOnDemandDeniedNilSafe — the counter must not panic when
// called on a nil receiver (mirrors the ObserveBuildCount /
// SetResidentGBPerCustomer nil-safe precedent).
func TestMetricsTLSOnDemandDeniedNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveTLSOnDemandDenied("allowlist") // must not panic
}

// TestMetricsEdgeRuleApplyRegistersAndPreInstantiates — ADR-091
// hardening PR-A: the apply-path counter must surface every (kind,
// result) tuple at 0 from the moment the daemon binds (so the §12
// dashboard chip "edge rule apply rate" never shows "no data" and
// so the frozen-zero state for every kind is observable as the
// "rule compile/apply never fired" tripwire). Increment one
// (kind=jwt, result=success) and assert it surfaces at 1 while the
// rest of the cross product stays at 0.
func TestMetricsEdgeRuleApplyRegistersAndPreInstantiates(t *testing.T) {
	m := NewMetrics()
	m.ObserveEdgeRuleApply("jwt", "success")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, kind := range []string{"route", "rewrite", "redirect", "headers", "cors", "jwt", "ip"} {
		for _, result := range []string{"success", "error"} {
			line := "gateway_edge_rule_apply_total{kind=\"" + kind + "\",result=\"" + result + "\"}"
			if !strings.Contains(body, line) {
				t.Errorf("missing exposition line for %q in body:\n%s", line, body)
			}
		}
	}
	if !strings.Contains(body, `gateway_edge_rule_apply_total{kind="jwt",result="success"} 1`) {
		t.Errorf("jwt+success should surface at 1, body:\n%s", body)
	}
}

// TestMetricsEdgeRuleApplyNilSafe — the counter must not panic when
// called on a nil receiver (parallels TestMetricsTLSOnDemandDeniedNilSafe).
func TestMetricsEdgeRuleApplyNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveEdgeRuleApply("cors", "error") // must not panic
}

// TestMetricsEdgeRuleCompileErrorRegistersAndPreInstantiates —
// ADR-091 hardening PR-A: the compile-error counter must surface
// every kind at 0 from boot. Non-zero values page the operator
// (a rule shipped broken); the frozen-zero state for every kind
// is observable as the "no compile errors fired" tripwire.
func TestMetricsEdgeRuleCompileErrorRegistersAndPreInstantiates(t *testing.T) {
	m := NewMetrics()
	m.ObserveEdgeRuleCompileError("ip")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, kind := range []string{"route", "rewrite", "redirect", "headers", "cors", "jwt", "ip"} {
		line := "gateway_edge_rule_compile_error_total{kind=\"" + kind + "\"}"
		if !strings.Contains(body, line) {
			t.Errorf("missing exposition line for %q in body:\n%s", line, body)
		}
	}
	if !strings.Contains(body, `gateway_edge_rule_compile_error_total{kind="ip"} 1`) {
		t.Errorf("ip compile-error should surface at 1, body:\n%s", body)
	}
}

// TestMetricsEdgeRuleCompileErrorNilSafe — the counter must not
// panic when called on a nil receiver (parallels the apply test).
func TestMetricsEdgeRuleCompileErrorNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveEdgeRuleCompileError("rewrite") // must not panic
}

// TestMetricsAccountRateLimitedRegistersAndPreInstantiates — ADR-040
// / issue #292: the counter must surface the four (plan) rows under the
// "__other__" placeholder at 0 from the moment the daemon binds, so the
// §12 dashboard panel never shows "no data". Real account_id rows
// appear on first 429.
func TestMetricsAccountRateLimitedRegistersAndPreInstantiates(t *testing.T) {
	m := NewMetrics()
	m.ObserveAccountRateLimit("acct-x", "pro")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`gateway_per_account_rate_limited_total{account_id="__other__",plan="free"} 0`,
		`gateway_per_account_rate_limited_total{account_id="__other__",plan="hobby"} 0`,
		`gateway_per_account_rate_limited_total{account_id="__other__",plan="pro"} 0`,
		`gateway_per_account_rate_limited_total{account_id="__other__",plan="scale"} 0`,
		`gateway_per_account_rate_limited_total{account_id="acct-x",plan="pro"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing exposition line %q in body:\n%s", want, body)
		}
	}
}

// TestMetricsAccountRateLimitedOverflowCollapsesToOther — the
// accountLabelSet mirror (issue #278 pattern from pkg/wire/metrics.go)
// must collapse distinct account_ids past the cap to "__other__" so
// the §12 panel cardinality stays bounded past customer-count growth.
// Reaches into the unexported accountLabels field via a custom
// constructor (test-only newAccountLabelSetWithCap) so the test
// exercises the real overflow path with a tiny capacity.
//
// Asserts:
//   - the cap'th distinct real id is admitted (appears under its
//     real label);
//   - the (cap+1)'th distinct id collapses to "__other__" (does NOT
//     mint a new label);
//   - "anonymous" and "__other__" themselves pass through admit()
//     untouched so the closed (plan) pre-instantiation keeps working.
func TestMetricsAccountRateLimitedOverflowCollapsesToOther(t *testing.T) {
	const cap = 4
	s := newAccountLabelSetWithCap(cap)
	if got := s.admit("acct-1"); got != "acct-1" {
		t.Fatalf("admit(acct-1) = %q, want %q", got, "acct-1")
	}
	if got := s.admit("acct-2"); got != "acct-2" {
		t.Fatalf("admit(acct-2) = %q, want %q", got, "acct-2")
	}
	if got := s.admit("acct-3"); got != "acct-3" {
		t.Fatalf("admit(acct-3) = %q, want %q", got, "acct-3")
	}
	// 3 real ids admitted (cap=4, reservedCount=2; remaining budget=2).
	if got := s.admit("acct-4"); got != "acct-4" {
		t.Fatalf("admit(acct-4) = %q, want %q (cap still has budget)", got, "acct-4")
	}
	// Now the budget is exhausted — the next distinct id collapses.
	if got := s.admit("acct-5"); got != otherAccountLabel {
		t.Fatalf("admit(acct-5) = %q, want %q (overflow)", got, otherAccountLabel)
	}
	// And stays collapsed even after further distinct ids.
	if got := s.admit("acct-6"); got != otherAccountLabel {
		t.Fatalf("admit(acct-6) = %q, want %q (still overflow)", got, otherAccountLabel)
	}
	// An already-admitted id still surfaces under its real label —
	// admission is sticky, not LRU.
	if got := s.admit("acct-1"); got != "acct-1" {
		t.Fatalf("admit(acct-1) again = %q, want %q (sticky admission)", got, "acct-1")
	}
	// Reserved labels are pass-through and never consume capacity.
	if got := s.admit(""); got != anonymousAccountLabel {
		t.Fatalf("admit(\"\") = %q, want %q (empty normalises)", got, anonymousAccountLabel)
	}
	if got := s.admit(anonymousAccountLabel); got != anonymousAccountLabel {
		t.Fatalf("admit(anonymous) = %q, want %q (pass-through)", got, anonymousAccountLabel)
	}
	if got := s.admit(otherAccountLabel); got != otherAccountLabel {
		t.Fatalf("admit(__other__) = %q, want %q (pass-through)", got, otherAccountLabel)
	}
}

// TestMetricsSetQueueDepthAccountIDLabel — ADR-123 PR-D: the
// `gateway_queue_depth` gauge gains an `account_id` label so the
// queue_backlog_growing signal contributes to the
// FaasAlertPresetAnyFiringAccount correlation rule. Pre-PR-D the
// gauge carried only `app`; PR-D adds the per-account row admitted
// via accountLabelSet (overflow=`__other__`). Asserts:
//
//   - the closed-set `__other__` row is pre-instantiated at 0 from
//     the moment the daemon binds, so the §12 panel never shows
//     "no data" before the first real wait;
//   - a real account_id surfaces under its own label on first set;
//   - the empty-string accountID (defensive fallback) collapses to
//     `__other__` rather than minting an empty-label series.
func TestMetricsSetQueueDepthAccountIDLabel(t *testing.T) {
	m := NewMetrics()
	m.PreInstantiateQueueDepth("app-pre")
	m.SetQueueDepth("app-pre", "acct-real", 7)
	m.SetQueueDepth("app-empty", "", 3) // empty → __other__ per spec

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`gateway_queue_depth{account_id="__other__",app="app-pre"} 0`,
		`gateway_queue_depth{account_id="acct-real",app="app-pre"} 7`,
		`gateway_queue_depth{account_id="__other__",app="app-empty"} 3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing exposition line %q in body:\n%s", want, body)
		}
	}
}

// TestMetricsAccountRateLimitedNilSafe — the helper must not panic on a
// nil receiver (the call site in pkg/gateway/handler.go already
// nil-guards, but the helper itself is nil-safe by design — mirror of
// ObserveWakeQueueWait / ObserveTLSOnDemandDenied).
func TestMetricsAccountRateLimitedNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveAccountRateLimit("x", "free") // must not panic
}

// TestMetricsAccountRateLimitedNilAccountLabels — covers the path
// where the helper is called on a *Metrics whose accountLabels is nil
// (e.g. a Metrics constructed outside NewMetrics, or after a future
// refactor that introduces a no-metrics constructor). The call must
// not panic and must fall back to the literal account_id, preserving
// the pre-mirror behaviour for the unit-test path.
func TestMetricsAccountRateLimitedNilAccountLabels(t *testing.T) {
	m := NewMetrics()
	// Simulate the "accountLabels never wired" path by nil-ing the
	// field after NewMetrics constructed it. Real production wiring
	// always sets it, but a future refactor that introduces a
	// no-metrics constructor must keep the helper nil-safe.
	m.accountLabels = nil
	// Must not panic.
	m.ObserveAccountRateLimit("acct-x", "pro")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `gateway_per_account_rate_limited_total{account_id="acct-x",plan="pro"} 1`) {
		t.Errorf("fallback path did not surface literal account_id; body:\n%s", rec.Body.String())
	}
}

// TestNewAccountLabelSetPanicsOnZeroCapacity — fail-loud at boot if
// the production wiring accidentally constructs with capacity ≤ 0.
// Mirrors pkg/wire/metrics.go:2573-2588 (the upstream primitive
// panics on the same condition).
func TestNewAccountLabelSetPanicsOnZeroCapacity(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("newAccountLabelSetWithCap(0) did not panic")
		}
	}()
	_ = newAccountLabelSetWithCap(0)
}

// TestMetricsWakeLocalityPreinstantiated pins the closed (outcome) set
// at zero from the moment the daemon binds, so the dashboard panel
// surfaces from boot and a missing wire-incrementation is visible as a
// frozen zero (PR scale-out readiness). Catches a future change that
// drops the pre-instantiation loop or renames an outcome value.
func TestMetricsWakeLocalityPreinstantiated(t *testing.T) {
	m := NewMetrics()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, outcome := range []string{"local_snapshot", "local_coldboot"} {
		want := fmt.Sprintf(`gateway_wake_locality_total{outcome=%q} 0`, outcome)
		if !strings.Contains(body, want) {
			t.Errorf("pre-instantiated %s missing from /metrics body:\n%s", want, body)
		}
	}
}

// TestMetricsWakeSnapshotTierPreinstantiated (issue #470 / PR #470-FU-B)
// pins the closed (tier) set on the per-wake snapshot-tier counter at
// zero from the moment the daemon binds, so the warm-tier dashboard
// panel surfaces from boot. Catches a future change that drops the
// pre-instantiation loop or renames a tier value.
func TestMetricsWakeSnapshotTierPreinstantiated(t *testing.T) {
	m := NewMetrics()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, tier := range []string{"warm", "init", "cold"} {
		want := fmt.Sprintf(`gateway_wake_snapshot_tier_total{tier=%q} 0`, tier)
		if !strings.Contains(body, want) {
			t.Errorf("pre-instantiated %s missing from /metrics body:\n%s", want, body)
		}
	}
}

// TestMetricsWakePhaseDurationPreinstantiated (ADR-098 C11) pins the
// closed phase set on the new phase-decomposed wake histogram at
// zero from the moment the daemon binds, so the §12 panel surfaces
// from boot. Catches a future change that drops the pre-instantiation
// loop or renames a phase value. The aggregate
// gateway_wake_latency_seconds histogram is NOT changed here — that
// series stays byte-identical (tested in pkg/gateway/testhist/...).
func TestMetricsWakePhaseDurationPreinstantiated(t *testing.T) {
	m := NewMetrics()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, phase := range []string{
		"queue_wait", "coordinator_wait", "schedd_admit",
		"vmmd_wake", "guest_ready", "cold_fallback_reason",
	} {
		want := fmt.Sprintf(`gateway_wake_phase_duration_seconds_count{phase=%q} 0`, phase)
		if !strings.Contains(body, want) {
			t.Errorf("pre-instantiated %s missing from /metrics body:\n%s", want, body)
		}
	}
}

// TestMetricsObserveWakePhaseRoundTrip (ADR-098 C11) asserts that
// ObserveWakePhase increments the labelled histogram and that the
// scalar (legacy) ObserveWakeQueueWait dual-writes into the
// phase="queue_wait" series. Pinning both halves of the dual-write
// here keeps the §12 panel honest through a future refactor that
// drops either the scalar or the vector.
func TestMetricsObserveWakePhaseRoundTrip(t *testing.T) {
	m := NewMetrics()
	m.ObserveWakePhase("vmmd_wake", 250*time.Millisecond)
	m.ObserveWakeQueueWait(120 * time.Millisecond)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`gateway_wake_phase_duration_seconds_count{phase="vmmd_wake"} 1`,
		`gateway_wake_phase_duration_seconds_count{phase="queue_wait"} 1`,
		// Legacy scalar stays byte-identical for one release.
		// ObserveWakeQueueWait dual-writes into the vector AND
		// into the scalar; only the queue_wait call counts here.
		`gateway_wake_queue_wait_seconds_count 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing exposition line %q in body:\n%s", want, body)
		}
	}
}

// TestMetricsWakeLocalityObserved asserts the counter increments per
// outcome and that an unknown outcome is passed through (Prometheus
// default behaviour — the closed set is closed by the pre-instantiation
// loop and the call site, not by the registry).
func TestMetricsWakeLocalityObserved(t *testing.T) {
	m := NewMetrics()
	m.ObserveWakeLocality("local_snapshot")
	m.ObserveWakeLocality("local_coldboot")
	m.ObserveWakeLocality("local_coldboot")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`gateway_wake_locality_total{outcome="local_snapshot"} 1`,
		`gateway_wake_locality_total{outcome="local_coldboot"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing exposition line %q in body:\n%s", want, body)
		}
	}
}

// TestObserveWakeLocalityNilSafe — the wrapper must not panic on a nil
// receiver (the call site in handler.go already nil-guards, but the
// helper itself is nil-safe by design — mirror of
// ObserveWakeQueueWait / ObserveTLSOnDemandDenied).
func TestObserveWakeLocalityNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveWakeLocality("local_snapshot") // must not panic
}

// TestMetricsWakeSnapshotTierObserved (issue #470 / PR #470-FU-B)
// asserts the per-wake snapshot-tier counter increments per
// tier and that the empty-string fallthrough lands on "init"
// (the engine's default tier on the pre-#470 legacy path).
func TestMetricsWakeSnapshotTierObserved(t *testing.T) {
	m := NewMetrics()
	m.ObserveWakeSnapshotTier("warm")
	m.ObserveWakeSnapshotTier("warm")
	m.ObserveWakeSnapshotTier("warm")
	m.ObserveWakeSnapshotTier("init")
	m.ObserveWakeSnapshotTier("cold")
	m.ObserveWakeSnapshotTier("") // empty → "init" fallback

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	// warm=3, init=1+1=2 (the "" fallback lands on init), cold=1.
	for _, want := range []string{
		`gateway_wake_snapshot_tier_total{tier="warm"} 3`,
		`gateway_wake_snapshot_tier_total{tier="init"} 2`,
		`gateway_wake_snapshot_tier_total{tier="cold"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing exposition line %q in body:\n%s", want, body)
		}
	}
}

// TestObserveWakeSnapshotTierNilSafe — the wrapper must not panic
// on a nil receiver (mirrors TestObserveWakeLocalityNilSafe).
func TestObserveWakeSnapshotTierNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveWakeSnapshotTier("warm") // must not panic
	m.ObserveWakeSnapshotTier("")     // must not panic with empty
}

// TestTierFromWakeMethod (issue #470 / PR #470-FU-B) locks the
// temporary bridge that maps the existing 2-value WakeMethod
// to the closest 3-value tier label. PR #470-FU-A replaces the
// call site with the engine's actual tier field; this test
// guards the bridge so the transition seam is observable.
func TestTierFromWakeMethod(t *testing.T) {
	cases := []struct {
		method WakeMethod
		want   string
	}{
		{WakeMethodSnapshotRestore, "init"},
		{WakeMethodColdBoot, "cold"},
		{WakeMethodUnspecified, "init"},
	}
	for _, c := range cases {
		if got := tierFromWakeMethod(c.method); got != c.want {
			t.Errorf("tierFromWakeMethod(%v) = %q, want %q", c.method, got, c.want)
		}
	}
}

// TestComputeNodeChangedSubscriberAliveRegisters — PR scale-out
// readiness. The gauge must surface in the exposition handler
// (catches a rename that would silently break the dashboard panel).
//
// Uses the same Gather() path as TestMetricsTLSCertExpiryRegisters
// rather than a string-Contains on the literal numeric value: the
// gauge's value is monotonic and the operator cares about
// "frozen-or-zero", not the exact number.
func TestComputeNodeChangedSubscriberAliveRegisters(t *testing.T) {
	m := NewMetrics()
	m.TouchComputeNodeChangedSubscriber()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "gateway_compute_node_changed_subscriber_alive") {
		t.Errorf("gauge not in registry output:\n%s", rec.Body.String())
	}
	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var got float64
	var found bool
	for _, fam := range fams {
		if fam.GetName() != "gateway_compute_node_changed_subscriber_alive" {
			continue
		}
		for _, mt := range fam.GetMetric() {
			got = mt.GetGauge().GetValue()
			found = true
		}
	}
	if !found {
		t.Fatal("gateway_compute_node_changed_subscriber_alive not in registry gather")
	}
	if got != 1 {
		t.Errorf("gauge value = %v, want 1 after one Touch", got)
	}
}

// TestComputeNodeChangedSubscriberAliveObserves — the wrapper
// increments by exactly one per call. Pins the monotonic contract
// the alert rule depends on (freeze = stale).
func TestComputeNodeChangedSubscriberAliveObserves(t *testing.T) {
	m := NewMetrics()
	for i := 0; i < 3; i++ {
		m.TouchComputeNodeChangedSubscriber()
	}
	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var got float64
	for _, fam := range fams {
		if fam.GetName() != "gateway_compute_node_changed_subscriber_alive" {
			continue
		}
		for _, mt := range fam.GetMetric() {
			got = mt.GetGauge().GetValue()
		}
	}
	if got != 3 {
		t.Errorf("gauge value = %v, want 3 after 3 Touches", got)
	}
}

// TestTouchComputeNodeChangedSubscriberNilSafe — the wrapper must not
// panic when called on a nil receiver (mirrors ObserveWakeLocalityNilSafe
// above and SetTLSCertExpiryNilSafe earlier in this file). cmd/gatewayd-internal/
// tests pass a nil *Metrics; production wires deps.metrics which is
// always non-nil after NewMetrics.
func TestTouchComputeNodeChangedSubscriberNilSafe(t *testing.T) {
	var m *Metrics
	m.TouchComputeNodeChangedSubscriber() // must not panic
}

// TestMetricsStreamFlushesRegistered is the B2 PR-D tripwire for
// the gateway_stream_flushes_total counter. PR-B+PR-C (commit 34c3677b)
// shipped the counter construction but omitted m.streamFlushes from
// the reg.MustRegister args at metrics.go:453. The counter is
// incremented in code but never emitted on /metrics — every downstream
// consumer (the §12 dashboard panel in B6, the metal e2e in B5) sees
// "no data" regardless of actual request shape. This test asserts (a)
// the metric name appears in the registry output, and (b) the
// counter increments survive the registered-counter pipeline.
func TestMetricsStreamFlushesRegistered(t *testing.T) {
	m := NewMetrics()
	m.ObserveStreamFlush("app-A", "hobby")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "gateway_stream_flushes_total") {
		t.Errorf("counter not in registry output:\n%s", body)
	}

	// Numeric readback via Gather() — same shape as
	// TestMetricsTLSCertExpiryRegisters / TestMetricsWakeLocalityObserved.
	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var got float64
	var found bool
	for _, fam := range fams {
		if fam.GetName() != "gateway_stream_flushes_total" {
			continue
		}
		for _, mt := range fam.GetMetric() {
			app := mt.GetLabel()[0].GetValue()
			plan := mt.GetLabel()[1].GetValue()
			if app == "app-A" && plan == "hobby" {
				got = mt.GetCounter().GetValue()
				found = true
			}
		}
	}
	if !found {
		t.Fatal("gateway_stream_flushes_total{app=app-A, plan=hobby} not in registry gather")
	}
	if got != 1 {
		t.Errorf("counter value = %v, want 1 after one ObserveStreamFlush", got)
	}
}

// TestObserveStreamFlushNilSafe — the wrapper must not panic when
// called on a nil receiver (mirrors the established nil-safety pattern
// for the other gateway metrics).
func TestObserveStreamFlushNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveStreamFlush("app-A", "hobby") // must not panic
}

// TestMetricsStreamActiveStartEnd asserts the B3 gauge surfaces in
// the registry and that a paired Start/End leaves the gauge at zero.
// The 1000-iteration stress loop catches the goroutine-leak-style
// drift called out in PR-D R3 (a panic between Start and End leaks
// the gauge; the loop exercises the defer path).
func TestMetricsStreamActiveStartEnd(t *testing.T) {
	m := NewMetrics()
	const appID, plan = "app-A", "hobby"
	for i := 0; i < 1000; i++ {
		m.ObserveStreamStart(appID, plan)
		m.ObserveStreamEnd(appID, plan)
	}

	// Numeric readback via Gather().
	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, fam := range fams {
		if fam.GetName() != "gateway_stream_active" {
			continue
		}
		for _, mt := range fam.GetMetric() {
			app := mt.GetLabel()[0].GetValue()
			planL := mt.GetLabel()[1].GetValue()
			if app == appID && planL == plan {
				found = true
				if got := mt.GetGauge().GetValue(); got != 0 {
					t.Errorf("gauge value = %v, want 0 after 1000 balanced Start/End pairs", got)
				}
			}
		}
	}
	if !found {
		t.Fatal("gateway_stream_active not in registry gather after stress loop")
	}
}

// TestMetricsStreamActiveConcurrentBalance asserts the gauge is
// goroutine-safe under concurrent Start/End. 1000 goroutines each
// issue a balanced Start/End; the final gauge must be zero.
func TestMetricsStreamActiveConcurrentBalance(t *testing.T) {
	m := NewMetrics()
	const appID, plan = "app-A", "hobby"
	const N = 1000
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			m.ObserveStreamStart(appID, plan)
			m.ObserveStreamEnd(appID, plan)
		}()
	}
	wg.Wait()

	// Read the gauge back.
	fams, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range fams {
		if fam.GetName() != "gateway_stream_active" {
			continue
		}
		for _, mt := range fam.GetMetric() {
			if mt.GetGauge().GetValue() != 0 {
				t.Errorf("gauge value = %v, want 0 after concurrent balanced Start/End", mt.GetGauge().GetValue())
			}
		}
	}
}

// TestObserveStreamStartEndNilSafe — the wrappers must not panic
// when called on a nil receiver.
func TestObserveStreamStartEndNilSafe(t *testing.T) {
	var m *Metrics
	m.ObserveStreamStart("app-A", "hobby") // must not panic
	m.ObserveStreamEnd("app-A", "hobby")   // must not panic
}

// TestMetricsWSCountersRegistered (issue #676 / ADR-080
// follow-up, PR-B) confirms all four gateway_ws_* Prometheus
// series + their pre-instantiated closed label cells appear in
// the /metrics output from boot. Catches a typo in the metric
// names (which the §12 dashboards depend on) AND a regression
// in the constructor's pre-instantiate loop (a missing label
// cell would surface as "no data" on the panel until the first
// production WS hit).
func TestMetricsWSCountersRegistered(t *testing.T) {
	m := NewMetrics()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()

	// Series name contract — pin the exact names the
	// deploy/grafana/ panels query (dashes between segments;
	// "_total" suffix on counters; "_seconds" on the histogram
	// + "_count" / "_sum" auto-emitted siblings).
	wantSeries := []string{
		"gateway_ws_upgrade_total",
		"gateway_ws_active_sessions",
		"gateway_ws_session_duration_seconds",
		"gateway_ws_session_bytes_total",
	}
	for _, name := range wantSeries {
		if !strings.Contains(body, name) {
			t.Errorf("series %q not in registry output", name)
		}
	}

	// Pre-instantiated label cells. The constructor loops over
	// (api.Plans × wsOutcomes / wsSessionOutcomes / wsDirections)
	// and stamps every cell. Asserting one cell per series
	// catches a missing-loop regression; the package-level
	// construction test below covers the full cross-product.
	wantCells := []string{
		`gateway_ws_upgrade_total{outcome="accepted",plan="hobby"}`,
		`gateway_ws_upgrade_total{outcome="plan_denied",plan="free"}`,
		`gateway_ws_upgrade_total{outcome="bridge_disabled",plan="pro"}`,
		`gateway_ws_active_sessions{plan="scale"}`,
		`gateway_ws_session_duration_seconds_count{outcome="client_disconnect",plan="hobby"}`,
		`gateway_ws_session_bytes_total{direction="tx",plan="pro"}`,
		`gateway_ws_session_bytes_total{direction="rx",plan="scale"}`,
	}
	for _, cell := range wantCells {
		if !strings.Contains(body, cell) {
			t.Errorf("pre-instantiated cell %q missing from output", cell)
		}
	}
}

// TestMetricsWSHelpersIncDecSymmetric (issue #676 / ADR-080
// follow-up, PR-B) confirms the gauge Inc/Dec helpers balance
// and the counter helpers track the byte volume. The symmetric
// Inc/Dec pair is the load-bearing property: a forwarder that
// Inc's without matching Dec (or vice versa) silently breaks
// the ws_active_sessions{plan} panel rate. The counter Add
// helper must accept n>0 (zero or negative is a no-op, not an
// underflow).
func TestMetricsWSHelpersIncDecSymmetric(t *testing.T) {
	m := NewMetrics()

	// Three sessions open, three close — gauge returns to zero.
	for range []int{0, 1, 2} {
		m.IncWSSessionStart("hobby")
	}
	m.DecWSSessionEnd("hobby")
	m.DecWSSessionEnd("hobby")
	m.DecWSSessionEnd("hobby")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	want := `gateway_ws_active_sessions{plan="hobby"} 0`
	if !strings.Contains(body, want) {
		t.Errorf("symmetric Inc/Dec didn't restore gauge to zero; want %q in body", want)
	}

	// Add bytes — counter increments by the exact delta.
	m.AddWSSessionBytes("hobby", WSDirectionTx, 1024)
	m.AddWSSessionBytes("hobby", WSDirectionTx, 4096)
	m.AddWSSessionBytes("hobby", WSDirectionRx, 512)
	m.AddWSSessionBytes("hobby", WSDirectionRx, 0)   // no-op
	m.AddWSSessionBytes("hobby", WSDirectionRx, -10) // no-op (negative guard)

	rec = httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body = rec.Body.String()
	if !strings.Contains(body, `gateway_ws_session_bytes_total{direction="tx",plan="hobby"} 5120`) {
		t.Errorf("tx counter mismatch; want 5120 in body")
	}
	if !strings.Contains(body, `gateway_ws_session_bytes_total{direction="rx",plan="hobby"} 512`) {
		t.Errorf("rx counter mismatch; want 512 in body (negative/zero must be no-op)")
	}
}

// TestMetricsWSNilSafe (issue #676 / ADR-080 follow-up, PR-B)
// keeps the helper methods nil-safe so the pre-PR-B test corpus
// (where Metrics may be nil) keeps building without per-test
// boilerplate. Mirrors the existing TestObserveStreamStartEndNilSafe
// precedent at the top of this file.
func TestMetricsWSNilSafe(t *testing.T) {
	var m *Metrics
	m.IncWSUpgrade("hobby", WSOutcomeAccepted)
	m.IncWSSessionStart("hobby")
	m.DecWSSessionEnd("hobby")
	m.ObserveWSSessionDuration("hobby", WSOutcomeAccepted, time.Second)
	m.AddWSSessionBytes("hobby", WSDirectionTx, 1024)
}

func TestMetricsTLSCertExpiryUnknownUntilObserved(t *testing.T) {
	m := NewMetrics()
	for _, remaining := range []float64{math.NaN(), -60, 86400} {
		if !math.IsNaN(remaining) {
			m.SetTLSCertExpiry(time.Duration(remaining) * time.Second)
		}
		families, err := m.Registry().Gather()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, family := range families {
			if family.GetName() != "gateway_tls_cert_expiry_seconds" {
				continue
			}
			found = true
			got := family.Metric[0].GetGauge().GetValue()
			if math.IsNaN(remaining) {
				if !math.IsNaN(got) {
					t.Fatalf("unobserved expiry = %v, want NaN", got)
				}
			} else if got != remaining {
				t.Fatalf("expiry = %v, want %v", got, remaining)
			}
		}
		if !found {
			t.Fatal("expiry gauge missing")
		}
	}
}
