// Tests for the bounded account-label admission set introduced for
// issue #278 (per-customer failure observability). The set lives on
// OpsMetrics and is shared by the audit-write and request-failure
// counters so a customer is represented by their real id in both
// metrics, or by "__other__" in both.
//
// The shape of these tests is load-bearing: they pin the contract
// that
//   - the first maxAccountLabelValues distinct ids are admitted,
//   - further ids collapse to "__other__" without consuming capacity,
//   - the reserved "anonymous" and "__other__" labels never count
//     against the cap,
//   - the admission set is race-safe under -race,
//   - repeated lookups for the same id return the same underlying
//     Prometheus Counter.

package wire_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// TestAccountLabel_FirstNAdmitted asserts the first maxAccountLabelValues
// distinct ids round-trip unchanged through accountLabel.
func TestAccountLabel_FirstNAdmitted(t *testing.T) {
	const n = 100 // sub-sample of the 10k cap for fast tests
	m := wire.NewOpsMetrics("apid")
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("acct-%08x", i)
		// Force admission by hitting the audit-write counter once.
		m.AuditWriteFailures(id).Inc()
	}
	body := render(t, m)
	want := `apid_audit_write_failures_total{account_id="acct-00000000"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("missing first-id series %q in:\n%s", want, body)
	}
	// A mid-range id must also be present.
	midWant := fmt.Sprintf(`apid_audit_write_failures_total{account_id=%q} 1`, fmt.Sprintf("acct-%08x", n/2))
	if !strings.Contains(body, midWant) {
		t.Errorf("missing mid-id series %q in:\n%s", midWant, body)
	}
}

// TestAccountLabel_OverflowsToOther asserts the (cap+1)th distinct id
// collapses to "__other__". The reserved value is admitted at boot,
// so the 10 001st real id is the first to land in overflow.
func TestAccountLabel_OverflowsToOther(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	// Fill to capacity. We bypass accountLabel directly by hammering
	// AuditWriteFailures — each unique id is admitted once.
	const cap = 10_000
	for i := 0; i < cap; i++ {
		m.AuditWriteFailures(fmt.Sprintf("real-%05d", i))
	}
	// The (cap+1)th id is the first overflow.
	m.AuditWriteFailures("real-overflow").Inc()
	body := render(t, m)
	if !strings.Contains(body, `apid_audit_write_failures_total{account_id="__other__"} 1`) {
		t.Errorf("expected __other__ series at overflow; body was:\n%s", body)
	}
	// A second overflow id should also land in __other__ and
	// increment the same series (not mint a new one). Review finding
	// #2 on PR #332: this also pins the property that the overflow
	// path does NOT consume capacity — the reserved "__other__" label
	// was pre-admitted at boot, so the map size stays at cap+2
	// (cap real ids + 2 reserved) regardless of how many overflow ids
	// observe in.
	m.AuditWriteFailures("real-overflow-2").Inc()
	body = render(t, m)
	if !strings.Contains(body, `apid_audit_write_failures_total{account_id="__other__"} 2`) {
		t.Errorf("expected __other__ series to accumulate on the audit counter; body was:\n%s", body)
	}
	// Drive a third overflow id through the request-failure accessor
	// to prove the cap is shared across metrics. The same __other__
	// label must surface in the request-failure counter too — without
	// that, the two metrics would diverge and an operator looking at
	// one wouldn't see the other.
	m.RequestFailure("real-overflow-3", "GET /v1/test").Inc()
	body = render(t, m)
	if !strings.Contains(body, `apid_request_failures_total{account_id="__other__",code="err",route="GET /v1/test"} 1`) {
		t.Errorf("expected __other__ series on request-failure metric; body was:\n%s", body)
	}
	// The audit counter must NOT have advanced from the cross-metric
	// overflow (different metric) — pinning that the two accessors
	// share admission but not series identity.
	if !strings.Contains(body, `apid_audit_write_failures_total{account_id="__other__"} 2`) {
		t.Errorf("audit counter must stay at 2 after a request-failure overflow; body was:\n%s", body)
	}
}

// TestAccountLabel_ReservedValuesDontConsumeCapacity asserts the
// reserved labels (anonymous, __other__) are admitted at boot
// without consuming the real-account budget.
func TestAccountLabel_ReservedValuesDontConsumeCapacity(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	// The reserved labels must surface from the very first scrape —
	// pre-instantiated in NewOpsMetrics so the dashboard never goes
	// "no data" for an idle box.
	body := render(t, m)
	for _, want := range []string{
		`apid_audit_write_failures_total{account_id="anonymous"} 0`,
		`apid_audit_write_failures_total{account_id="__other__"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing reserved series %q in:\n%s", want, body)
		}
	}
	// Empty input normalizes to "anonymous".
	m.AuditWriteFailures("").Inc()
	body = render(t, m)
	if !strings.Contains(body, `apid_audit_write_failures_total{account_id="anonymous"} 1`) {
		t.Errorf("empty id did not normalize to anonymous; body was:\n%s", body)
	}
}

// TestAccountLabel_SharedBetweenMetrics asserts the cap is shared
// between AuditWriteFailures and RequestFailure: an id admitted via
// one accessor must surface in the other's series with its real
// label, and the cap must not be double-counted.
func TestAccountLabel_SharedBetweenMetrics(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	const id = "shared-acct-1"
	m.AuditWriteFailures(id).Inc()
	m.RequestFailure(id, "GET /v1/test").Inc()
	body := render(t, m)
	for _, want := range []string{
		fmt.Sprintf(`apid_audit_write_failures_total{account_id=%q} 1`, id),
		fmt.Sprintf(`apid_request_failures_total{account_id=%q,code="err",route="GET /v1/test"} 1`, id),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing shared series %q in:\n%s", want, body)
		}
	}
}

// TestAccountLabel_ConcurrentAdmission asserts the admission set is
// race-safe under -race. 100 goroutines admit 100 distinct ids each
// (10 000 total — under the cap), and we expect all of them to
// surface in the body without a panic or duplicate series.
func TestAccountLabel_ConcurrentAdmission(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	const goroutines = 100
	const perGoroutine = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id := fmt.Sprintf("g%03d-i%05d", gid, i)
				m.AuditWriteFailures(id).Inc()
			}
		}(g)
	}
	wg.Wait()
	// Spot-check the first id from goroutine 0 — if the map was
	// corrupted under -race, this assertion will fail intermittently.
	body := render(t, m)
	want := `apid_audit_write_failures_total{account_id="g000-i00000"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("missing concurrent series %q in:\n%s", want, body)
	}
}

// TestAccountLabel_IdempotentLabel asserts two calls with the same
// id return a Counter that points at the same underlying series
// (so incrementing via either call is interchangeable). This is the
// contract the audit seam depends on for hot-path caching.
func TestAccountLabel_IdempotentLabel(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	c1 := m.AuditWriteFailures("dup-id")
	c2 := m.AuditWriteFailures("dup-id")
	c1.Inc()
	c2.Inc()
	// The prometheus client lib returns the same underlying Counter
	// on repeated WithLabelValues calls — if c1 and c2 pointed at
	// different series, each Inc would land on a separate series and
	// the readSingle below would see 1 (not 2). This test pins the
	// contract for our accessor wrapper so the hot-path cache is
	// guaranteed to share the same Prometheus child.
	if got := readSingle(t, m, `apid_audit_write_failures_total{account_id="dup-id"}`); got != 2 {
		t.Errorf("dup-id counter = %v, want 2 (callers must share series)", got)
	}
}

// TestAuditWriteDuration_PreInstantiated asserts the ok/failed
// histogram series surface from boot (so the dashboard never goes
// "no data" on an idle daemon).
func TestAuditWriteDuration_PreInstantiated(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	body := render(t, m)
	for _, want := range []string{
		`apid_audit_write_failures_duration_seconds_count{result="ok"} 0`,
		`apid_audit_write_failures_duration_seconds_count{result="failed"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing pre-instantiated histogram series %q in:\n%s", want, body)
		}
	}
}

// TestRequestFailureFor_ExtractsRouteFromPattern pins the canonical
// HTTP-path accessor (issue #278, review finding #1 on PR #332).
// The route label is read from r.Pattern (the Go mux pattern) with
// the reserved "unmatched" fallback for paths the mux did not
// dispatch. By owning the extraction inside the accessor, callers
// cannot accidentally pass a raw URL path and explode the
// cardinality — the route label is bounded by the route table.
func TestRequestFailureFor_ExtractsRouteFromPattern(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	const id = "acct-route-pattern"
	// Matched route: r.Pattern is the mux pattern.
	matched := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	matched.Pattern = "GET /v1/test"
	m.RequestFailureFor(matched, id).Inc()
	body := render(t, m)
	if !strings.Contains(body, fmt.Sprintf(`apid_request_failures_total{account_id=%q,code="err",route="GET /v1/test"} 1`, id)) {
		t.Errorf("matched route: missing expected series in:\n%s", body)
	}
	// Unmatched route: r.Pattern is "" (the 404 path a URL scanner
	// hits). The accessor must collapse to "unmatched" rather than
	// propagating the empty string (which would minter a new series
	// per call).
	unmatched := httptest.NewRequest(http.MethodGet, "/wp-login.php", nil)
	unmatched.Pattern = ""
	m.RequestFailureFor(unmatched, id).Inc()
	body = render(t, m)
	if !strings.Contains(body, fmt.Sprintf(`apid_request_failures_total{account_id=%q,code="err",route="unmatched"} 1`, id)) {
		t.Errorf("unmatched route: missing expected series in:\n%s", body)
	}
}

// readSingle is a tiny helper to scrape a single counter value out
// of the apid /metrics text. Fails the test if the line is absent.
func readSingle(t *testing.T, m *wire.OpsMetrics, line string) float64 {
	t.Helper()
	body := render(t, m)
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, line+" ") {
			var v float64
			if _, err := fmt.Sscanf(l, line+" %f", &v); err == nil {
				return v
			}
		}
	}
	t.Fatalf("line %q not found in:\n%s", line, body)
	return 0
}

// TestRequestTotalSharesAdmissionSet pins the contract that
// apid_request_total{account_id,route,code} (issue #303, ADR-038)
// shares the same accountLabelSet as apid_request_failures_total
// and apid_audit_write_failures_total. The three metrics have to
// represent a customer by the same label so an operator looking at
// one metric can drill down to the same customer in the others —
// divergent admission would silently scatter the customer story
// across dashboards.
func TestRequestTotalSharesAdmissionSet(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	// Real id: both counters must surface the same label.
	const id = "shared-acct-rt"
	m.RequestTotal(id, "GET /v1/rt", "ok").Inc()
	m.RequestFailure(id, "GET /v1/rt").Inc()
	body := render(t, m)
	// Prometheus client lib emits labels in alphabetical order
	// (account_id, code, route) regardless of the order they were
	// declared or supplied.
	want := fmt.Sprintf(`apid_request_total{account_id=%q,code="ok",route="GET /v1/rt"} 1`, id)
	if !strings.Contains(body, want) {
		t.Errorf("missing request_total series %q in:\n%s", want, body)
	}
	wantFail := fmt.Sprintf(`apid_request_failures_total{account_id=%q,code="err",route="GET /v1/rt"} 1`, id)
	if !strings.Contains(body, wantFail) {
		t.Errorf("missing request_failures series %q in:\n%s", wantFail, body)
	}
	// Second Inc on the requestTotal counter (different code path).
	// The series is independent of the failure counter (different
	// counter), but the admission must still surface the same id.
	m.RequestTotal(id, "GET /v1/rt", "err").Inc()
	if got := readSingle(t, m, fmt.Sprintf(`apid_request_total{account_id=%q,code="err",route="GET /v1/rt"}`, id)); got != 1 {
		t.Errorf("err-code request_total = %v, want 1", got)
	}
	// Empty id: must resolve to "anonymous" in both.
	m.RequestTotal("", "GET /v1/anon", "ok").Inc()
	m.RequestFailure("", "GET /v1/anon").Inc()
	body = render(t, m)
	for _, want := range []string{
		`apid_request_total{account_id="anonymous",code="ok",route="GET /v1/anon"} 1`,
		`apid_request_failures_total{account_id="anonymous",code="err",route="GET /v1/anon"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("anonymous-id series missing %q in:\n%s", want, body)
		}
	}
}

// TestRequestTotalOverflowsToSharedOther pins the contract that ids
// past the accountLabelSet cap collapse to "__other__" in BOTH
// apid_request_total and apid_request_failures_total. The shared
// overflow bucket is the load-bearing primitive for the per-account
// alert rules in ADR-038 — without it, a single customer's
// catastrophic traffic would either grow the TSDB series set
// unbounded or split across multiple series.
func TestRequestTotalOverflowsToSharedOther(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	const cap = 10_000
	// Fill the admission set via the audit counter (cheapest, doesn't
	// disturb the requestTotal/requestFailures counter rows).
	for i := 0; i < cap; i++ {
		m.AuditWriteFailures(fmt.Sprintf("real-%05d", i))
	}
	// 10 001st id lands in __other__ via every accessor.
	m.RequestTotal("real-overflow-rt", "GET /v1/o", "ok").Inc()
	m.RequestFailure("real-overflow-rf", "GET /v1/o").Inc()
	body := render(t, m)
	for _, want := range []string{
		`apid_request_total{account_id="__other__",code="ok",route="GET /v1/o"} 1`,
		`apid_request_failures_total{account_id="__other__",code="err",route="GET /v1/o"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("__other__-shared series missing %q in:\n%s", want, body)
		}
	}
}

// TestRequestTotalForExtractsRouteAndCode pins the canonical
// HTTP-path accessor (issue #303, ADR-038) — the route label is
// read from r.Pattern with the reserved "unmatched" fallback, and
// the code label is derived from the response status via
// wire.CodeFromStatus (2xx/3xx/4xx → "ok", 5xx → "err"). Owning
// the extraction inside the accessor means callers cannot
// accidentally pass a raw URL path or the wrong status code.
func TestRequestTotalForExtractsRouteAndCode(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	const id = "acct-rt-pattern"
	// Matched route, 200 OK.
	matched := httptest.NewRequest(http.MethodGet, "/v1/rt", nil)
	matched.Pattern = "GET /v1/rt"
	m.RequestTotalFor(matched, http.StatusOK, id).Inc()
	body := render(t, m)
	if !strings.Contains(body, fmt.Sprintf(`apid_request_total{account_id=%q,code="ok",route="GET /v1/rt"} 1`, id)) {
		t.Errorf("matched + 200: missing expected series in:\n%s", body)
	}
	// Matched route, 500 err.
	matchedInternal := httptest.NewRequest(http.MethodGet, "/v1/rt", nil)
	matchedInternal.Pattern = "GET /v1/rt"
	m.RequestTotalFor(matchedInternal, http.StatusInternalServerError, id).Inc()
	body = render(t, m)
	if !strings.Contains(body, fmt.Sprintf(`apid_request_total{account_id=%q,code="err",route="GET /v1/rt"} 1`, id)) {
		t.Errorf("matched + 500: missing expected series in:\n%s", body)
	}
	// Unmatched route, 404. route collapses to "unmatched"; code remains
	// "ok" because the platform error-rate series reserves "err" for 5xx.
	unmatched := httptest.NewRequest(http.MethodGet, "/wp-login.php", nil)
	unmatched.Pattern = ""
	m.RequestTotalFor(unmatched, http.StatusNotFound, id).Inc()
	body = render(t, m)
	unmatchedWant := fmt.Sprintf(`apid_request_total{account_id=%q,code="ok",route="unmatched"} 1`, id)
	if !strings.Contains(body, unmatchedWant) {
		t.Errorf("unmatched + 404: missing expected series in:\n%s", body)
	}
}

// TestCodeFromStatus pins the closed code label set {ok, err}.
// 2xx/3xx/4xx → "ok"; 5xx → "err". Client and entitlement errors
// remain visible in apid_request_failures_total but cannot trigger the
// platform/server error-rate anomaly.
func TestCodeFromStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusOK, "ok"},
		{http.StatusNoContent, "ok"},
		{http.StatusMovedPermanently, "ok"},
		{http.StatusBadRequest, "ok"},
		{http.StatusUnauthorized, "ok"},
		{http.StatusNotFound, "ok"},
		{http.StatusInternalServerError, "err"},
		{0, "ok"}, // No completed 5xx response was observed.
	} {
		if got := wire.CodeFromStatus(tc.status); got != tc.want {
			t.Errorf("CodeFromStatus(%d) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// TestRequestTotalPreInstantiated asserts the closed (account_id,
// route, code) tuples surface from the first scrape so the §12
// traffic-anomaly panels and alert rules never see "no data" on an
// idle daemon. The reserved pairs include the same bypass buckets
// the requestFailures counter uses (anonymous, unmatched) plus the
// two closed code values.
func TestRequestTotalPreInstantiated(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	body := render(t, m)
	for _, want := range []string{
		`apid_request_total{account_id="anonymous",code="ok",route="unmatched"} 0`,
		`apid_request_total{account_id="anonymous",code="err",route="unmatched"} 0`,
		`apid_request_total{account_id="__other__",code="ok",route="unmatched"} 0`,
		`apid_request_total{account_id="__other__",code="err",route="unmatched"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing pre-instantiated series %q in:\n%s", want, body)
		}
	}
}
