package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAutomationRecoverySuppress(t *testing.T) {
	review := "11111111-1111-4111-8111-111111111111"
	action := "22222222-2222-4222-8222-222222222222"
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/apps/consumer/recovery-policy/reviews/"+review+"/actions/"+action+"/suppress" {
			t.Fatal(r.Method, r.URL.Path)
		}
		var req api.SuppressWorkflowRecoveryAutomaticActionRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedPolicyVersion != 4 || req.Reason != "handled externally" {
			t.Fatalf("request %+v", req)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryAutomaticAction{ActionID: action, Status: "suppressed", Reason: "operator_suppressed", Suppression: &api.WorkflowRecoveryAutomaticSuppression{Reason: req.Reason, ActorKind: "api_key", ActorID: "operator", SuppressedAt: time.Now().UTC()}})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomationsRecoverySuppress([]string{"--app", "consumer", "--review", review, "--action", action, "--policy-version", "4", "--reason", "handled externally"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"operator_suppressed", "handled externally", "Suppressed by api_key operator"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s: %s", want, out.String())
		}
	}
}
