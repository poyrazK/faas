package appmetrics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
)

func TestRequestHealth_SameWindowAndFreshness(t *testing.T) {
	for _, tt := range []struct {
		name       string
		failSignal string
		want       string
	}{
		{"available", "", SourcePrometheus},
		{"count failure", "sum(increase(gateway_request_duration_by_deployment_seconds_count{app=\"app\",deployment=~\"serving\"}[", SourceDegraded},
		{"5xx failure", "class=\"5xx\"", SourceDegraded},
		{"coverage failure", "count(count by", SourceDegraded},
		{"freshness failure", "timestamp(", SourceDegraded},
		{"unattributed failure", "__other__", SourceDegraded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			queries := []string{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query().Get("query")
				queries = append(queries, q)
				if tt.failSignal != "" && strings.Contains(q, tt.failSignal) {
					http.Error(w, "private backend error", 500)
					return
				}
				value := 100.0
				if strings.Contains(q, "class=\"5xx\"") {
					value = 5
				}
				if strings.Contains(q, "timestamp(") {
					value = 1791201600
				}
				if strings.Contains(q, "count(count by") {
					value = 1
				}
				if strings.Contains(q, "__other__") {
					value = 0
				}
				_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"%g"]}]}}`, value)
			}))
			defer srv.Close()
			out := FetchRequestHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), "app", []string{"serving"})
			if out.Source != tt.want {
				t.Fatalf("%+v", out)
			}
			if tt.want == SourcePrometheus {
				if out.RequestCount != 100 || out.ServerErrors != 5 || out.ErrorRatePct != 5 {
					t.Fatalf("%+v", out)
				}
				if _, err := time.Parse(time.RFC3339Nano, out.AsOf); err != nil {
					t.Fatal(err)
				}
				if len(queries) != 5 {
					t.Fatalf("%d queries", len(queries))
				}
				for _, q := range queries {
					if !strings.Contains(q, "gateway_request_duration_by_deployment_seconds_count") || !strings.Contains(q, `app="app"`) || !strings.Contains(q, "deployment=") {
						t.Fatalf("unscoped query: %s", q)
					}
					if strings.Contains(q, "quantile") || strings.Contains(q, "wake") {
						t.Fatalf("unrelated signal queried: %s", q)
					}
					if (strings.Contains(q, "count(count by") || strings.Contains(q, "__other__")) && !strings.Contains(q, "count_over_time(") {
						t.Fatalf("partial counter samples could be hidden: %s", q)
					}
					// Gateway-answered refusals (401 without an API key, CORS
					// preflights) are unattributed but not release failures.
					if strings.Contains(q, "__other__") && (!strings.Contains(q, `deployment="__other__"}`) || !strings.Contains(q, `deployment="",class="5xx"}`)) {
						t.Fatalf("unattributed check must block on any overflow and on pre-routing 5xx only: %s", q)
					}
				}
			}
		})
	}
}

// increase() extrapolates, so the counts are rounded; the rate is of the
// rounded counts it is shown with (prod hunt r33: "24 of 28 requests
// returned 5xx (86.75%)").
func TestRequestHealth_RateMatchesShownCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		value := 28.4
		switch {
		case strings.Contains(q, "__other__"):
			value = 0
		case strings.Contains(q, `class="5xx"`):
			value = 24.3
		case strings.Contains(q, "timestamp("):
			value = 1791201600
		case strings.Contains(q, "count(count by"):
			value = 1
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"%g"]}]}}`, value)
	}))
	defer srv.Close()
	out := FetchRequestHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), "app", []string{"serving"})
	if out.Source != SourcePrometheus || out.RequestCount != 28 || out.ServerErrors != 24 {
		t.Fatalf("%+v", out)
	}
	if got := fmt.Sprintf("%.2f", out.ErrorRatePct); got != "85.71" {
		t.Fatalf("rate %s, want 85.71 (24 of 28)", got)
	}
}

func TestRequestHealth_Disabled(t *testing.T) {
	out := FetchRequestHealth(t.Context(), nil, "app", []string{"serving"})
	if out.Source != SourceDegraded || out.AsOf != "" {
		t.Fatalf("%+v", out)
	}
}

func TestRequestHealth_IncompleteOrInvalidEvidence(t *testing.T) {
	for _, tt := range []struct {
		name, signal, value, reason string
	}{
		{"missing release samples", "count(count by", "1", "request_coverage_incomplete"},
		{"overflow or pre-routing requests", "__other__", "0.1", "request_coverage_incomplete"},
		{"invalid count", "sum(increase", "NaN", "request_evidence_unavailable"},
		{"invalid coverage", "count(count by", "NaN", "request_evidence_unavailable"},
		{"invalid timestamp", "timestamp(", "1e20", "request_evidence_unavailable"},
		{"failures exceed requests", `class="5xx"`, "101", "request_evidence_unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query().Get("query")
				value := "100"
				switch {
				case strings.Contains(q, "count(count by"):
					value = "2"
				case strings.Contains(q, "timestamp("):
					value = "1791201600"
				case strings.Contains(q, "__other__"), strings.Contains(q, `class="5xx"`):
					value = "0"
				}
				if strings.Contains(q, tt.signal) {
					value = tt.value
				}
				_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[0,%q]}]}}`, value)
			}))
			defer srv.Close()
			out := FetchRequestHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), "app", []string{"serving", "canary"})
			if out.Source != SourceDegraded || out.Reason != tt.reason || out.AsOf != "" || out.RequestCount != 0 {
				t.Fatalf("%+v", out)
			}
		})
	}
}

// No series for the release: unobserved while the gateway exports series
// for other apps, unavailable when it exports none at all.
func TestRequestHealth_UnobservedRelease(t *testing.T) {
	for _, tt := range []struct {
		name, live, want string
	}{
		{"pipeline live", `[{"value":[0,"3"]}]`, RequestsUnobservedReason},
		{"no series anywhere", `[]`, "request_evidence_unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result := `[]`
				if r.URL.Query().Get("query") == `count(gateway_request_duration_by_deployment_seconds_count)` {
					result = tt.live
				}
				_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":%s}}`, result)
			}))
			defer srv.Close()
			out := FetchRequestHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), "app", []string{"serving"})
			if out.Source != SourceDegraded || out.Reason != tt.want || out.RequestCount != 0 {
				t.Fatalf("%+v", out)
			}
		})
	}
}

// After a gateway restart a release's series has one scrape and no request:
// count is 0 and the coverage query has no result. That is an idle release.
func TestRequestHealth_RestartedIdleSeries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		result := `[{"value":[0,"0"]}]`
		switch {
		case strings.Contains(q, "count(count by"):
			result = `[]`
		case q == `count(gateway_request_duration_by_deployment_seconds_count)`:
			result = `[{"value":[0,"4"]}]`
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":%s}}`, result)
	}))
	defer srv.Close()
	out := FetchRequestHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), "app", []string{"serving"})
	if out.Source != SourceDegraded || out.Reason != RequestsUnobservedReason || out.RequestCount != 0 {
		t.Fatalf("%+v", out)
	}
}
