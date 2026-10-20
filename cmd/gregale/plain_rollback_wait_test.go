package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-1446: a plain `gregale rollback APP` waits for its receipt and exits
// non-zero when the previous release was not restored; --no-wait keeps the
// request-only behaviour and never reads the receipt.
func TestCmdPlainRollbackWaitsForTheOutcome(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		outcome  string
		wantCode int
		wantGets int
	}{
		{name: "restored", args: []string{"demo", "--poll-interval", "1ms", "--timeout", "2s"}, outcome: "complete", wantCode: 0, wantGets: 2},
		{name: "not restored", args: []string{"demo", "--poll-interval", "1ms", "--timeout", "2s"}, outcome: "failed", wantCode: 1, wantGets: 2},
		{name: "no wait", args: []string{"demo", "--no-wait"}, outcome: "complete", wantCode: 0, wantGets: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_TOKEN", "test")
			pin := rollbackCLIPin()
			pin.Trigger = "manual"
			gets := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/rollback":
					w.WriteHeader(http.StatusAccepted)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: pin.TargetDeploymentID, AppID: pin.AppID, Status: "snapshotting", RollbackOperation: &pin})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/rollbacks/"+pin.ID:
					gets++
					got := pin
					if gets > 1 {
						got.Status = tc.outcome
						now := time.Now().UTC()
						got.UpdatedAt = now
						if tc.outcome == "complete" {
							ms := int64(8300)
							got.CompletedAt, got.AuditID, got.RecoveryMS = &now, "42", &ms
						} else {
							got.Code, got.Error = "rollback_target_not_activated", "the rollback target returned to superseded without serving"
						}
					}
					_ = json.NewEncoder(w).Encode(got)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", http.StatusInternalServerError)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			if code := main.CmdRollback(tc.args); code != tc.wantCode || gets != tc.wantGets {
				t.Fatalf("exit=%d GET=%d; want exit=%d GET=%d", code, gets, tc.wantCode, tc.wantGets)
			}
		})
	}
}
