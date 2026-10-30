package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// #2683: an edge-rule change the API saved before every serving gateway
// applied it answers 503 with X-Faas-Edge-Rules-State: converging. The CLI
// printed the generic "check service status before retrying" hint, so a user
// resent a change that had already happened. It now says the change is saved
// and should not be resent.
func TestEdgeRuleConvergingChangeIsNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     string
		wantSaved bool
	}{
		{name: "saved, still converging", state: api.EdgeRulesStateConverging, wantSaved: true},
		{name: "refused before the write", state: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/v1/edge-rules/rule-1" {
					http.NotFound(w, r)
					return
				}
				if tc.state != "" {
					w.Header().Set(api.EdgeRulesStateHeader, tc.state)
				}
				api.WriteProblem(w, api.ErrCapacity("edge rule was deleted but the serving fleet has not acknowledged it; verify the rule is absent before retrying this mutation"))
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			errOut, restore := main.CaptureStderr(t)
			code := main.Run([]string{"edge", "rule", "delete", "rule-1", "--yes"})
			restore()
			got := errOut.String()
			if code == 0 {
				t.Fatalf("exit 0 for a 503:\n%s", got)
			}
			saved := strings.Contains(got, "The change is saved") && strings.Contains(got, "Don't resend it")
			if saved != tc.wantSaved {
				t.Fatalf("saved-change hint shown = %v, want %v:\n%s", saved, tc.wantSaved, got)
			}
		})
	}
}
