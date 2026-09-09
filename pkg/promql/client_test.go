package promql

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestQueryScalarVector covers the happy path with a vector result.
func TestQueryScalarVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"metric": map[string]string{"foo": "bar"}, "value": []any{1.7e9, "42.5"}},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryScalar(context.Background(), "sum(foo)")
	if err != nil {
		t.Fatalf("QueryScalar: %v", err)
	}
	if got != 42.5 {
		t.Errorf("got %v, want 42.5", got)
	}
}

// TestQueryScalarScalarResult covers the scalar resultType branch
// (the alert-state query the §12 degraded flag depends on).
func TestQueryScalarScalarResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "scalar", "result": []map[string]any{{"value": []any{1.7e9, "1"}}}},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryScalar(context.Background(), "count(foo) > 0")
	if err != nil {
		t.Fatalf("QueryScalar: %v", err)
	}
	if got != 1 {
		t.Errorf("got %v, want 1", got)
	}
}

// TestQueryScalarEmptyResult asserts the "no data" error path.
func TestQueryScalarEmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "vector", "result": []map[string]any{}},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	if _, err := c.QueryScalar(context.Background(), "foo"); err == nil {
		t.Fatal("expected error on empty result")
	} else if !strings.Contains(err.Error(), "no data") {
		t.Errorf("err = %v, want 'no data'", err)
	}
}

func TestQueryScalarRejectsNonFiniteResult(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(value, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"` + value + `"]}]}}`))
			}))
			t.Cleanup(srv.Close)
			c := NewClient(srv.URL, srv.Client())
			if _, err := c.QueryScalar(context.Background(), "histogram_quantile(...)"); err == nil {
				t.Fatal("expected error on non-finite result")
			} else if !strings.Contains(err.Error(), "non-finite") {
				t.Fatalf("err = %v, want non-finite error", err)
			}
		})
	}
}

// TestQueryScalarNon200 asserts non-200 → error.
func TestQueryScalarNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("down"))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	if _, err := c.QueryScalar(context.Background(), "foo"); err == nil {
		t.Fatal("expected error on non-200")
	} else if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want substring '500'", err)
	}
}

// TestQueryScalarUnsupportedResultType asserts matrix/string errors.
func TestQueryScalarUnsupportedResultType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "matrix", "result": []any{}},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	if _, err := c.QueryScalar(context.Background(), "foo"); err == nil {
		t.Fatal("expected error on unsupported resultType")
	} else if !strings.Contains(err.Error(), "matrix") {
		t.Errorf("err = %v, want substring 'matrix'", err)
	}
}

// TestQueryScalarNilClient guards the "client not configured" path
// the dashboard's nil-promql branch relies on.
func TestQueryScalarNilClient(t *testing.T) {
	var c *Client
	if _, err := c.QueryScalar(context.Background(), "foo"); err == nil {
		t.Fatal("expected error on nil client")
	}
}

// TestNewClientEmptyBaseURL guards the disabled-Prometheus path.
func TestNewClientEmptyBaseURL(t *testing.T) {
	c := NewClient("", nil)
	if _, err := c.QueryScalar(context.Background(), "foo"); err == nil {
		t.Fatal("expected error on empty base URL")
	}
}

// TestQueryScalarContextTimeout asserts the per-query timeout fires.
// The test upstream sleeps 5s; the client times out at 50ms. After
// the test body returns, httptest.Server.Close() interrupts the
// sleeping handler so cleanup is bounded.
func TestQueryScalarContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
			_, _ = w.Write([]byte("late"))
		case <-r.Context().Done():
			return
		}
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	c.SetTimeout(50 * time.Millisecond)
	if _, err := c.QueryScalar(context.Background(), "foo"); err == nil {
		t.Fatal("expected timeout error")
	}
}
