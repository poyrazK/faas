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
		{"count failure", "sum(increase(gateway_request_duration_seconds_count{app=\"app\"}[", SourceDegraded},
		{"5xx failure", "class=\"5xx\"", SourceDegraded},
		{"freshness failure", "timestamp(", SourceDegraded},
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
				_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"%g"]}]}}`, value)
			}))
			defer srv.Close()
			out, source := FetchRequestHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), "app")
			if source != tt.want {
				t.Fatalf("%s %+v", source, out)
			}
			if tt.want == SourcePrometheus {
				if out.RequestCount != 100 || out.ErrorRatePct != 5 {
					t.Fatalf("%+v", out)
				}
				if _, err := time.Parse(time.RFC3339Nano, out.AsOf); err != nil {
					t.Fatal(err)
				}
				if len(queries) != 3 {
					t.Fatalf("%d queries", len(queries))
				}
				for _, q := range queries {
					if strings.Contains(q, "quantile") || strings.Contains(q, "wake") {
						t.Fatalf("unrelated signal queried: %s", q)
					}
				}
			}
		})
	}
}

func TestRequestHealth_Disabled(t *testing.T) {
	out, source := FetchRequestHealth(t.Context(), nil, "app")
	if source != SourceDegraded || out.AsOf != "" {
		t.Fatalf("%s %+v", source, out)
	}
}
