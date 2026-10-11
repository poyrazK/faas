package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type sequenceRolloutStatus struct {
	rows  []api.DeploymentResponse
	calls int
}

func (s *sequenceRolloutStatus) GetDeployment(context.Context, string) (api.DeploymentResponse, error) {
	index := min(s.calls, len(s.rows)-1)
	s.calls++
	return s.rows[index], nil
}

func TestRolloutStatusWaitPinsDeploymentAndBarriers(t *testing.T) {
	row := api.DeploymentResponse{ID: pinnedBindingDeployment, AppID: "app", Status: "live", RolloutState: "rolling_out", ServiceRolloutHandoff: &api.ServiceRolloutHandoffResponse{Action: "promote", Phase: "routing"}}
	drain := row
	drain.ServiceRolloutHandoff = &api.ServiceRolloutHandoffResponse{Action: "promote", Phase: "draining"}
	done := drain
	done.RolloutState = "complete"
	done.ServiceRolloutHandoff = &api.ServiceRolloutHandoffResponse{Action: "promote", Phase: "complete"}
	client := &sequenceRolloutStatus{rows: []api.DeploymentResponse{row, drain, done}}
	got, err := pollRolloutStatus(t.Context(), client, "app", row.ID, true, time.Millisecond)
	if err != nil || got.RolloutState != "complete" || client.calls != 3 {
		t.Fatalf("wait skipped barriers: %+v calls=%d err=%v", got, client.calls, err)
	}
	wrong := row
	wrong.ID = recoveryPredecessorID
	if _, err := pollRolloutStatus(t.Context(), &sequenceRolloutStatus{rows: []api.DeploymentResponse{wrong}}, "app", row.ID, false, time.Millisecond); err == nil {
		t.Fatal("accepted different deployment")
	}
	client = &sequenceRolloutStatus{rows: []api.DeploymentResponse{row}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	last, err := pollRolloutStatus(ctx, client, "app", row.ID, true, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || last.ID != row.ID {
		t.Fatalf("deadline lost last status: %+v %v", last, err)
	}
}

func TestCmdRolloutsServiceAbortReceipt(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "wrong request"}[malformed], func(t *testing.T) {
			resetJSONOut(t)
			gate := &api.ServiceRolloutBindingGate{RequestID: "request-1", Action: "abort", DeploymentID: recoveryPredecessorID, Status: "pending"}
			out := api.RolloutTransitionResponse{AuditID: "42", Deployment: api.DeploymentResponse{ID: pinnedBindingDeployment, RolloutState: "rolling_out", ServiceRolloutHandoff: &api.ServiceRolloutHandoffResponse{Action: "abort", Phase: "pending", PredecessorDeploymentID: recoveryPredecessorID, BindingsCheck: gate}}, ServiceRecovery: &api.ServiceRolloutRecoveryReceipt{DeploymentID: pinnedBindingDeployment, PredecessorDeploymentID: recoveryPredecessorID, RequestID: gate.RequestID, Status: "accepted"}}
			if malformed {
				out.ServiceRecovery.RequestID = "different-request"
			}
			body, _ := json.Marshal(out)
			authedFakeAPI(t, string(body), http.StatusAccepted)
			code := cmdRollouts([]string{"recover", "api", "--action", "abort", "--deployment", pinnedBindingDeployment, "--expected-predecessor", recoveryPredecessorID})
			if (code == 0) == malformed {
				t.Fatalf("malformed=%v exit=%d", malformed, code)
			}
		})
	}
}

func TestCmdRolloutsStatusResolvesRevisionAndOnlyReads(t *testing.T) {
	resetJSONOut(t)
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("status issued %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/api":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app"})
		case "/v1/apps/api/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: pinnedBindingDeployment, Revision: 12}}})
		case "/v1/deployments/" + pinnedBindingDeployment:
			gets++
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: pinnedBindingDeployment, AppID: "app", RolloutState: "complete"})
		default:
			t.Errorf("unexpected read: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	code, _, _ := captureBindingCLI(t, srv.URL, "rollouts", "status", "api", "--deployment", "v12", "--wait")
	if code != 0 || gets != 1 {
		t.Fatalf("status exit=%d exact reads=%d", code, gets)
	}
}
