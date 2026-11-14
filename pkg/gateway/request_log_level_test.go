package gateway

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
)

// The request path builds a request's log attributes only when the logger
// will write the line; a warm 2xx is a Debug line production drops.
func TestObserveWritesTheRequestLogOnlyAtItsLevel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		level  slog.Level
		status int
		cold   bool
		want   bool
	}{
		{"warm 2xx at info", slog.LevelInfo, http.StatusOK, false, false},
		{"warm 2xx at debug", slog.LevelDebug, http.StatusOK, false, true},
		{"cold 2xx at info", slog.LevelInfo, http.StatusOK, true, true},
		{"4xx at info", slog.LevelInfo, http.StatusNotFound, false, true},
		{"5xx at info", slog.LevelInfo, http.StatusServiceUnavailable, false, true},
		{"5xx at warn", slog.LevelWarn, http.StatusServiceUnavailable, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := &Handler{log: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: tc.level}))}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r = r.WithContext(context.WithValue(r.Context(), effectiveTrafficPolicyKey{}, "traffic-v1:abc"))
			h.observe(r, tc.status, "app-1", string(api.PlanPro), tc.cold, Target{})
			got := buf.String()
			if wrote := strings.Contains(got, `"msg":"gateway_request"`); wrote != tc.want {
				t.Fatalf("wrote the request line = %t, want %t: %s", wrote, tc.want, got)
			}
			if tc.want && !strings.Contains(got, `"traffic_policy_revision":"traffic-v1:abc"`) {
				t.Fatalf("request line lost its policy revision: %s", got)
			}
		})
	}
}

func TestRequestLogLevel(t *testing.T) {
	for _, tc := range []struct {
		code string
		cold bool
		want slog.Level
	}{
		{"200", false, slog.LevelDebug},
		{"204", false, slog.LevelDebug},
		{"200", true, slog.LevelInfo},
		{"301", false, slog.LevelInfo},
		{"503", false, slog.LevelInfo},
		{"2", false, slog.LevelInfo},
		{"", false, slog.LevelInfo},
	} {
		if got := requestLogLevel(tc.code, tc.cold); got != tc.want {
			t.Errorf("requestLogLevel(%q, %t) = %v, want %v", tc.code, tc.cold, got, tc.want)
		}
	}
}

// Info request lines spend the request-log budget. Past it a line is not
// built, the metric counts it, and the next written line carries the count
// once.
func TestObserveBudgetsInfoRequestLines(t *testing.T) {
	var buf bytes.Buffer
	m := NewMetrics()
	h := &Handler{
		log:              slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})),
		metrics:          m,
		requestLogBudget: &requestLogBudget{limiter: rate.NewLimiter(0, 2)},
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for range 5 {
		h.observe(r, http.StatusNotFound, "-", "", false, Target{})
	}
	// A warm 2xx is a Debug line: neither written at Info nor budgeted.
	h.observe(r, http.StatusOK, "app-1", string(api.PlanPro), false, Target{})
	if got := strings.Count(buf.String(), `"msg":"gateway_request"`); got != 2 {
		t.Fatalf("wrote %d request lines, want the budget's 2: %s", got, buf.String())
	}
	if got := testutil.ToFloat64(m.requestLogSuppressed); got != 3 {
		t.Fatalf("gateway_request_log_suppressed_total = %v, want 3", got)
	}
	if strings.Contains(buf.String(), "suppressed_before") {
		t.Fatalf("a line written within the budget claims suppressed lines: %s", buf.String())
	}

	for _, want := range []string{`"suppressed_before":3`, ""} {
		buf.Reset()
		h.requestLogBudget.limiter = rate.NewLimiter(0, 1)
		h.observe(r, http.StatusServiceUnavailable, "app-1", string(api.PlanPro), false, Target{})
		got := buf.String()
		if !strings.Contains(got, `"msg":"gateway_request"`) {
			t.Fatalf("no request line once the budget refilled: %q", got)
		}
		if want == "" && strings.Contains(got, "suppressed_before") {
			t.Fatalf("the suppressed count was carried twice: %s", got)
		}
		if want != "" && !strings.Contains(got, want) {
			t.Fatalf("the next written line lacks %s: %s", want, got)
		}
	}
}

func TestRequestLogBudgetAdmit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget *requestLogBudget
		level  slog.Level
		want   bool
	}{
		{"nil budget admits info", nil, slog.LevelInfo, true},
		{"spent budget refuses info", &requestLogBudget{limiter: rate.NewLimiter(0, 0)}, slog.LevelInfo, false},
		{"spent budget still admits debug", &requestLogBudget{limiter: rate.NewLimiter(0, 0)}, slog.LevelDebug, true},
		{"budget admits info", &requestLogBudget{limiter: rate.NewLimiter(0, 1)}, slog.LevelInfo, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.budget.admit(tc.level, nil); got != tc.want {
				t.Fatalf("admit(%v) = %t, want %t", tc.level, got, tc.want)
			}
		})
	}
}

// BenchmarkObserveNotFound is the request-log cost of one 404 for a host that
// routes nowhere: written within the budget, and suppressed past it.
func BenchmarkObserveNotFound(b *testing.B) {
	for _, bc := range []struct {
		name   string
		budget *requestLogBudget
	}{
		{"written", nil},
		{"suppressed", &requestLogBudget{limiter: rate.NewLimiter(0, 0)}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			h := &Handler{
				log:              slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})),
				metrics:          NewMetrics(),
				requestLogBudget: bc.budget,
			}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			b.ReportAllocs()
			for b.Loop() {
				h.observe(r, http.StatusNotFound, "-", "", false, Target{})
			}
		})
	}
}
