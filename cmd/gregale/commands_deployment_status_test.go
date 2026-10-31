package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

const conditionsDeploymentID = "00000000000000000000000000000001"

func deploymentStatus(rolledOut api.Condition) api.ResourceStatus {
	rolledOut.Type = api.ConditionRolledOut
	return api.ResourceStatus{Kind: "deployment", Name: conditionsDeploymentID, ObservedAt: "2026-10-30T15:00:00Z", Conditions: []api.Condition{
		{Type: api.ConditionReady, Status: "True", Reason: "live", Message: "The deployment is live."},
		{Type: api.ConditionProgressing, Status: "True", Reason: "rolling_out", Message: "Safe rollout at step 1 of 3."},
		rolledOut,
	}}
}

func deploymentStatusServer(t *testing.T, status api.ResourceStatus) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/deployments/"+conditionsDeploymentID+"/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_status")
}

func TestReleaseStatusPrintsConditions(t *testing.T) {
	deploymentStatusServer(t, deploymentStatus(api.Condition{Status: "False", Reason: "rolling_out", Message: "Safe rollout at step 1 of 3."}))
	stdout, _ := captureOutput(t)
	if code := main.Run([]string{"release", "status", conditionsDeploymentID}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"deployment " + conditionsDeploymentID, "RolledOut", "rolling_out"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestReleaseWaitFor(t *testing.T) {
	tests := []struct {
		name      string
		rolledOut api.Condition
		args      []string
		code      int
		wantError string
	}{
		{"holds", api.Condition{Status: "True", Reason: "rolled_out", Message: "done"}, []string{"--for", "RolledOut"}, 0, ""},
		{"aborted rollout is terminal", api.Condition{Status: "False", Reason: "rollout_aborted", Message: "stopped", Terminal: true}, []string{"--for", "RolledOut"}, 1, "rollout_aborted"},
		{"not with --rollout", api.Condition{Status: "True"}, []string{"--for", "RolledOut", "--rollout"}, 1, "--rollout is --for RolledOut"},
		{"app conditions are not release conditions", api.Condition{Status: "True"}, []string{"--for", "Degraded"}, 1, "deployment conditions are Ready, Progressing, RolledOut"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deploymentStatusServer(t, deploymentStatus(tc.rolledOut))
			_, stderr := captureOutput(t)
			args := append([]string{"release", "wait", conditionsDeploymentID}, tc.args...)
			if code := main.Run(args); code != tc.code {
				t.Errorf("exit = %d, want %d; stderr:\n%s", code, tc.code, stderr)
			}
			if tc.wantError != "" && !strings.Contains(stderr.String(), tc.wantError) {
				t.Errorf("stderr missing %q:\n%s", tc.wantError, stderr)
			}
		})
	}
}
