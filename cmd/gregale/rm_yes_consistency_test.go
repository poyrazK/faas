package main_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// alerts, synthetics, release-gates and slos rm rejected --yes, which
// webhooks, crons, edge-rules and apps rm accept (Hunt 12 H12-37).
func TestRmCommandsAcceptYes(t *testing.T) {
	const alertID = "f98701d3-e498-4d73-8f39-6028c02d3635"
	tests := []struct {
		name string
		args []string
		path string
	}{
		{"alerts rm", []string{"alerts", "rm", "--app", "web", alertID, "--yes"}, "/v1/apps/web/alerts/" + alertID},
		{"synthetics rm", []string{"synthetics", "rm", "--app", "web", "chk-1", "--yes"}, "/v1/apps/web/synthetics/chk-1"},
		{"slos rm", []string{"slos", "rm", "--yes", "--app", "web", "slo-1"}, "/v1/apps/web/slos/slo-1"},
		{"release-gates rm", []string{"release-gates", "rm", "no-new-errors", "--app", "web", "--yes"}, "/v1/apps/web/release-gates/no-new-errors"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			deleted := ""
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deleted = r.URL.Path
					if r.URL.Path == tt.path && tt.name == "release-gates rm" {
						_, _ = w.Write([]byte(`{}`))
						return
					}
					w.WriteHeader(http.StatusNoContent)
					return
				}
				http.NotFound(w, r)
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			_, restore := main.CaptureStdout(t)
			code := main.Run(tt.args)
			restore()
			if code != 0 || deleted != tt.path {
				t.Fatalf("%v: exit=%d deleted=%q, want 0 and %q", tt.args, code, deleted, tt.path)
			}
		})
	}
}
