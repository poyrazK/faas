package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261022153155: `gregale ai deployments` lists an app's AI calls by
// deployment, and `gregale release-gates add --ai` adds an ai gate.
func TestAIDeploymentsAndAIGateCommands(t *testing.T) {
	main.ResetJSONOut(t)
	ttfb := int64(380)
	var created api.CreateReleaseGateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/my-api/ai-deployments":
			main.WriteJSONTest(w, api.AIDeploymentUsageList{Items: []api.AIDeploymentUsage{{
				DeploymentID: "6f1c0e1e-6c1b-4c7a-9c8e-2f0b7c1d9a10", LastCallAt: "2026-10-22T15:41:00Z", Calls: 4210,
				ErrorRate: 0.004, FlaggedRate: 0.011, CostMillicents: 421_000, CostPerCallMillicents: 100, MeanTTFBMS: &ttfb}}})
		case "POST /v1/apps/my-api/release-gates":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusCreated)
			main.WriteJSONTest(w, api.ReleaseGate{Name: created.Name, Kind: created.Kind, Metric: created.Metric,
				MaxIncreasePercent: created.MaxIncreasePercent, MinSamples: 20})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	if code := main.CmdAI([]string{"deployments", "--app", "my-api"}); code != 0 {
		t.Fatalf("ai deployments exit = %d", code)
	}
	for _, want := range []string{"6f1c0e1e-6c1b-4c7a-9c8e-2f0b7c1d9a10", "4210", "0.40%", "1.10%", "USD 4.21000", "USD 0.00100", "380"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("ai deployments output lacks %q:\n%s", want, stdout.String())
		}
	}
	stdout.Reset()
	if code := main.CmdReleaseGates([]string{"add", "--app", "my-api", "--name", "ai-cost", "--ai", "cost_per_call", "--max-increase", "30"}); code != 0 {
		t.Fatalf("release-gates add exit = %d", code)
	}
	if created.Kind != "ai" || created.Metric != "cost_per_call" || created.MaxIncreasePercent != 30 || created.Threshold != nil {
		t.Fatalf("request = %+v", created)
	}
	if !strings.Contains(stdout.String(), "AI cost_per_call rises more than 30% over the previous release") {
		t.Fatalf("output = %s", stdout.String())
	}
	for _, args := range [][]string{
		{"add", "--app", "my-api", "--name", "x", "--ai", "cost_per_call", "--metric", "orders", "--above", "1"},
		{"add", "--app", "my-api", "--name", "x", "--new-errors", "--max-increase", "30"},
	} {
		if code := main.CmdReleaseGates(args); code == 0 {
			t.Fatalf("%v was accepted", args)
		}
	}
}
