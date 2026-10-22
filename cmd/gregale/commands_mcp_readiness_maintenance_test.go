package main_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessMaintenanceCLI(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"00000000-0000-0000-0000-000000000001"}`, http.StatusCreated)
	if code := main.CmdMCPReadiness([]string{"maintenance", "create", "--app", "demo", "--scope", "staging", "--duration", "30m", "--reason", "upgrade"}); code != 0 || (*f.SawPath()) != "/v1/apps/demo/mcp-readiness-maintenance" {
		t.Fatal("create", code, (*f.SawPath()))
	}
	var req api.CreateMCPReadinessMaintenanceRequest
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil || req.Scope != "staging" || req.EndsAt.Sub(req.StartsAt).Minutes() != 30 {
		t.Fatal(req, err)
	}
	for _, args := range [][]string{{"create", "--duration", "0s", "--reason", "upgrade"}, {"create", "--duration", "721h", "--reason", "upgrade"}, {"create", "--duration", "1h", "--reason", ""}, {"list", "--before", "bad"}, {"cancel", "--id", "bad", "--reason", "complete"}, {"list", "--duration", "1h"}} {
		(*f.SawMethod()) = ""
		args = append(args, "--app", "demo")
		if code := main.CmdMCPReadinessMaintenance(args); code == 0 || (*f.SawMethod()) != "" {
			t.Fatal("invalid input reached API", args, code)
		}
	}
}
