package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `billing costs --json` and `gregale --json billing costs` printed the
// table: the global parser consumes --json before the command's own flag
// sees it (Hunt 12 H12-42).
func TestBillingFinancialHonoursGlobalJSON(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"costs, flag after", []string{"billing", "costs", "--json"}},
		{"costs, flag before", []string{"--json", "billing", "costs"}},
		{"forecast", []string{"billing", "forecast", "--json"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/billing/costs", "/v1/billing/forecast":
					_, _ = w.Write([]byte(`{"period_start":"2026-10-01T00:00:00Z","period_end":"2026-11-01T00:00:00Z","as_of":"2026-10-22T15:41:00Z"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.Run(tt.args)
			restore()
			var v map[string]any
			if code != 0 || json.Unmarshal(out.Bytes(), &v) != nil {
				t.Fatalf("exit %d, output is not JSON:\n%s", code, out.String())
			}
		})
	}
}
