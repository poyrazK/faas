package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationEventRecoveryJobSubmission(t *testing.T) {
	for _, mode := range []string{"success", "lost_reply", "scope_changed", "conflict"} {
		t.Run(mode, func(t *testing.T) {
			generation := int64(0)
			p := main.AutomationEventPlan{Version: 1, App: "consumer", AppID: inspectorAppID, Name: "paid", Revision: 3, Source: "orders/?+", Key: "incident-1", CreatedAt: time.Now().UTC(), Events: []main.AutomationEventPlanItem{{ID: "evt&1", Subscription: "selected", Generation: &generation, Action: recoveryRecipient("routing_replay").RecoveryActions[0]}}}
			expected := main.AutomationEventJobRequest(p, 5, "incident")
			posts := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				write := func(v any) {
					if err := json.NewEncoder(w).Encode(v); err != nil {
						t.Error(err)
					}
				}
				switch r.URL.Path {
				case "/v1/apps/consumer":
					id := inspectorAppID
					if mode == "scope_changed" {
						id = recoverySecondRunID
					}
					write(api.AppResponse{ID: id})
				case "/v1/apps/consumer/automations/paid":
					t.Error("submission retries must not require a current automation definition")
					w.WriteHeader(404)
				case "/v1/apps/consumer/event-recoveries":
					if r.Method != http.MethodPost {
						t.Fatal("wrong method")
					}
					var req api.EventRecoveryRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(req, expected) {
						t.Errorf("request=%+v expected=%+v", req, expected)
					}
					posts++
					if mode == "lost_reply" && posts == 1 {
						w.WriteHeader(503)
						write(api.NewProblem(503, "unavailable", "Unavailable", "retry"))
						return
					}
					if mode == "conflict" {
						w.WriteHeader(409)
						write(api.NewProblem(409, api.CodeConflict, "Conflict", "changed"))
						return
					}
					write(api.EventRecoveryJob{ID: recoveryInvocationID, AppID: p.AppID, Selection: req, State: "running", SelectedCount: 1, PendingCount: 1})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			path := filepath.Join(t.TempDir(), "plan.json")
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"event-recovery-start", "--plan", path, "--rate", "5", "--reason", "incident"}
			main.CaptureAutomationStdout(t)
			code := main.CmdAutomations(args)
			wantCode := map[string]int{"success": 0, "lost_reply": 3, "scope_changed": 1, "conflict": 5}[mode]
			if code != wantCode {
				t.Fatalf("exit=%d", code)
			}
			if mode == "scope_changed" && posts != 0 {
				t.Fatal("scope mismatch submitted")
			}
			if mode == "lost_reply" {
				if code := main.CmdAutomations(args); code != 0 || posts != 2 {
					t.Fatalf("retry=%d posts=%d", code, posts)
				}
			}
		})
	}
}
func TestAutomationEventJobKeyPinsSelection(t *testing.T) {
	p := main.AutomationEventPlan{AppID: inspectorAppID, Name: "paid", Key: "incident"}
	first := main.AutomationEventJobRequest(p, 1, "")
	p.Revision = 2
	next := main.AutomationEventJobRequest(p, 2, "retry")
	if first.RequestID != next.RequestID {
		t.Fatal("changed plan must conflict under original job key")
	}
	p.Key = "next-incident"
	if main.AutomationEventJobRequest(p, 1, "").RequestID == first.RequestID {
		t.Fatal("new key reused prior job")
	}
}
