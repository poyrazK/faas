package appmetrics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/promql"
)

// Exercise the production expressions against Prometheus's actual evaluation
// engine. Optional locally; the HTTP/evaluator tests run without this binary.
func TestRequestHealth_PromQL(t *testing.T) {
	tool, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool is not installed")
	}
	queries := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		v := 100.0
		switch {
		case strings.Contains(q, "count(count by"):
			v = 2
		case strings.Contains(q, "timestamp("):
			v = 1791201600
		case strings.Contains(q, `class="5xx"`), strings.Contains(q, "|__other__"):
			v = 0
		}
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"value":[0,"%g"]}]}}`, v)
	}))
	out := FetchRequestHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), "app", []string{"serving", "canary"})
	srv.Close()
	if out.Source != SourcePrometheus || len(queries) != 5 {
		t.Fatalf("query capture: %+v, %d queries", out, len(queries))
	}
	series := func(deployment, class, values string) map[string]any {
		return map[string]any{"series": fmt.Sprintf(`gateway_request_duration_by_deployment_seconds_count{app="app",deployment=%q,class=%q}`, deployment, class), "values": values}
	}
	cases := []struct {
		name  string
		input []map[string]any
		want  []float64
	}{
		{"complete; preview and previous-release errors excluded", []map[string]any{
			series("serving", "2xx", "0+19x5"), series("serving", "5xx", "0+1x5"),
			series("canary", "2xx", "0+20x5"), series("preview", "5xx", "0+100x5"), series("retired", "5xx", "0+100x5"),
		}, []float64{200, 5, 2, 300, 0}},
		{"new error counter is incomplete coverage", []map[string]any{
			series("serving", "2xx", "0+20x5"), series("serving", "5xx", "_ _ _ _ _ 50"), series("canary", "2xx", "0+20x5"),
		}, []float64{200, 0, 1, 300, 0}},
		{"first unattributed request remains visible", []map[string]any{
			series("serving", "2xx", "0+20x5"), series("canary", "2xx", "0+20x5"), series("__other__", "5xx", "_ _ _ _ _ 7"),
		}, []float64{200, 0, 2, 300, 7}},
		{"oldest serving-release sample controls freshness", []map[string]any{
			series("serving", "2xx", "0+20x5"), series("canary", "2xx", "0 0 0 _ _ _"),
		}, []float64{100, 0, 2, 120, 0}},
		{"stale marker cannot leave historical coverage confirmed", []map[string]any{
			series("serving", "2xx", "0+20x5"), series("canary", "2xx", "0 0 0 stale _ _"),
		}, []float64{100, 0, 1, 300, 0}},
	}
	var tests []map[string]any
	for _, tt := range cases {
		var expressions []map[string]any
		for n, query := range queries {
			expressions = append(expressions, map[string]any{"expr": query, "eval_time": "5m", "exp_samples": []map[string]any{{"labels": "{}", "value": tt.want[n]}}})
		}
		tests = append(tests, map[string]any{"name": tt.name, "interval": "1m", "input_series": tt.input, "promql_expr_test": expressions})
	}
	raw, err := json.Marshal(map[string]any{"evaluation_interval": "1m", "tests": tests})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "request-health.yml")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), tool, "test", "rules", path) //nolint:gosec // fixed tool, generated test file; no customer input.
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PromQL regression: %v\n%s", err, output)
	}
}
