package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessMaintenanceCLI(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"00000000-0000-0000-0000-000000000001"}`, http.StatusCreated)
	if code := cmdMCPReadiness([]string{"maintenance", "create", "--app", "demo", "--scope", "staging", "--duration", "30m", "--reason", "upgrade"}); code != 0 || f.sawPath != "/v1/apps/demo/mcp-readiness-maintenance" {
		t.Fatal("create", code, f.sawPath)
	}
	var req api.CreateMCPReadinessMaintenanceRequest
	if err := json.Unmarshal(f.sawBody, &req); err != nil || req.Scope != "staging" || req.EndsAt.Sub(req.StartsAt).Minutes() != 30 {
		t.Fatal(req, err)
	}
	for _, args := range [][]string{{"create", "--duration", "0s", "--reason", "upgrade"}, {"create", "--duration", "721h", "--reason", "upgrade"}, {"create", "--duration", "1h", "--reason", ""}, {"list", "--before", "bad"}, {"cancel", "--id", "bad", "--reason", "complete"}, {"list", "--duration", "1h"}} {
		f.sawMethod = ""
		args = append(args, "--app", "demo")
		if code := cmdMCPReadinessMaintenance(args); code == 0 || f.sawMethod != "" {
			t.Fatal("invalid input reached API", args, code)
		}
	}
}
