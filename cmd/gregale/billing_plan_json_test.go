package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale --json billing plan` printed human lines on success, so a script
// (and the payment journey) could not tell a scheduled downgrade from an
// undone cancellation. JSON mode now prints the account apid returned.
func TestBillingPlanJSONPrintsTheAccount(t *testing.T) {
	at := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		current string
		target  string
		resp    api.AccountResponse
	}{
		{"scheduled_downgrade", "pro", "hobby",
			api.AccountResponse{Plan: "pro", PlanChangeStatus: "pending_provider_confirmation", RequestedPlan: "hobby", EffectiveAt: &at}},
		{"kept_subscription", "pro", "pro",
			api.AccountResponse{Plan: "pro", PlanChangeStatus: "scheduled_change_undone"}},
		{"changed", "free", "free", api.AccountResponse{Plan: "free"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
					_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: tc.current})
				case r.Method == http.MethodPatch && r.URL.Path == "/v1/account/plan":
					_ = json.NewEncoder(w).Encode(tc.resp)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			t.Setenv("HOME", t.TempDir())
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			stdout, readStderr, restore := main.SwapIO(t)
			code := main.Run([]string{"--json", "billing", "plan", tc.target, "--yes"})
			restore()
			if code != 0 {
				t.Fatalf("exit = %d; stderr %q", code, readStderr())
			}
			var got api.AccountResponse
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not the account JSON: %v\n%s", err, stdout.String())
			}
			if got.Plan != tc.resp.Plan || got.PlanChangeStatus != tc.resp.PlanChangeStatus ||
				got.RequestedPlan != tc.resp.RequestedPlan || (tc.resp.EffectiveAt != nil && (got.EffectiveAt == nil || !got.EffectiveAt.Equal(at))) {
				t.Fatalf("printed %+v, want %+v", got, tc.resp)
			}
		})
	}
}
