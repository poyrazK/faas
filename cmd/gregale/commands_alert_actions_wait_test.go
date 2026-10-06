package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type alertWaitFixtureClient struct {
	rows []api.AlertRollback
	read int
}

func (c *alertWaitFixtureClient) GetAlertRollback(_ context.Context, _, _ string) (api.AlertRollback, error) {
	i := min(c.read, len(c.rows)-1)
	c.read++
	return c.rows[i], nil
}

func alertWaitFixture() api.AlertRollback {
	fire := uuid.NewString()
	return api.AlertRollback{ID: fire, AppID: uuid.NewString(), AccountID: uuid.NewString(), RuleID: uuid.NewString(), Scope: "default", CandidateDeploymentID: uuid.NewString(), PredecessorDeploymentID: uuid.NewString(), Status: "pending", Service: true, ServiceRequestID: fire, ServicePhase: "routing", AuditID: "10"}
}

func TestAlertActionWaitPinsPairAndRequiresFinishedHandoff(t *testing.T) {
	for _, scenario := range []string{"finished drain", "different predecessor", "different fire", "different app", "different request", "request cleared", "routing reported complete", "missing routing audit", "missing completion audit", "missing completion time", "unknown status", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			pin := alertWaitFixture()
			draining := pin
			draining.ServicePhase = "draining"
			complete := draining
			now := time.Now().UTC()
			complete.Status, complete.ServicePhase, complete.CompletedAt = "complete", "complete", &now
			complete.ServiceRoutingAuditID, complete.AuditID = "11", "12"
			switch scenario {
			case "different predecessor":
				complete.PredecessorDeploymentID = uuid.NewString()
			case "different fire":
				complete.ID = uuid.NewString()
			case "different app":
				complete.AppID = uuid.NewString()
			case "different request":
				complete.ServiceRequestID = uuid.NewString()
			case "request cleared":
				complete.ServiceRequestID = ""
			case "routing reported complete":
				complete.ServicePhase = "routing"
			case "missing routing audit":
				complete.ServiceRoutingAuditID = ""
			case "missing completion audit":
				complete.AuditID = ""
			case "missing completion time":
				complete.CompletedAt = nil
			case "unknown status":
				complete.Status = "other"
			case "failed":
				complete.Status, complete.Code = "failed", "alert_rollback_deployment_changed"
			}
			client := &alertWaitFixtureClient{rows: []api.AlertRollback{draining, complete}}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			last, err := waitAlertRollback(ctx, client, "demo", pin, time.Millisecond)
			if scenario == "finished drain" {
				if err != nil || last.Status != "complete" || client.read != 2 {
					t.Fatalf("wait %+v %v reads=%d", last, err, client.read)
				}
			} else if err == nil {
				t.Fatalf("unsafe receipt accepted %+v", last)
			}
		})
	}
}

func TestAlertActionWaitTimeoutPreservesBlockersAndCancellation(t *testing.T) {
	pin := alertWaitFixture()
	pin.Status, pin.ServicePhase, pin.Code = "blocked", "pending", "service_rollout_not_ready"
	pin.Blockers = []api.BindingCheckFinding{{Code: pin.Code, DeploymentID: pin.PredecessorDeploymentID, Message: "recipient capacity unavailable"}}
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		if canceled {
			cancel()
		}
		client := &alertWaitFixtureClient{rows: []api.AlertRollback{pin}}
		last, err := waitAlertRollback(ctx, client, "demo", pin, time.Millisecond)
		cancel()
		want := context.DeadlineExceeded
		if canceled {
			want = context.Canceled
		}
		if !errors.Is(err, want) || last.Code != pin.Code || len(last.Blockers) != 1 {
			t.Fatalf("lost final status %+v %v", last, err)
		}
	}
}

func TestCmdAlertActionsWaitUsesGETOnly(t *testing.T) {
	pin := alertWaitFixture()
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("wait performed %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/demo":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: pin.AppID})
		case "/v1/apps/demo/alert-rollbacks/" + pin.ID:
			reads++
			row := pin
			if reads == 2 {
				row.ServicePhase = "draining"
			}
			if reads >= 3 {
				now := time.Now().UTC()
				row.Status, row.ServicePhase, row.CompletedAt = "complete", "complete", &now
				row.ServiceRoutingAuditID, row.AuditID = "11", "12"
			}
			_ = json.NewEncoder(w).Encode(row)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	if code := cmdAlerts([]string{"actions", "--app", "demo", "--fire", pin.ID, "--wait", "--poll-interval", "1ms", "--timeout", "1s"}); code != 0 || reads != 3 {
		t.Fatalf("exit=%d reads=%d", code, reads)
	}
}
