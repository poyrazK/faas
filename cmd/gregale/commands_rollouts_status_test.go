package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type sequenceRolloutStatus struct {
	rows  []api.DeploymentResponse
	errs  []error
	calls int
}

func (s *sequenceRolloutStatus) GetDeployment(context.Context, string) (api.DeploymentResponse, error) {
	index := min(s.calls, len(s.rows)-1)
	s.calls++
	if index < len(s.errs) && s.errs[index] != nil {
		return api.DeploymentResponse{}, s.errs[index]
	}
	return s.rows[index], nil
}

// TestRolloutStatusWaitOutlivesTransientPollErrors pins the production-us
// finding: one timed-out read ended `rollouts status --wait` with a stale
// "rolling_out (50%)" while the rollout had already reached 100%.
func TestRolloutStatusWaitOutlivesTransientPollErrors(t *testing.T) {
	ten := api.DeploymentResponse{ID: main.PinnedBindingDeployment, AppID: "app", Status: "live", RolloutState: "rolling_out", TrafficPercent: 10}
	fifty := ten
	fifty.TrafficPercent = 50
	done := ten
	done.RolloutState, done.TrafficPercent = "complete", 100
	unavailable := &api.APIError{Problem: api.Problem{Status: http.StatusServiceUnavailable}}
	client := &sequenceRolloutStatus{
		rows: []api.DeploymentResponse{ten, ten, {}, fifty, {}, done},
		errs: []error{nil, nil, errors.New("request timed out"), nil, unavailable, nil},
	}
	var seen []int
	got, err := main.PollRolloutStatus(t.Context(), client, "app", ten.ID, true, time.Millisecond, func(d api.DeploymentResponse) {
		seen = append(seen, d.TrafficPercent)
	})
	if err != nil || got.RolloutState != "complete" || client.calls != 6 {
		t.Fatalf("wait = %+v calls=%d err=%v, want complete after 6 reads", got, client.calls, err)
	}
	if len(seen) != 3 || seen[0] != 10 || seen[1] != 50 || seen[2] != 100 {
		t.Fatalf("progress = %v, want one line per change: [10 50 100]", seen)
	}

	notFound := &api.APIError{Problem: api.Problem{Status: http.StatusNotFound}}
	client = &sequenceRolloutStatus{rows: []api.DeploymentResponse{{}, ten}, errs: []error{notFound}}
	if _, err := main.PollRolloutStatus(t.Context(), client, "app", ten.ID, true, time.Millisecond, nil); !errors.Is(err, notFound) || client.calls != 1 {
		t.Fatalf("404 err=%v calls=%d, want the 404 after one read", err, client.calls)
	}
	client = &sequenceRolloutStatus{rows: []api.DeploymentResponse{{}, ten}, errs: []error{unavailable}}
	if _, err := main.PollRolloutStatus(t.Context(), client, "app", ten.ID, false, time.Millisecond, nil); !errors.Is(err, unavailable) || client.calls != 1 {
		t.Fatalf("status without --wait err=%v calls=%d, want the error after one read", err, client.calls)
	}
}

func TestRolloutStatusWaitPinsDeploymentAndBarriers(t *testing.T) {
	row := api.DeploymentResponse{ID: main.PinnedBindingDeployment, AppID: "app", Status: "live", RolloutState: "rolling_out", ServiceRolloutHandoff: &api.ServiceRolloutHandoffResponse{Action: "promote", Phase: "routing"}}
	drain := row
	drain.ServiceRolloutHandoff = &api.ServiceRolloutHandoffResponse{Action: "promote", Phase: "draining"}
	done := drain
	done.RolloutState = "complete"
	done.ServiceRolloutHandoff = &api.ServiceRolloutHandoffResponse{Action: "promote", Phase: "complete"}
	client := &sequenceRolloutStatus{rows: []api.DeploymentResponse{row, drain, done}}
	got, err := main.PollRolloutStatus(t.Context(), client, "app", row.ID, true, time.Millisecond, nil)
	if err != nil || got.RolloutState != "complete" || client.calls != 3 {
		t.Fatalf("wait skipped barriers: %+v calls=%d err=%v", got, client.calls, err)
	}
	wrong := row
	wrong.ID = recoveryPredecessorID
	if _, err := main.PollRolloutStatus(t.Context(), &sequenceRolloutStatus{rows: []api.DeploymentResponse{wrong}}, "app", row.ID, false, time.Millisecond, nil); err == nil {
		t.Fatal("accepted different deployment")
	}
	client = &sequenceRolloutStatus{rows: []api.DeploymentResponse{row}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	last, err := main.PollRolloutStatus(ctx, client, "app", row.ID, true, time.Millisecond, nil)
	if !errors.Is(err, context.DeadlineExceeded) || last.ID != row.ID {
		t.Fatalf("deadline lost last status: %+v %v", last, err)
	}
}

func TestCmdRolloutsServiceAbortReceipt(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "wrong request"}[malformed], func(t *testing.T) {
			main.ResetJSONOut(t)
			gate := &api.ServiceRolloutBindingGate{RequestID: "request-1", Action: "abort", DeploymentID: recoveryPredecessorID, Status: "pending"}
			out := api.RolloutTransitionResponse{AuditID: "42", Deployment: api.DeploymentResponse{ID: main.PinnedBindingDeployment, RolloutState: "rolling_out", ServiceRolloutHandoff: &api.ServiceRolloutHandoffResponse{Action: "abort", Phase: "pending", PredecessorDeploymentID: recoveryPredecessorID, BindingsCheck: gate}}, ServiceRecovery: &api.ServiceRolloutRecoveryReceipt{DeploymentID: main.PinnedBindingDeployment, PredecessorDeploymentID: recoveryPredecessorID, RequestID: gate.RequestID, Status: "accepted"}}
			if malformed {
				out.ServiceRecovery.RequestID = "different-request"
			}
			body, _ := json.Marshal(out)
			main.AuthedFakeAPI(t, string(body), http.StatusAccepted)
			code := main.CmdRollouts([]string{"recover", "api", "--action", "abort", "--deployment", main.PinnedBindingDeployment, "--expected-predecessor", recoveryPredecessorID})
			if (code == 0) == malformed {
				t.Fatalf("malformed=%v exit=%d", malformed, code)
			}
		})
	}
}

func TestCmdRolloutsStatusResolvesRevisionAndOnlyReads(t *testing.T) {
	main.ResetJSONOut(t)
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
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: main.PinnedBindingDeployment, Revision: 12}}})
		case "/v1/deployments/" + main.PinnedBindingDeployment:
			gets++
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: main.PinnedBindingDeployment, AppID: "app", RolloutState: "complete"})
		default:
			t.Errorf("unexpected read: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	code, _, _ := main.CaptureBindingCLI(t, srv.URL, "rollouts", "status", "api", "--deployment", "v12", "--wait")
	if code != 0 || gets != 1 {
		t.Fatalf("status exit=%d exact reads=%d", code, gets)
	}
}
