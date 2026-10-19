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

const recoveryPredecessorID = "5b87c415-7c93-4932-acab-a3c90e98be86"

func TestCmdRolloutsExactRecoveryRevisionSelection(t *testing.T) {
	posts, reads := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/apps/api/deployments":
			reads++
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: main.PinnedBindingDeployment, Revision: 12}, {ID: recoveryPredecessorID, Revision: 11}}})
		case "POST /v1/apps/api/rollouts/recover":
			posts++
			var req api.RecoverRolloutRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Action != "abort" || req.DeploymentID != main.PinnedBindingDeployment || req.ExpectedPredecessorDeploymentID != recoveryPredecessorID || req.Reason != "bad release" {
				t.Errorf("unpinned request: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(api.RolloutTransitionResponse{Deployment: api.DeploymentResponse{ID: main.PinnedBindingDeployment, RolloutState: "aborted"}, AuditID: "42", Recovery: &api.RolloutRecoveryReceipt{DeploymentID: main.PinnedBindingDeployment, PredecessorDeploymentID: recoveryPredecessorID, RestoredTrafficPercent: 100}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	code, out, errOut := main.CaptureBindingCLI(t, srv.URL, "rollouts", "recover", "api", "--action", "abort", "--deployment", "v12", "--expected-predecessor", "v11", "--reason", "bad release", "--json")
	if code != 0 || posts != 1 || reads != 2 || !strings.Contains(out, recoveryPredecessorID) {
		t.Fatalf("code=%d reads=%d posts=%d output=%s %s", code, reads, posts, out, errOut)
	}
}

func TestCmdRolloutsExactRecoveryRejectsBadReceipts(t *testing.T) {
	for _, scenario := range []string{"missing receipt", "wrong predecessor", "wrong candidate", "wrong state", "missing audit", "wrong weight", "failed check", "wrong check deployment"} {
		t.Run(scenario, func(t *testing.T) {
			main.ResetJSONOut(t)
			resp := api.RolloutTransitionResponse{Deployment: api.DeploymentResponse{ID: main.PinnedBindingDeployment, RolloutState: "aborted"}, AuditID: "42", Recovery: &api.RolloutRecoveryReceipt{DeploymentID: main.PinnedBindingDeployment, PredecessorDeploymentID: recoveryPredecessorID, RestoredTrafficPercent: 100}}
			switch scenario {
			case "missing receipt":
				resp.Recovery = nil
			case "wrong predecessor":
				resp.Recovery.PredecessorDeploymentID = main.PinnedBindingDeployment
			case "wrong candidate":
				resp.Deployment.ID = recoveryPredecessorID
			case "wrong state":
				resp.Deployment.RolloutState = "rolling_out"
			case "missing audit":
				resp.AuditID = ""
			case "wrong weight":
				resp.Recovery.RestoredTrafficPercent = 75
			case "failed check":
				resp.Recovery.BindingsChecks = []api.BindingCheckReport{{DeploymentID: recoveryPredecessorID}}
			case "wrong check deployment":
				resp.Recovery.BindingsChecks = []api.BindingCheckReport{{Passed: true, DeploymentID: main.PinnedBindingDeployment}}
			}
			body, _ := json.Marshal(resp)
			main.AuthedFakeAPI(t, string(body), http.StatusOK)
			if code := main.CmdRolloutsRecover([]string{"api", "--action", "abort", "--deployment", main.PinnedBindingDeployment, "--expected-predecessor", recoveryPredecessorID}); code != 1 {
				t.Fatalf("accepted %s: exit=%d", scenario, code)
			}
		})
	}
}

func TestCmdRolloutsExactRecoveryValidatesBeforeMutation(t *testing.T) {
	for _, flags := range [][]string{
		{"--deployment", main.PinnedBindingDeployment},
		{"--expected-predecessor", recoveryPredecessorID},
		{"--deployment", main.PinnedBindingDeployment, "--expected-predecessor", main.PinnedBindingDeployment},
		{"--deployment", "bad-uuid", "--expected-predecessor", recoveryPredecessorID},
	} {
		main.ResetJSONOut(t)
		f := main.AuthedFakeAPI(t, "", http.StatusOK)
		args := append([]string{"api", "--action", "abort"}, flags...)
		if code := main.CmdRolloutsRecover(args); code != 1 || (*f.SawMethod()) != "" {
			t.Fatalf("invalid selector mutated: code=%d method=%s flags=%v", code, (*f.SawMethod()), flags)
		}
	}
}
