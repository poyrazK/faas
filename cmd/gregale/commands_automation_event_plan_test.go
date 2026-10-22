package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestAutomationEventPlanReviewExecuteAndRetry(t *testing.T) {
	for _, mode := range []string{"success", "server_conflict", "legacy", "lost_reply", "uncommitted", "revision_changed", "expired", "deadline_expired", "generation_changed", "unsafe_action", "read_error"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			generation := int64(0)
			revision := int64(3)
			posts := 0
			executing := false
			var keys []string
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing auth")
				}
				write := func(v any) {
					if err := json.NewEncoder(w).Encode(v); err != nil {
						t.Error(err)
					}
				}
				switch r.URL.Path {
				case "/v1/apps/consumer":
					write(api.AppResponse{ID: inspectorAppID})
				case "/v1/apps/consumer/automations/paid":
					version := revision
					if executing && mode == "revision_changed" {
						version++
					}
					write(api.AutomationResponse{Name: "paid", PublishedVersion: version})
				case "/v1/events/receipt":
					if executing && mode == "read_error" {
						w.WriteHeader(503)
						write(api.NewProblem(503, "unavailable", "Unavailable", "PRIVATE-ERROR"))
						return
					}
					eventID := r.URL.Query().Get("id")
					if eventID == "" {
						eventID = r.URL.Query().Get("event_id")
					}
					if eventID != "evt&1" {
						t.Errorf("event identity query %v", r.URL.Query())
					}
					recipient := recoveryRecipient("routing_replay")
					recipient.AppID = inspectorAppID
					recipient.WorkflowName = "paid"
					recipient.Origin = "acceptance"
					recipient.Routing.Generation = &generation
					recipient.Routing.ReplayCount = generation
					if mode == "legacy" {
						recipient.Routing.Generation = nil
					}
					recipient.Routing.LastError = "PRIVATE-ROUTING"
					if executing && mode == "deadline_expired" {
						deadline := time.Now().Add(-time.Minute)
						recipient.Routing.DeliveryDeadlineAt = &deadline
					}
					if executing && mode == "generation_changed" {
						g := generation + 1
						recipient.Routing.Generation = &g
					}
					if executing && mode == "unsafe_action" {
						recipient.RecoveryActions[0].URL = "https://untrusted.invalid"
					}
					until := time.Now().Add(time.Hour)
					if executing && mode == "expired" {
						until = time.Now().Add(-time.Hour)
					}
					write(api.EventReceiptResponse{EventSource: "orders/?+", EventID: "evt&1", SnapshotCaptured: true, RetainUntil: &until, Recipients: []api.EventReceiptRecipientResponse{recipient}})
				case "/v1/apps/consumer/event-deliveries:replay-fanout-failure":
					if r.Method != "POST" {
						t.Error("wrong method")
					}
					var req api.ReplayEventFanoutFailureRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.RecoveryExpectation == nil || req.RecoveryExpectation.WorkflowName != "paid" || req.RecoveryExpectation.PublishedVersion != 3 || req.RecoveryExpectation.ReplayCount != generation {
						t.Errorf("missing or incorrect recovery fence: %+v", req.RecoveryExpectation)
					}
					if req.AllowExpired || req.EventID != "evt&1" || req.SubscriptionID != "selected" {
						t.Errorf("unsafe request %+v", req)
					}
					posts++
					keys = append(keys, r.Header.Get("Idempotency-Key"))
					if mode == "server_conflict" {
						w.WriteHeader(409)
						write(api.NewProblem(409, api.CodeEventRecoveryChanged, "Changed", "PRIVATE-CHANGE"))
						return
					}
					if mode == "uncommitted" && posts == 1 {
						w.WriteHeader(503)
						write(api.NewProblem(503, "unavailable", "Unavailable", "PRIVATE-REPLY"))
						return
					}
					generation++
					if mode == "lost_reply" {
						w.WriteHeader(503)
						write(api.NewProblem(503, "unavailable", "Unavailable", "PRIVATE-REPLY"))
						return
					}
					write(api.ReplayEventFanoutFailureResponse{EventSource: "orders/?+", EventID: "evt&1", SubscriptionID: "selected", State: "pending"})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			dir := t.TempDir()
			plan := filepath.Join(dir, "plan.json")
			args := []string{"event-recovery-plan", "--app", "consumer", "--name", "paid", "--source", "orders/?+", "--event", "evt&1", "--idempotency-key", "incident-1", "--out", plan}
			if code := main.CmdAutomations(args); code != 0 {
				t.Fatalf("export=%d", code)
			}
			mu.Lock()
			if posts != 0 {
				t.Error("export mutated")
			}
			executing = true
			mu.Unlock()
			data, err := os.ReadFile(plan)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "PRIVATE") {
				t.Fatal("leaked private data")
			}
			capture := func(fn func()) string { buffer := main.CaptureAutomationStdout(t); fn(); return buffer.String() }
			(*main.JsonOutput) = true
			output := capture(func() {
				code := main.CmdAutomations([]string{"event-recover", "--plan", plan, "--journal", filepath.Join(dir, "first.jsonl")})
				if (mode == "success" || mode == "legacy") && code != 0 || mode != "success" && mode != "legacy" && code != 1 {
					t.Errorf("execute=%d", code)
				}
			})
			expectedReasons := map[string]string{"success": "routing_replay_accepted", "server_conflict": "routing_state_changed", "legacy": "routing_replay_accepted", "lost_reply": "replay_unconfirmed", "uncommitted": "replay_unconfirmed", "revision_changed": "automation_revision_changed", "expired": "event_expired", "deadline_expired": "delivery_deadline_expired", "generation_changed": "routing_state_changed", "unsafe_action": "routing_action_unsupported", "read_error": "receipt_unavailable"}
			var outcomes []main.AutomationEventPlanOutcome
			if err := json.Unmarshal([]byte(output), &outcomes); err != nil {
				t.Fatalf("decode outcomes: %v: %s", err, output)
			}
			if len(outcomes) != 1 || outcomes[0].Reason != expectedReasons[mode] {
				t.Fatalf("unexpected outcomes: %+v", outcomes)
			}
			if strings.Contains(output, "PRIVATE") {
				t.Fatal("private output")
			}
			want := 0
			if mode == "success" || mode == "server_conflict" || mode == "legacy" || mode == "lost_reply" || mode == "uncommitted" {
				want = 1
			}
			mu.Lock()
			if posts != want {
				t.Errorf("posts=%d want=%d", posts, want)
			}
			if want == 1 && (len(keys) != 1 || !strings.HasPrefix(keys[0], "automation-event-recover-")) {
				t.Errorf("keys=%v", keys)
			}
			mu.Unlock()
			journal, err := os.ReadFile(filepath.Join(dir, "first.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(journal), "PRIVATE") || !strings.Contains(string(journal), `"kind":"outcome"`) {
				t.Fatal("invalid journal")
			}
			if mode == "uncommitted" {
				capture(func() {
					if main.CmdAutomations([]string{"event-recover", "--plan", plan, "--journal", filepath.Join(dir, "retry.jsonl")}) != 0 {
						t.Error("uncommitted retry failed")
					}
				})
				mu.Lock()
				if posts != 2 || generation != 1 || len(keys) != 2 || keys[0] != keys[1] {
					t.Errorf("unstable retry: posts=%d generation=%d keys=%v", posts, generation, keys)
				}
				mu.Unlock()
			}
			if mode == "success" || mode == "legacy" || mode == "lost_reply" {
				capture(func() {
					if main.CmdAutomations([]string{"event-recover", "--plan", plan, "--journal", filepath.Join(dir, "retry.jsonl")}) != 1 {
						t.Error("retry adopted generation")
					}
				})
				mu.Lock()
				if posts != 1 {
					t.Error("duplicate replay")
				}
				mu.Unlock()
			}
		})
	}
}

func TestAutomationEventPlanRejectsInvalidSelections(t *testing.T) {
	p := main.AutomationEventPlan{Version: 1, App: "consumer", AppID: inspectorAppID, Name: "paid", Revision: 3, Source: "orders/?+", Key: "key", CreatedAt: time.Now(), Events: []main.AutomationEventPlanItem{{ID: "evt&1", Subscription: "selected", Action: recoveryRecipient("routing_replay").RecoveryActions[0]}}}
	if err := main.ValidateAutomationEventPlan(p); err != nil {
		t.Fatal(err)
	}
	p.Events = append(p.Events, p.Events[0])
	if main.ValidateAutomationEventPlan(p) == nil {
		t.Fatal("accepted duplicate")
	}
	p.Events = p.Events[:1]
	p.Events[0].Action.Body.AllowExpired = true
	if main.ValidateAutomationEventPlan(p) == nil {
		t.Fatal("accepted age override")
	}
	p.Events[0].Action.Body.AllowExpired = false
	data, _ := json.Marshal(p)
	for _, text := range []string{string(data) + "{}", strings.Replace(string(data), `"version":1`, `"version":2`, 1), strings.Replace(string(data), `"version":1`, `"unknown":1,"version":1`, 1)} {
		file := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := main.ReadAutomationEventPlan(file); err == nil {
			t.Fatal("accepted malformed plan")
		}
	}
}
