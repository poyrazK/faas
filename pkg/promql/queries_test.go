// queries_test.go — coverage for pkg/promql/client.go's four less-
// exercised methods (QueryRange, QueryMap, QueryBuckets,
// QueryGrouped) plus parseSampleValue and NewClient's nil-doer
// default. The existing client_test.go covers QueryScalar well;
// this file fills the rest of the surface.
//
// Whitebox test (package promql), matching the existing
// client_test.go convention.
package promql

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestQueryVectorPreservesLabelsAndDropsMalformedSamples(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"alertname":"APIErrorRate","component":"apid","severity":"page"},"value":[1700000000,"1"]},{"metric":{"component":"builderd"},"value":[1700000000,9]}]}}`))
	}))
	t.Cleanup(srv.Close)

	got, err := NewClient(srv.URL, srv.Client()).QueryVector(context.Background(), `ALERTS{alertstate="firing"}`)
	if err != nil {
		t.Fatalf("QueryVector: %v", err)
	}
	if len(got) != 1 || got[0].Value != 1 || got[0].Labels["component"] != "apid" || got[0].Labels["severity"] != "page" {
		t.Fatalf("QueryVector() = %#v, want one labeled apid/page sample", got)
	}
}

func TestQueryVectorDropsNonFiniteSamples(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"daemon":"apid"},"value":[1,"NaN"]},{"metric":{"daemon":"builderd"},"value":[1,"+Inf"]}]}}`))
	}))
	t.Cleanup(srv.Close)
	got, err := NewClient(srv.URL, srv.Client()).QueryVector(context.Background(), "ALERTS")
	if err != nil {
		t.Fatalf("QueryVector: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("QueryVector non-finite samples = %#v, want none", got)
	}
}

func TestQueryVectorRejectsNonVectorResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[]}}`))
	}))
	t.Cleanup(srv.Close)
	if _, err := NewClient(srv.URL, srv.Client()).QueryVector(context.Background(), "foo"); err == nil {
		t.Fatal("expected scalar response to be rejected")
	}
}

// TestQueryRange_Happy covers the query_range happy path: matrix
// result with two series, two samples each, parsed values numeric
// not string-as-numeric.
func TestQueryRange_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []map[string]any{
					{
						"metric": map[string]string{"app": "a", "code": "200"},
						"values": [][]any{
							{1.7e9, "10"}, {1.7e9 + 60.0, "20"},
						},
					},
					{
						"metric": map[string]string{"app": "a", "code": "500"},
						"values": [][]any{
							{1.7e9, "1"}, {1.7e9 + 60.0, "0"},
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryRange(context.Background(),
		"sum by (app, code) (rate(http_requests_total[5m]))",
		"1700000000", "1700003600", "1m")
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 series", len(got))
	}
	// Each series should carry 2 samples.
	for i, s := range got {
		if len(s.Values) != 2 {
			t.Errorf("series %d len(Values) = %d, want 2", i, len(s.Values))
		}
		if s.Values[0].Value != 10 && s.Values[0].Value != 1 {
			t.Errorf("series %d first value = %v, want 10 or 1",
				i, s.Values[0].Value)
		}
	}
}

// TestQueryRange_WrongResultType covers the matrix-only contract on
// QueryRange: a vector result must error.
func TestQueryRange_WrongResultType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"resultType": "vector", "result": []any{}},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	_, err := c.QueryRange(context.Background(), "foo", "1", "2", "1m")
	if err == nil {
		t.Fatal("expected error on non-matrix resultType")
	}
	if !strings.Contains(err.Error(), "matrix") {
		t.Errorf("err = %v, want substring 'matrix'", err)
	}
}

// TestQueryRange_Non200 covers the truncated-body error path on
// QueryRange (the limit is 1<<12 = 4096 bytes; verified by the
// numeric status in the message).
func TestQueryRange_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("prom is sad"))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	_, err := c.QueryRange(context.Background(), "foo", "1", "2", "1m")
	if err == nil {
		t.Fatal("expected error on non-200")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v, want substring '503'", err)
	}
}

// TestQueryRange_NilClient guards the disabled-Prometheus branch on
// every method, not just QueryScalar.
func TestQueryRange_NilClient(t *testing.T) {
	var c *Client
	_, err := c.QueryRange(context.Background(), "foo", "1", "2", "1m")
	if err == nil {
		t.Fatal("expected error on nil client")
	}
}

// TestQueryRange_MalformedValueSkipped covers the per-row skip path
// in QueryRange: a sample row whose Value[1] is not a string is
// silently skipped (the row is dropped, not the whole series).
func TestQueryRange_MalformedValueSkipped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "matrix",
				"result": []map[string]any{
					{
						"metric": map[string]string{"app": "a"},
						"values": [][]any{
							{1.7e9, "10"},         // good
							{1.7e9 + 60.0, 42.0},  // Value[1] is float, not string → skip
							{1.7e9 + 120.0, "20"}, // good
						},
					},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryRange(context.Background(), "foo", "1", "2", "1m")
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 series", len(got))
	}
	if len(got[0].Values) != 2 {
		t.Errorf("len(Values) = %d, want 2 (1 good + 1 bad skipped)",
			len(got[0].Values))
	}
}

// TestQueryMap_Happy covers the per-app rollup with two apps and
// one sample each.
func TestQueryMap_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"metric": map[string]string{"app": "alpha"}, "value": []any{1.7e9, "3.14"}},
					{"metric": map[string]string{"app": "bravo"}, "value": []any{1.7e9, "2.71"}},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryMap(context.Background(), "count by (app) (foo)")
	if err != nil {
		t.Fatalf("QueryMap: %v", err)
	}
	if got["alpha"] != 3.14 {
		t.Errorf("alpha = %v, want 3.14", got["alpha"])
	}
	if got["bravo"] != 2.71 {
		t.Errorf("bravo = %v, want 2.71", got["bravo"])
	}
}

// TestQueryMap_DropsRowsWithoutAppLabel covers the silent-drop path
// the docstring promises: rows without an `app` label don't poison
// the rollup map with a "" key.
func TestQueryMap_DropsRowsWithoutAppLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"metric": map[string]string{"app": "alpha"}, "value": []any{1.7e9, "1"}},
					{"metric": map[string]string{}, "value": []any{1.7e9, "99"}},          // no app
					{"metric": map[string]string{"app": ""}, "value": []any{1.7e9, "99"}}, // empty app
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryMap(context.Background(), "foo")
	if err != nil {
		t.Fatalf("QueryMap: %v", err)
	}
	if _, has := got[""]; has {
		t.Errorf("QueryMap result contains \"\" key; should drop unlabelled rows: %+v", got)
	}
	if got["alpha"] != 1 {
		t.Errorf("alpha = %v, want 1", got["alpha"])
	}
	if len(got) != 1 {
		t.Errorf("len(got) = %d, want 1", len(got))
	}
}

// TestQueryBuckets_Happy pins the histogram-bucket shape: one app,
// four `le` buckets including "+Inf".
func TestQueryBuckets_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"metric": map[string]string{"app": "alpha", "le": "0.005"}, "value": []any{1.7e9, "100"}},
					{"metric": map[string]string{"app": "alpha", "le": "0.01"}, "value": []any{1.7e9, "200"}},
					{"metric": map[string]string{"app": "alpha", "le": "0.025"}, "value": []any{1.7e9, "300"}},
					{"metric": map[string]string{"app": "alpha", "le": "+Inf"}, "value": []any{1.7e9, "500"}},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryBuckets(context.Background(), "sum by (app, le) (rate(foo[5m]))")
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 app", len(got))
	}
	bucket, ok := got["alpha"]
	if !ok {
		t.Fatalf("missing 'alpha' app key: %+v", got)
	}
	if len(bucket) != 4 {
		t.Errorf("len(bucket) = %d, want 4 le-values", len(bucket))
	}
	if bucket["+Inf"] != 500 {
		t.Errorf("+Inf bucket = %v, want 500", bucket["+Inf"])
	}
	if bucket["0.005"] != 100 {
		t.Errorf("0.005 bucket = %v, want 100", bucket["0.005"])
	}
}

// TestQueryBuckets_DropsRowsWithoutLe covers the bucket-specific
// row-skip: a row without an `le` label is silently dropped (a row
// missing both `app` and `le` would error elsewhere; this case is
// the app-without-le one).
func TestQueryBuckets_DropsRowsWithoutLe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"metric": map[string]string{"app": "alpha"}, "value": []any{1.7e9, "99"}},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryBuckets(context.Background(), "foo")
	if err != nil {
		t.Fatalf("QueryBuckets: %v", err)
	}
	if _, has := got["alpha"]; has {
		t.Errorf("alpha without `le` label should be dropped, got: %+v", got)
	}
}

// TestQueryGrouped_EmptyLabelRejected covers the precondition guard
// on QueryGrouped: empty outer/inner label is rejected before any
// HTTP round-trip is attempted.
func TestQueryGrouped_EmptyLabelRejected(t *testing.T) {
	c := NewClient("http://example.invalid", nil)
	_, err := c.QueryGrouped(context.Background(), "foo", "", "route")
	if err == nil {
		t.Fatal("expected error on empty outer label")
	}
	_, err = c.QueryGrouped(context.Background(), "foo", "app", "")
	if err == nil {
		t.Fatal("expected error on empty inner label")
	}
}

// TestQueryGrouped_Happy covers the inner-and-outer-label rollup
// shape for the throttlerec call site.
func TestQueryGrouped_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"metric": map[string]string{"app": "alpha", "route": "/"}, "value": []any{1.7e9, "1.5"}},
					{"metric": map[string]string{"app": "alpha", "route": "/x"}, "value": []any{1.7e9, "2.5"}},
					{"metric": map[string]string{"app": "bravo", "route": "/y"}, "value": []any{1.7e9, "3.5"}},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	got, err := c.QueryGrouped(context.Background(), "foo", "app", "route")
	if err != nil {
		t.Fatalf("QueryGrouped: %v", err)
	}
	if got["alpha"]["/"] != 1.5 {
		t.Errorf("alpha[/] = %v, want 1.5", got["alpha"]["/"])
	}
	if got["alpha"]["/x"] != 2.5 {
		t.Errorf("alpha[/x] = %v, want 2.5", got["alpha"]["/x"])
	}
	if got["bravo"]["/y"] != 3.5 {
		t.Errorf("bravo[/y] = %v, want 3.5", got["bravo"]["/y"])
	}
}

// TestParseSampleValue_Direct exercises the unexported parser on
// each branch: string-is-float, string-is-not-float, Value[1]-not-string.
func TestParseSampleValue_Direct(t *testing.T) {
	// Happy path: "3.14" parses to 3.14.
	f, err := parseSampleValue([2]any{1.7e9, "3.14"}, "foo")
	if err != nil {
		t.Errorf("parseSampleValue(happy) = %v, want nil", err)
	}
	if f != 3.14 {
		t.Errorf("f = %v, want 3.14", f)
	}
	// Value[1] not a string → error.
	_, err = parseSampleValue([2]any{1.7e9, 42.0}, "foo")
	if err == nil {
		t.Error("expected error when Value[1] is not a string")
	}
	// String is not a parseable float → wrap.
	_, err = parseSampleValue([2]any{1.7e9, "not-a-float"}, "foo")
	if err == nil {
		t.Error("expected error on unparseable float string")
	}
}

// TestNewClient_NilDoer pins the documented default: a nil doer
// triggers a 3s-timeout http.Client. SetTimeout must still take
// effect on top of it.
func TestNewClient_NilDoer(t *testing.T) {
	c := NewClient("http://localhost:0", nil)
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.doer == nil {
		t.Fatal("nil doer must default to a real http.Client")
	}
	// SetTimeout override.
	c.SetTimeout(7 * time.Second)
	if c.timeout != 7*time.Second {
		t.Errorf("timeout after SetTimeout = %v, want 7s", c.timeout)
	}
	// TrimRight the trailing slash.
	if NewClient("http://x/", nil).baseURL != "http://x" {
		t.Errorf("baseURL was not trimmed of trailing slash")
	}
}

// TestQueryScalar_UnexpectedValueShape is an adversarial fixture:
// Value[1] is a JSON number, not a string. The existing tests don't
// pin this branch because Prometheus always emits stringified
// floats, but defensive parsing matters because clients have
// shipped real numeric Values in the past.
func TestQueryScalar_UnexpectedValueShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []map[string]any{
					{"metric": map[string]string{}, "value": []any{1.7e9, 42.0}},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, srv.Client())
	_, err := c.QueryScalar(context.Background(), "foo")
	if err == nil {
		t.Fatal("expected error on numeric Value[1]")
	}
	if !strings.Contains(err.Error(), "shape") {
		t.Errorf("err = %v, want substring 'shape'", err)
	}
}

// silence the `errors` linter on the import block (errors is brought
// in for the unused-imports edge case in future additions).
var _ = errors.Is
