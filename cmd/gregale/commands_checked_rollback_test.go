package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type rollbackPollClient struct {
	rows  []api.RollbackOperation
	calls int
}

func (f *rollbackPollClient) GetRollbackOperation(_ context.Context, _ string, _ string) (api.RollbackOperation, error) {
	i := f.calls
	f.calls++
	if i >= len(f.rows) {
		i = len(f.rows) - 1
	}
	return f.rows[i], nil
}
func rollbackCLIPin() api.RollbackOperation {
	return api.RollbackOperation{ID: uuid.NewString(), AppID: uuid.NewString(), Scope: "default", TargetDeploymentID: uuid.NewString(), CurrentDeploymentID: uuid.NewString(), Status: "preparing", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}
func TestPollCheckedRollbackPinsReceiptAndCompletion(t *testing.T) {
	pin := rollbackCLIPin()
	now := time.Now()
	complete := pin
	complete.Status = "complete"
	complete.CompletedAt = &now
	complete.AuditID = "123"
	for _, scenario := range []string{"complete", "wrong target", "wrong current", "wrong operation", "wrong app", "missing audit", "failed", "unknown state"} {
		t.Run(scenario, func(t *testing.T) {
			got := complete
			switch scenario {
			case "wrong target":
				got.TargetDeploymentID = uuid.NewString()
			case "wrong current":
				got.CurrentDeploymentID = uuid.NewString()
			case "wrong operation":
				got.ID = uuid.NewString()
			case "wrong app":
				got.AppID = uuid.NewString()
			case "missing audit":
				got.AuditID = ""
			case "failed":
				got.Status = "failed"
				got.Code = "rollback_deployment_changed"
			case "unknown state":
				got.Status = "done"
			}
			client := &rollbackPollClient{rows: []api.RollbackOperation{pin, got}}
			last, err := pollRollbackOperation(t.Context(), client, "app", pin, true, time.Millisecond)
			if scenario == "complete" {
				if err != nil || last.Status != "complete" {
					t.Fatalf("wait: %+v %v", last, err)
				}
			} else if err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
}
func TestPollCheckedRollbackTimeoutPreservesBlockers(t *testing.T) {
	pin := rollbackCLIPin()
	blocked := pin
	blocked.Status = "blocked"
	blocked.Code = "bindings_check_failed"
	blocked.Blockers = []api.BindingCheckFinding{{Code: "verification_expired", Message: "Verify target"}}
	client := &rollbackPollClient{rows: []api.RollbackOperation{blocked}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	last, err := pollRollbackOperation(ctx, client, "app", pin, true, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || len(last.Blockers) != 1 {
		t.Fatalf("lost last blocker: %+v %v", last, err)
	}
}
func TestCmdCheckedRollbackPostsOnceAndWaitsWithGET(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	pin := rollbackCLIPin()
	posts, gets := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: pin.AppID, Slug: "demo"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/rollback":
			posts++
			var req api.RollbackRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TargetDeploymentID == nil || *req.TargetDeploymentID != pin.TargetDeploymentID || req.ExpectedCurrentDeploymentID == nil || *req.ExpectedCurrentDeploymentID != pin.CurrentDeploymentID {
				t.Errorf("wrong exact request: %+v %v", req, err)
			}
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: pin.TargetDeploymentID, AppID: pin.AppID, Status: "snapshotting", Scope: "default", RollbackOperation: &pin})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/rollbacks/"+pin.ID:
			gets++
			got := pin
			got.Status = "routing"
			if gets > 1 {
				now := time.Now()
				got.Status = "complete"
				got.CompletedAt = &now
				got.AuditID = "123"
			}
			_ = json.NewEncoder(w).Encode(got)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	code := cmdRollback([]string{"demo", "--to", pin.TargetDeploymentID, "--expected-current", pin.CurrentDeploymentID, "--wait", "--poll-interval", "1ms", "--timeout", "1s"})
	if code != 0 || posts != 1 || gets != 2 {
		t.Fatalf("exit=%d POST=%d GET=%d", code, posts, gets)
	}
}
func TestCmdCheckedRollbackInvalidFlagsMakeNoWrites(t *testing.T) {
	for _, args := range [][]string{{"demo", "--to", "v1", "--expected-current="}, {"demo", "--wait"}, {"demo", "--expected-current", "v2"}, {"demo", "--to", "v1", "--expected-current", "v2", "--timeout", "0s"}, {"demo", "--to", "v1", "--reason", "test"}} {
		if code := cmdRollback(args); code != 1 {
			t.Fatalf("args %s accepted: %d", strings.Join(args, " "), code)
		}
	}
}
