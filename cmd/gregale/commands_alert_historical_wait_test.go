package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestHistoricalAlertWaitPinsOperationAndCompletion(t *testing.T) {
	for _, service := range []bool{false, true} {
		for _, scenario := range []string{"complete", "different operation", "operation cleared", "kind changed", "early complete", "missing routing audit", "missing completion audit"} {
			t.Run(scenario+map[bool]string{false: "/function", true: "/service"}[service], func(t *testing.T) {
				pin := alertWaitFixture()
				pin.Service = service
				pin.ServiceRequestID = ""
				pin.ServicePhase = ""
				pin.Historical = true
				pin.RollbackOperationID = pin.ID
				pin.RollbackPhase = "preparing"
				pending := pin
				pending.RollbackPhase = "routing"
				complete := pending
				now := time.Now().UTC()
				complete.Status = "complete"
				complete.RollbackPhase = "complete"
				complete.RollbackRoutingAuditID = "22"
				complete.AuditID = "23"
				complete.CompletedAt = &now
				switch scenario {
				case "different operation":
					complete.RollbackOperationID = uuid.NewString()
				case "operation cleared":
					complete.RollbackOperationID = ""
				case "kind changed":
					complete.Historical = false
				case "early complete":
					complete.RollbackPhase = "routing"
				case "missing routing audit":
					complete.RollbackRoutingAuditID = ""
				case "missing completion audit":
					complete.AuditID = ""
				}
				client := &alertWaitFixtureClient{rows: []api.AlertRollback{pending, complete}}
				got, err := waitAlertRollback(t.Context(), client, "app", pin, time.Millisecond)
				if scenario == "complete" {
					if err != nil || got.Status != "complete" || client.read != 2 {
						t.Fatalf("wait %+v %v", got, err)
					}
				} else if err == nil {
					t.Fatal("invalid completion accepted")
				}
			})
		}
	}
}
func TestAlertPostDeployWindowDuration(t *testing.T) {
	for _, d := range []time.Duration{-time.Second, time.Millisecond, time.Hour + time.Second} {
		if _, err := alertRollbackWindowSeconds(d); err == nil {
			t.Errorf("invalid %s", d)
		}
	}
	for _, d := range []time.Duration{0, time.Second, 10 * time.Minute, time.Hour} {
		if seconds, err := alertRollbackWindowSeconds(d); err != nil || seconds != int(d/time.Second) {
			t.Errorf("duration %s %d %v", d, seconds, err)
		}
	}
}

func TestAlertWindowCLIRequestPreservesOmittedAndExplicitZero(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPatch {
			t.Errorf("method %s", r.Method)
		}
		var req api.UpdateAlertRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		switch requests {
		case 1:
			if req.PostDeployRollbackWindowSeconds == nil || *req.PostDeployRollbackWindowSeconds != 600 {
				t.Errorf("window %+v", req)
			}
		case 2:
			if req.PostDeployRollbackWindowSeconds != nil {
				t.Error("rename changed window")
			}
		case 3:
			if req.PostDeployRollbackWindowSeconds == nil || *req.PostDeployRollbackWindowSeconds != 0 {
				t.Error("disable not explicit")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AlertRuleResponse{ID: id})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	for _, flags := range [][]string{{"--post-deploy-rollback-window", "10m"}, {"--name", "renamed"}, {"--post-deploy-rollback-window", "0"}} {
		args := append([]string{"--app", "demo"}, flags...)
		args = append(args, id)
		if code := cmdAlertUpdate(args); code != 0 {
			t.Fatalf("exit %d", code)
		}
	}
	if requests != 3 {
		t.Fatalf("requests %d", requests)
	}
}
