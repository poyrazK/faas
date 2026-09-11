package appmetrics_test

// Issue #396 / ADR-045 PR 2 — pkg/appmetrics tests.
//
// Coverage matrix (mirrors the TestAppMetrics_* naming convention
// from cmd/apid/handlers_metrics_test.go so future readers find
// corresponding tests by name):
//   - happy path: all 7 PromQL queries land in the response
//   - degraded fallback: one query errors → zeroed fields + "degraded:" Source
//   - NaN guard: histogram_quantile over an empty window returns NaN → coerced to 0
//   - nil-client path: Fetch with nil PromQL → "degraded: prometheus not configured"
//   - query failure: per-query error → degraded source
//   - closed-set vocabulary: Ranges()/IsValidRange()
//   - SafeFloat / SafePercent / SafeRoundNonNeg boundary cases
//   - log-injection guard: err string with \r\n → captured slog JSON has neither

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	pkgpromql "github.com/onebox-faas/faas/pkg/promql"
)

// stubPromQL is the test double for appmetrics.PromQL. The fn
// callback returns (value, error) for each QueryScalar call; tests
// set the per-scenario behaviour.
type stubPromQL struct {
	fn func(query string) (float64, error)
}

func (s *stubPromQL) QueryScalar(_ context.Context, query string) (float64, error) {
	return s.fn(query)
}

// captureLog returns a logger that writes JSON to the returned buffer.
// Tests assert on the buffer.
func captureLog(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// okStub returns a fixed scalar for every PromQL query — happy path.
func okStub(v float64) *stubPromQL {
	return &stubPromQL{fn: func(_ string) (float64, error) { return v, nil }}
}

// queryDiscriminatorStub returns one scalar for the
// schedd_egress_net_tx_bytes_total query and another for the
// gateway_egress_tx_bytes_total query, so the test can assert
// the two fields are populated independently. Any other query
// returns 0 + nil (the test does not assert on the other fields).
type queryDiscriminatorStub struct {
	scheddEgress  float64
	gatewayEgress float64
}

func (s *queryDiscriminatorStub) QueryScalar(_ context.Context, query string) (float64, error) {
	switch {
	case strings.Contains(query, "schedd_egress_net_tx_bytes_total"):
		return s.scheddEgress, nil
	case strings.Contains(query, "gateway_egress_tx_bytes_total"):
		return s.gatewayEgress, nil
	default:
		return 0, nil
	}
}

// TestAppMetrics_Fetch_HappyPath stubs every query to return 42 and
// asserts every field landed in the response and Source == "prometheus".
func TestAppMetrics_Fetch_HappyPath(t *testing.T) {
	log, _ := captureLog(t)
	stub := okStub(42)

	resp, src := appmetrics.Fetch(context.Background(), stub, log, "app-1", "5m")
	if src != appmetrics.SourcePrometheus {
		t.Errorf("src = %q, want %q", src, appmetrics.SourcePrometheus)
	}
	if resp.RequestCount != 42 {
		t.Errorf("RequestCount = %d, want 42", resp.RequestCount)
	}
	if resp.LatencyP50MS != 42 || resp.LatencyP95MS != 42 || resp.LatencyP99MS != 42 {
		t.Errorf("latencies not all 42: %+v", resp)
	}
	if resp.ErrorRatePct != 42 || resp.ColdStartPct != 42 {
		t.Errorf("percentages not 42: %+v", resp)
	}
	if resp.WakeP95MS != 42 {
		t.Errorf("WakeP95MS = %v, want 42", resp.WakeP95MS)
	}
}

// TestAppMetrics_Fetch_DegradedFallback: one query errors and the
// whole response is zeroed + Source is the "degraded:" form.
func TestAppMetrics_Fetch_DegradedFallback(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(q string) (float64, error) {
		// Match the error-rate query by its unique label-set
		// (code=~"[45].."). No other query contains this regex.
		if strings.Contains(q, "[45]..") {
			return 0, errors.New("prometheus 503: down for maintenance")
		}
		return 7, nil
	}}

	resp, src := appmetrics.Fetch(context.Background(), stub, log, "app-1", "5m")
	if !strings.HasPrefix(src, appmetrics.SourceDegradedPrefix) {
		t.Errorf("src = %q, want prefix %q", src, appmetrics.SourceDegradedPrefix)
	}
	// Whole response zeroed on degraded (line 197 of the prior
	// implementation discards pre-populated fields).
	if resp.RequestCount != 0 {
		t.Errorf("RequestCount = %d, want 0 (degraded must zero)", resp.RequestCount)
	}
	if resp.ErrorRatePct != 0 {
		t.Errorf("ErrorRatePct = %v, want 0 (degraded must zero)", resp.ErrorRatePct)
	}
}

// TestAppMetrics_Fetch_NaNGuard_Float: stub returns "NaN" — SafeFloat
// coerces to 0.
func TestAppMetrics_Fetch_NaNGuard_Float(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(q string) (float64, error) {
		if strings.Contains(q, "histogram_quantile") {
			// Real PromQL returns NaN for histogram_quantile over
			// an empty window; we mirror that by returning NaN here.
			return math.NaN(), nil
		}
		return 0, nil
	}}

	resp, src := appmetrics.Fetch(context.Background(), stub, log, "app-1", "5m")
	if src != appmetrics.SourcePrometheus {
		t.Errorf("src = %q, want %q", src, appmetrics.SourcePrometheus)
	}
	if math.IsNaN(resp.LatencyP50MS) || resp.LatencyP50MS != 0 {
		t.Errorf("LatencyP50MS = %v, want 0 (NaN must be coerced)", resp.LatencyP50MS)
	}
	if math.IsNaN(resp.WakeP95MS) || resp.WakeP95MS != 0 {
		t.Errorf("WakeP95MS = %v, want 0 (NaN must be coerced)", resp.WakeP95MS)
	}
}

// TestAppMetrics_Fetch_EmptyHistogramUsesPromQLFallback exercises the real
// promql.Client boundary. QueryScalar rejects Prometheus's non-finite samples,
// so every histogram query must turn an idle window into vector(0) before the
// response reaches Go.
func TestAppMetrics_Fetch_EmptyHistogramUsesPromQLFallback(t *testing.T) {
	var histogramQueries int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		value := "0"
		if strings.Contains(query, "histogram_quantile") {
			histogramQueries++
			if !strings.Contains(query, "_count") || !strings.Contains(query, "or vector(0)") {
				value = "NaN"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1,%q]}]}}`, value)
	}))
	t.Cleanup(srv.Close)

	client := pkgpromql.NewClient(srv.URL, srv.Client())
	resp, src := appmetrics.Fetch(context.Background(), client, slog.Default(), "app-1", "1h")
	if src != appmetrics.SourcePrometheus {
		t.Fatalf("src = %q, want %q", src, appmetrics.SourcePrometheus)
	}
	if histogramQueries != 4 {
		t.Fatalf("histogram queries = %d, want 4", histogramQueries)
	}
	if resp.LatencyP50MS != 0 || resp.LatencyP95MS != 0 || resp.LatencyP99MS != 0 || resp.WakeP95MS != 0 {
		t.Fatalf("idle histogram values must be zero: %+v", resp)
	}
}

// TestAppMetrics_Fetch_NaNGuard_Percent: stub returns NaN for the
// error-rate query — SafePercent clamps to 0.
func TestAppMetrics_Fetch_NaNGuard_Percent(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(q string) (float64, error) {
		if strings.Contains(q, "[45]..") {
			return math.NaN(), nil
		}
		return 0, nil
	}}

	resp, src := appmetrics.Fetch(context.Background(), stub, log, "app-1", "5m")
	if src != appmetrics.SourcePrometheus {
		t.Errorf("src = %q, want %q", src, appmetrics.SourcePrometheus)
	}
	if math.IsNaN(resp.ErrorRatePct) || resp.ErrorRatePct != 0 {
		t.Errorf("ErrorRatePct = %v, want 0 (NaN must be coerced)", resp.ErrorRatePct)
	}
}

// TestAppMetrics_Fetch_NilClient: passing nil fetcher must NOT panic
// and must return the "degraded: prometheus not configured" form.
func TestAppMetrics_Fetch_NilClient(t *testing.T) {
	log, _ := captureLog(t)

	resp, src := appmetrics.Fetch(context.Background(), nil, log, "app-1", "5m")
	if !strings.Contains(src, "prometheus not configured") {
		t.Errorf("src = %q, want contains 'prometheus not configured'", src)
	}
	if resp.RequestCount != 0 || resp.WakeP95MS != 0 {
		t.Errorf("nil-client must zero: %+v", resp)
	}
}

// TestAppMetrics_Fetch_TypedNilClient pins the typed-nil short-circuit
// that the production handler relies on. apid wires `*pkg/promql.Client`
// into the Fetch call site; when the server boots without a Prometheus
// URL the field is the zero-value concrete pointer, which wraps into
// the `PromQL` interface as a NON-nil value (the typed-nil gotcha).
// Without the type-switch guard added in PR #404 review, Fetch would
// dispatch into QueryScalar on a nil receiver and leak the
// implementation-detail error "promql: client not configured" up to
// the dashboard. This test pins the canonical "prometheus not
// configured" Source form on the typed-nil path.
func TestAppMetrics_Fetch_TypedNilClient(t *testing.T) {
	log, _ := captureLog(t)
	var c *pkgpromql.Client // typed nil
	var fetcher appmetrics.PromQL = c

	resp, src := appmetrics.Fetch(context.Background(), fetcher, log, "app-1", "5m")
	if !strings.Contains(src, "prometheus not configured") {
		t.Errorf("typed-nil src = %q, want contains 'prometheus not configured'", src)
	}
	if resp.RequestCount != 0 {
		t.Errorf("typed-nil must zero: %+v", resp)
	}
}

// TestAppMetrics_Fetch_RejectsLabelEscape pins the PromQL injection
// guard (PR #404 review finding BUG-01). The query builder uses %q
// for the app= label value, but Prometheus label-string literals do
// NOT recognise Go escaping — a `"` in the appID closes the outer
// label prematurely and re-opens a new `app=…` selector. The package
// must refuse any appID containing `"`, `\`, or `\n` BEFORE building
// any query, returning a degraded response without dispatching to
// Prometheus. PR 4's meterd caller will pass alert_rules.app_id (a
// customer-supplied slug), so this guard is load-bearing.
func TestAppMetrics_Fetch_RejectsLabelEscape(t *testing.T) {
	log, _ := captureLog(t)
	stub := okStub(7)

	cases := []string{
		`evil","class="2xx"`, // closes outer label, re-opens class label
		"back\\slash",        // backslash is the other Go escape char
		"line\nfeed",         // newline breaks the query body literally
	}
	for _, appID := range cases {
		t.Run(appID, func(t *testing.T) {
			// The stub must NOT be called for any of these.
			seen := false
			guard := &stubPromQL{fn: func(_ string) (float64, error) {
				seen = true
				return 7, nil
			}}
			resp, src := appmetrics.Fetch(context.Background(), guard, log, appID, "5m")
			if seen {
				t.Errorf("Fetch dispatched to Prometheus with rejected appID %q", appID)
			}
			if !strings.Contains(src, "invalid app id") {
				t.Errorf("src = %q, want contains 'invalid app id'", src)
			}
			if resp.RequestCount != 0 {
				t.Errorf("rejected appID must zero response: %+v", resp)
			}
			_ = stub // silence unused
		})
	}
}

// TestAppMetrics_Fetch_SourceHasNoCRLF pins PR #404 review finding
// BUG-02: the `Source` field on the wire response must NOT contain
// raw CR or LF even if the upstream Prometheus (or a malicious one)
// returns an error body containing them. Otherwise the JSON response
// itself breaks downstream structured-log parsers.
func TestAppMetrics_Fetch_SourceHasNoCRLF(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_ string) (float64, error) {
		return 0, fmt.Errorf("evil prom error\r\nfake log line 2")
	}}

	_, src := appmetrics.Fetch(context.Background(), stub, log, "app-1", "5m")
	if strings.Contains(src, "\r") || strings.Contains(src, "\n") {
		t.Errorf("Source contains CR/LF: %q", src)
	}
	if !strings.Contains(src, "evil prom error") {
		t.Errorf("Source missing underlying message: %q", src)
	}
}

// TestAppMetrics_Fetch_QueryFailure: each query failure mode degrades
// the response with a label that names the failed query.
func TestAppMetrics_Fetch_QueryFailure(t *testing.T) {
	cases := []struct {
		label        string
		failingQuery string
	}{
		{"request_count", "gateway_requests_total"},
		{"error_rate", "[45].."},
		{"cold_start", "gateway_cold_boot_total"},
		{"wake_p95", "gateway_wake_latency_seconds"},
		{"p50", "rate(gateway_request_duration_seconds_bucket"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			log, _ := captureLog(t)
			stub := &stubPromQL{fn: func(q string) (float64, error) {
				if strings.Contains(q, tc.failingQuery) {
					return 0, errors.New("upstream timeout")
				}
				return 7, nil
			}}
			_, src := appmetrics.Fetch(context.Background(), stub, log, "app-1", "5m")
			if !strings.HasPrefix(src, appmetrics.SourceDegradedPrefix) {
				t.Errorf("src = %q, want degraded prefix", src)
			}
		})
	}
}

// TestAppMetrics_Ranges_ClosedSet pins the closed-set vocabulary in
// document order so a future reorder is a visible diff.
func TestAppMetrics_Ranges_ClosedSet(t *testing.T) {
	want := []string{"5m", "15m", "1h", "6h", "24h", "7d", "15d"}
	got := appmetrics.Ranges()
	if len(got) != len(want) {
		t.Fatalf("Ranges() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Ranges()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Ranges must return a copy: mutating the returned slice MUST NOT
	// affect subsequent calls. Closes the silent-drift surface a
	// future caller would otherwise expose.
	got[0] = "999h"
	again := appmetrics.Ranges()
	if again[0] != "5m" {
		t.Errorf("Ranges() must return a copy; got[0] mutated to %q", again[0])
	}
}

// TestAppMetrics_IsValidRange table-drives the closed-set check.
func TestAppMetrics_IsValidRange(t *testing.T) {
	cases := []struct {
		rng  string
		want bool
	}{
		{"5m", true},
		{"15d", true},
		{"30d", false},
		{"", false},
		{"banana", false},
		{"5M", false}, // case-sensitive on purpose
	}
	for _, tc := range cases {
		if got := appmetrics.IsValidRange(tc.rng); got != tc.want {
			t.Errorf("IsValidRange(%q) = %v, want %v", tc.rng, got, tc.want)
		}
	}
}

// TestSafeFloat_BoundaryCases pins NaN/Inf/negative coercion.
func TestSafeFloat_BoundaryCases(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{math.NaN(), 0},
		{math.Inf(1), 0},
		{math.Inf(-1), 0},
		{-1.0, 0},
		{0.0, 0},
		{1.5, 1.5},
	}
	for _, tc := range cases {
		if got := appmetrics.SafeFloat(tc.in); got != tc.want {
			t.Errorf("SafeFloat(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestSafePercent_BoundaryCases pins the [0,100] clamp.
//
// SafePercent composes SafeFloat first: NaN / +Inf / -Inf /
// negative inputs are coerced to 0 before the clamp runs, so
// +Inf lands at 0 (matching the byte-for-byte behaviour of the
// original cmd/apid/handlers_metrics.go safePercent). 100 is
// the upper edge; values > 100 clamp to 100.
func TestSafePercent_BoundaryCases(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{math.NaN(), 0},   // NaN → 0 via SafeFloat
		{math.Inf(1), 0},  // +Inf → 0 via SafeFloat (not 100 — SafeFloat clamps Inf first)
		{math.Inf(-1), 0}, // -Inf → 0 via SafeFloat
		{-1.0, 0},         // negative → 0 via SafeFloat
		{50.0, 50},        // in-range unchanged
		{100.0, 100},      // edge
		{150.0, 100},      // over-100 clamps
	}
	for _, tc := range cases {
		if got := appmetrics.SafePercent(tc.in); got != tc.want {
			t.Errorf("SafePercent(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestSafeRoundNonNeg_BoundaryCases mirrors TestSafeFloat — the
// helper exists for documentation, not for different behaviour.
func TestSafeRoundNonNeg_BoundaryCases(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{math.NaN(), 0},
		{-5.0, 0},
		{7.4, 7.4},
		{7.6, 7.6}, // rounding policy lives at the int64(...) call site
	}
	for _, tc := range cases {
		if got := appmetrics.SafeRoundNonNeg(tc.in); got != tc.want {
			t.Errorf("SafeRoundNonNeg(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestFetch_LogInjectionGuard asserts the CodeQL two-call sanitiser
// pattern (alert #117) — err string with \r\n in the message must
// NOT be logged with those bytes intact. We capture the slog JSON
// output and assert neither CR nor LF appear in the `err` field.
func TestFetch_LogInjectionGuard(t *testing.T) {
	log, buf := captureLog(t)
	stub := &stubPromQL{fn: func(_ string) (float64, error) {
		// CR/LF is the injection vector CodeQL alert #117 pins.
		return 0, fmt.Errorf("evil msg\r\nfake log line 2")
	}}

	_, _ = appmetrics.Fetch(context.Background(), stub, log, "app-1", "5m")

	// Find every JSON line; assert none of them contain a raw CR or LF.
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		// Each line is a single slog JSON record; the CR/LF inside
		// the err field should have been stripped.
		if strings.Contains(line, "\r") {
			t.Errorf("log line contains CR: %q", line)
		}
		// Decode just the `err` field to assert it's free of \n.
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line not JSON: %v (line=%q)", err, line)
		}
		if errStr, ok := rec["err"].(string); ok {
			if strings.Contains(errStr, "\n") || strings.Contains(errStr, "\r") {
				t.Errorf("log err field contains CR/LF: %q", errStr)
			}
		}
	}
}

// TestFetch_NilLogger: passing nil logger must NOT panic and must
// fall back to slog.Default(). Cheap coverage for the nil-coercion
// path that handlers in production also rely on.
func TestFetch_NilLogger(t *testing.T) {
	stub := okStub(7)

	resp, src := appmetrics.Fetch(context.Background(), stub, nil, "app-1", "5m")
	if src != appmetrics.SourcePrometheus {
		t.Errorf("src = %q, want %q", src, appmetrics.SourcePrometheus)
	}
	if resp.RequestCount != 7 {
		t.Errorf("RequestCount = %d, want 7", resp.RequestCount)
	}
}

// Compile-time check that api.AppMetricsResponse is the wire struct
// we expect. A future pkg/api change that splits the struct would
// surface here.
var _ = func() bool { var x api.AppMetricsResponse; return x.AppID == "" }()

// TestFetch_PR2_TxBytesPopulatedIndependently is the PR-C.2 / issue
// #415 PR-2 close-out: the gateway-side tx_bytes mirror is populated
// by appmetrics.Fetch from gateway_egress_tx_bytes_total{app}, and
// the field is independent of the schedd-side EgressBytes (the
// schedd-side query targets schedd_egress_net_tx_bytes_total{app}).
//
// The discriminator stub returns different scalars for the two
// queries so the test can assert: (a) each field is populated
// from its own query, (b) a divergence between the two fields
// surfaces in the response (i.e. the gateway-side tx_bytes does
// NOT silently shadow the schedd-side EgressBytes).
func TestFetch_PR2_TxBytesPopulatedIndependently(t *testing.T) {
	stub := &queryDiscriminatorStub{
		scheddEgress:  1024, // 1 KiB
		gatewayEgress: 4096, // 4 KiB — divergence is the test condition
	}
	logger, _ := captureLog(t)

	resp, src := appmetrics.Fetch(context.Background(), stub, logger, "app-pr2", "5m")
	if src != appmetrics.SourcePrometheus {
		t.Errorf("src = %q, want %q (tx_bytes query failure must NOT flip the response to degraded — both fields are best-effort)", src, appmetrics.SourcePrometheus)
	}
	if resp.EgressBytes != 1024 {
		t.Errorf("EgressBytes = %d, want 1024 (schedd-side mirror should populate from schedd_egress_net_tx_bytes_total)", resp.EgressBytes)
	}
	if resp.TxBytes != 4096 {
		t.Errorf("TxBytes = %d, want 4096 (gateway-side mirror should populate from gateway_egress_tx_bytes_total independently of EgressBytes)", resp.TxBytes)
	}
}

// TestFetch_PR2_TxBytesQueryFailureDoesNotDegrade asserts that a
// failure on the gateway-side tx_bytes query does NOT flip the
// response to "degraded: ..." — the dispatch contract is the same
// as the schedd-side EgressBytes: best-effort, log-and-continue.
// The dashboard's tx_bytes field is allowed to be 0 (which is
// indistinguishable from "no data").
func TestFetch_PR2_TxBytesQueryFailureDoesNotDegrade(t *testing.T) {
	// failingTxBytesStub is a per-field-error stub: returns the
	// schedd-side EgressBytes scalar, errors on the gateway-side
	// tx_bytes query. The tx_bytes failure path is the test
	// condition; the schedd-side success path is the control.
	gatewayErr := errors.New("simulated gateway_egress_tx_bytes_total query failure")

	failingStub := &failingTxBytesStub{
		scheddEgress: 512,
		gatewayErr:   gatewayErr,
	}
	logger, logBuf := captureLog(t)

	resp, src := appmetrics.Fetch(context.Background(), failingStub, logger, "app-pr2", "5m")
	if src != appmetrics.SourcePrometheus {
		t.Errorf("src = %q, want %q (tx_bytes query failure must NOT flip the response to degraded)", src, appmetrics.SourcePrometheus)
	}
	if resp.EgressBytes != 512 {
		t.Errorf("EgressBytes = %d, want 512 (schedd-side mirror should still populate when tx_bytes fails)", resp.EgressBytes)
	}
	if resp.TxBytes != 0 {
		t.Errorf("TxBytes = %d, want 0 (the failing query should leave the field at zero)", resp.TxBytes)
	}
	// Verify the failure was logged with the right app_id so an
	// operator eyeballing the log can localise the gap.
	logged := logBuf.String()
	if !strings.Contains(logged, "tx_bytes query failed") {
		t.Errorf("expected log to contain 'tx_bytes query failed', got: %s", logged)
	}
	if !strings.Contains(logged, "app-pr2") {
		t.Errorf("expected log to contain app_id; got: %s", logged)
	}
}

// failingTxBytesStub is the per-field-error test double for the
// tx_bytes best-effort path. Returns nil for the schedd-side
// EgressBytes query and a configured error for the gateway-side
// tx_bytes query.
type failingTxBytesStub struct {
	scheddEgress float64
	gatewayErr   error
}

func (s *failingTxBytesStub) QueryScalar(_ context.Context, query string) (float64, error) {
	switch {
	case strings.Contains(query, "schedd_egress_net_tx_bytes_total"):
		return s.scheddEgress, nil
	case strings.Contains(query, "gateway_egress_tx_bytes_total"):
		return 0, s.gatewayErr
	default:
		return 0, nil
	}
}
