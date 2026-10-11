package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdAlertActionsUsesGETOnly(t *testing.T) {
	fire := uuid.NewString()
	receipt := api.AlertRollback{ID: fire, RuleID: uuid.NewString(), AppID: uuid.NewString(), Status: "blocked", CandidateDeploymentID: uuid.NewString(), PredecessorDeploymentID: uuid.NewString(), Code: "binding_verification_missing", Blockers: []api.BindingCheckFinding{{Code: "binding_verification_missing", Message: "verify predecessor"}}}
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet {
			t.Errorf("status mutated: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/demo/alert-rollbacks":
			_ = json.NewEncoder(w).Encode([]api.AlertRollback{receipt})
		case "/v1/apps/demo/alert-rollbacks/" + fire:
			_ = json.NewEncoder(w).Encode(receipt)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, args := range [][]string{{"actions", "--app", "demo"}, {"actions", "--app", "demo", "--fire", fire}} {
		if code := cmdAlerts(args); code != 0 {
			t.Fatalf("exit %d", code)
		}
	}
	if reads != 2 {
		t.Fatalf("reads %d", reads)
	}
}
func TestCmdAlertActionsInvalidFlagsDoNotRead(t *testing.T) {
	for _, args := range [][]string{{}, {"--app", "demo", "--fire", "latest"}, {"--app", "demo", "--fire", uuid.NewString(), "extra"}, {"--app", "demo", "--wait"}, {"--app", "demo", "--fire", uuid.NewString(), "--wait", "--timeout", "0s"}, {"--app", "demo", "--poll-interval", "-1s"}} {
		if code := cmdAlertActions(args); code != 1 {
			t.Fatalf("accepted %s: %d", strings.Join(args, " "), code)
		}
	}
}
