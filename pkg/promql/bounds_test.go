// adr: 042 — PromQL responses must remain bounded at the metrics boundary.

package promql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQueryScalarRejectsOversizedSuccessResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
		_, _ = w.Write([]byte(strings.Repeat(" ", promqlResponseMaxBytes)))
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, srv.Client()).QueryScalar(context.Background(), "up")
	if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("QueryScalar oversized response error = %v, want response-body limit", err)
	}
}

func TestQueryVectorRejectsTooManySeries(t *testing.T) {
	rows := make([]map[string]any, promqlMaxSeries+1)
	for i := range rows {
		rows[i] = map[string]any{
			"metric": map[string]string{"app": "app"},
			"value":  []any{1.7e9, "1"},
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result":     rows,
			},
		})
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, srv.Client()).QueryVector(context.Background(), "up")
	if err == nil || !strings.Contains(err.Error(), "series") {
		t.Fatalf("QueryVector series-limit error = %v, want series limit", err)
	}
}

func TestQueryRangeRejectsTooManySamples(t *testing.T) {
	values := make([][]any, promqlMaxSamples+1)
	for i := range values {
		values[i] = []any{float64(i), "1"}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []map[string]any{{
					"metric": map[string]string{"app": "app"},
					"values": values,
				}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	_, err := NewClient(srv.URL, srv.Client()).QueryRange(context.Background(), "up", "1", "2", "1m")
	if err == nil || !strings.Contains(err.Error(), "samples") {
		t.Fatalf("QueryRange sample-limit error = %v, want sample limit", err)
	}
}
