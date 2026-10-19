package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func responseTestPlan() main.AutomationResponsePlan {
	n := 1
	p := main.AutomationResponsePlan{Version: 1, App: "consumer", AppID: inspectorAppID, JobID: recoveryInvocationID, Key: "incident-1", CreatedAt: time.Now().UTC()}
	a := main.AutomationResponseAction{Kind: "cancel", Position: 1, RunID: inspectorRunID, Cancellation: &api.WorkflowRecoveryCancellationRequest{Position: 1, ExpectedResumeCount: &n, Reason: "incident"}}
	a.Cancellation.RequestID = main.ResponseActionID(p, a)
	p.Actions = []main.AutomationResponseAction{a}
	return p
}
func TestAutomationRecoveryResponsePlanValidation(t *testing.T) {
	p := responseTestPlan()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := main.SaveAutomationResponsePlan(path, p); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ReadAutomationResponsePlan(path); err != nil {
		t.Fatal(err)
	}
	if err := main.SaveAutomationResponsePlan(path, p); err == nil {
		t.Fatal("overwrote reviewed plan")
	}
	p.Actions = append(p.Actions, p.Actions[0])
	if main.ValidateAutomationResponsePlan(p) == nil {
		t.Fatal("duplicate target accepted")
	}
	p = responseTestPlan()
	p.Actions[0].Cancellation.RequestID = recoverySecondRunID
	if main.ValidateAutomationResponsePlan(p) == nil {
		t.Fatal("changed identity accepted")
	}
}
func TestAutomationRecoveryResponseResume(t *testing.T) {
	for _, scenario := range []string{"lost_reply", "readonly", "changed", "confirmation", "scope", "receipt_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			p := responseTestPlan()
			path := filepath.Join(t.TempDir(), "plan.json")
			if err := main.SaveAutomationResponsePlan(path, p); err != nil {
				t.Fatal(err)
			}
			posts, gets := 0, 0
			var receipt *api.WorkflowRecoveryActionReceipt
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/consumer":
					id := p.AppID
					if scenario == "scope" {
						id = recoverySecondRunID
					}
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: id})
				case "/v1/event-recoveries/" + p.JobID + "/workflow-action-receipts/" + p.Actions[0].Cancellation.RequestID:
					gets++
					if receipt == nil {
						w.WriteHeader(404)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: 404, Code: api.CodeNotFound})
						return
					}
					_ = json.NewEncoder(w).Encode(receipt)

				case "/v1/event-recoveries/" + p.JobID:
					_ = json.NewEncoder(w).Encode(api.EventRecoveryJob{ID: p.JobID, AppID: p.AppID, Selection: api.EventRecoveryRequest{WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover"}}})
				case "/v1/event-recoveries/" + p.JobID + "/items":
					count := 0
					_ = json.NewEncoder(w).Encode(api.EventRecoveryItems{JobID: p.JobID, Items: []api.EventRecoveryItem{{Position: 1, WorkflowRunID: inspectorRunID, ExpectedResumeCount: &count, State: "queued"}}})
				case "/v1/event-recoveries/" + p.JobID + "/workflow-cancel":
					posts++
					var req api.WorkflowRecoveryCancellationRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Fatal(err)
					}
					out := api.WorkflowRecoveryCancellationResponse{JobID: p.JobID, Position: 1, RunID: inspectorRunID, ResumeCount: 1, Outcome: "cancelled"}
					if scenario == "changed" {
						out.Outcome = "changed"
					}
					receipt = &api.WorkflowRecoveryActionReceipt{Request: req, Response: out, CapturedAt: time.Now().UTC(), ActorKind: "api_key", ActorID: "operator"}
					if scenario == "lost_reply" {
						w.WriteHeader(504)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: 504})
						return
					}
					_ = json.NewEncoder(w).Encode(out)
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			})
			main.CaptureAutomationStdout(t)
			if scenario == "receipt_conflict" {
				receipt = &api.WorkflowRecoveryActionReceipt{Request: *p.Actions[0].Cancellation, Response: api.WorkflowRecoveryCancellationResponse{JobID: p.JobID, Position: 1, ResumeCount: 2, Outcome: "cancelled"}, CapturedAt: time.Now().UTC()}
			}
			if scenario == "readonly" {
				if code := main.CmdAutomationsResponseExecute([]string{"--plan", path}, "status"); code != 5 {
					t.Fatalf("readonly code=%d", code)
				}
				if posts != 0 || gets != 1 {
					t.Fatalf("readonly posts=%d gets=%d", posts, gets)
				}
				return
			}
			args := []string{"--plan", path}
			if scenario != "confirmation" {
				args = append(args, "--yes")
			}
			code := main.CmdAutomationsResponseExecute(args, "apply")
			want := 0
			if scenario == "confirmation" || scenario == "scope" {
				want = 1
			} else if scenario == "lost_reply" || scenario == "changed" || scenario == "receipt_conflict" {
				want = 5
			}
			if code != want {
				t.Fatalf("apply=%d want=%d", code, want)
			}
			if scenario == "confirmation" || scenario == "scope" || scenario == "receipt_conflict" {
				if posts != 0 {
					t.Fatal("unsafe submission")
				}
				return
			}
			code = main.CmdAutomationsResponseExecute([]string{"--plan", path, "--yes"}, "apply")
			want = 0
			if scenario == "changed" {
				want = 5
			}
			if code != want || posts != 1 {
				t.Fatalf("resume=%d posts=%d", code, posts)
			}
			code = main.CmdAutomationsResponseExecute([]string{"--plan", path}, "status")
			if code != want || posts != 1 {
				t.Fatalf("reconcile=%d posts=%d", code, posts)
			}
		})
	}
}

func TestAutomationRecoveryResponseRetryReconciliation(t *testing.T) {
	for _, scenario := range []string{"pending", "running", "failed", "succeeded", "missing", "conflict", "lineage", "lost_reply"} {
		t.Run(scenario, func(t *testing.T) {
			p := responseTestPlan()
			rp := main.AutomationRecoveryPlan{Version: 1, App: p.App, AppID: p.AppID, Name: "recover", ParentJobID: p.JobID, Key: p.Key, CreatedAt: p.CreatedAt, Runs: []main.AutomationRecoverySelection{{ID: inspectorRunID, ExpectedResumeCount: 1, ParentPosition: 1}}}
			req := main.AutomationRunJobRequest(rp, 1, "incident")
			a := main.AutomationResponseAction{Kind: "retry", Position: 1, RunID: inspectorRunID, Retry: &req}
			req.RequestID = main.ResponseActionID(p, a)
			p.Actions = []main.AutomationResponseAction{a}
			path := filepath.Join(t.TempDir(), "plan.json")
			if err := main.SaveAutomationResponsePlan(path, p); err != nil {
				t.Fatal(err)
			}
			childSelection := req
			childSelection.Reason = ""
			child := api.EventRecoveryJob{ID: recoverySecondRunID, AppID: p.AppID, Selection: childSelection}
			if scenario == "conflict" {
				child.AppID = recoveryInvocationID
			}
			retained := scenario != "missing" && scenario != "lost_reply"
			posts := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/consumer":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: p.AppID})
				case "/v1/apps/consumer/event-recoveries/by-request/" + req.RequestID:
					if !retained {
						w.WriteHeader(404)
						_ = json.NewEncoder(w).Encode(api.Problem{Status: 404})
						return
					}
					_ = json.NewEncoder(w).Encode(child)
				case "/v1/apps/consumer/event-recoveries":
					if r.Method != "POST" {
						t.Fatal("unexpected method")
					}
					posts++
					var submitted api.EventRecoveryRequest
					if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
						t.Fatal(err)
					}
					if submitted.RequestID != req.RequestID || submitted.WorkflowRuns.Runs[0].ExpectedResumeCount != 1 {
						t.Fatalf("refreshed retry=%+v", submitted)
					}
					retained = true
					w.WriteHeader(504)
					_ = json.NewEncoder(w).Encode(api.Problem{Status: 504})
				case "/v1/event-recoveries/" + child.ID + "/items":
					count, pos := 1, int64(1)
					item := api.EventRecoveryItem{Position: 1, WorkflowRunID: inspectorRunID, ExpectedResumeCount: &count, ParentJobID: p.JobID, ParentPosition: &pos, State: "pending"}
					if scenario != "pending" && scenario != "lost_reply" {
						item.State = "queued"
						state, source := scenario, "workflow_run"
						if scenario == "failed" || scenario == "succeeded" {
							source = "recovery_result"
						}
						now := time.Now().UTC()
						item.Execution = &api.EventRecoveryExecution{State: state, Source: source, EvidenceSource: "workflow_run", RecordedAt: &now}
					}
					if scenario == "lineage" {
						item.ParentJobID = child.ID
					}
					_ = json.NewEncoder(w).Encode(api.EventRecoveryItems{JobID: child.ID, Items: []api.EventRecoveryItem{item}})
				default:
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			})
			main.CaptureAutomationStdout(t)
			if scenario == "lost_reply" {
				if code := main.CmdAutomationsResponseExecute([]string{"--plan", path, "--yes"}, "apply"); code != 5 || posts != 1 {
					t.Fatalf("first apply=%d posts=%d", code, posts)
				}
				if code := main.CmdAutomationsResponseExecute([]string{"--plan", path, "--yes"}, "apply"); code != 5 || posts != 1 {
					t.Fatalf("resume=%d posts=%d", code, posts)
				}
			}
			code := main.CmdAutomationsResponseExecute([]string{"--plan", path}, "status")
			want := 5
			if scenario == "failed" || scenario == "succeeded" {
				want = 0
			}
			if code != want {
				t.Fatalf("status=%d want=%d", code, want)
			}
			if scenario != "lost_reply" && posts != 0 {
				t.Fatal("status submitted retry")
			}
		})
	}
}

func TestAutomationRecoveryResponsePreview(t *testing.T) {
	for _, scenario := range []string{"eligible", "changed", "target_changed", "parent_scope"} {
		t.Run(scenario, func(t *testing.T) {
			p := responseTestPlan()
			path := filepath.Join(t.TempDir(), "plan.json")
			if err := main.SaveAutomationResponsePlan(path, p); err != nil {
				t.Fatal(err)
			}
			previews := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/consumer":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: p.AppID})
				case "/v1/event-recoveries/" + p.JobID:
					_app := p.AppID
					if scenario == "parent_scope" {
						_app = recoverySecondRunID
					}
					_ = json.NewEncoder(w).Encode(api.EventRecoveryJob{ID: p.JobID, AppID: _app, Selection: api.EventRecoveryRequest{WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover"}}})
				case "/v1/event-recoveries/" + p.JobID + "/items":
					count := 0
					run := inspectorRunID
					if scenario == "target_changed" {
						run = recoverySecondRunID
					}
					_ = json.NewEncoder(w).Encode(api.EventRecoveryItems{JobID: p.JobID, Items: []api.EventRecoveryItem{{Position: 1, WorkflowRunID: run, ExpectedResumeCount: &count}}})
				case "/v1/event-recoveries/" + p.JobID + "/workflow-action-receipts/" + p.Actions[0].Cancellation.RequestID:
					w.WriteHeader(404)
					_ = json.NewEncoder(w).Encode(api.Problem{Status: 404})
				case "/v1/event-recoveries/" + p.JobID + "/workflow-cancellation-preview":
					previews++
					outcome := "eligible"
					if scenario == "changed" {
						outcome = "changed"
					}
					_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryCancellationResponse{JobID: p.JobID, Position: 1, RunID: inspectorRunID, ResumeCount: 1, Outcome: outcome, Preview: true})
				default:
					t.Fatalf("preview submitted unexpected request %s %s", r.Method, r.URL.Path)
				}
			})
			main.CaptureAutomationStdout(t)
			code := main.CmdAutomationsResponseExecute([]string{"--plan", path}, "preview")
			want := 5
			calls := 0
			if scenario == "eligible" {
				want = 0
			}
			if scenario == "eligible" || scenario == "changed" {
				calls = 1
			}
			if code != want || previews != calls {
				t.Fatalf("code=%d previews=%d", code, previews)
			}
		})
	}
}

func TestAutomationRecoveryResponsePlanCreation(t *testing.T) {
	for _, scenario := range []string{"eligible", "changed", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "response.json")
			previews := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/apps/consumer":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: inspectorAppID})
				case "/v1/event-recoveries/" + recoveryInvocationID:
					_ = json.NewEncoder(w).Encode(api.EventRecoveryJob{ID: recoveryInvocationID, AppID: inspectorAppID, Selection: api.EventRecoveryRequest{WorkflowRuns: &api.WorkflowRunRecoveryPlan{Name: "recover"}}})
				case "/v1/event-recoveries/" + recoveryInvocationID + "/items":
					count := 0
					_ = json.NewEncoder(w).Encode(api.EventRecoveryItems{JobID: recoveryInvocationID, Items: []api.EventRecoveryItem{{Position: 1, WorkflowRunID: inspectorRunID, ExpectedResumeCount: &count, State: "queued"}}})
				case "/v1/event-recoveries/" + recoveryInvocationID + "/workflow-cancellation-preview":
					previews++
					outcome := "eligible"
					if scenario == "changed" {
						outcome = "changed"
					}
					_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryCancellationResponse{JobID: recoveryInvocationID, Position: 1, RunID: inspectorRunID, ResumeCount: 1, Outcome: outcome, Preview: true})
				default:
					t.Fatalf("plan creation submitted unexpected request %s %s", r.Method, r.URL.Path)
				}
			})
			main.CaptureAutomationStdout(t)
			args := []string{"--app", "consumer", "--job", recoveryInvocationID, "--key", "incident-1", "--reason", "incident", "--plan-out", path, "--cancel-position", "1"}
			if scenario == "duplicate" {
				args = append(args, "--cancel-position", "1")
			}
			code := main.CmdAutomationsResponsePlan(args)
			if scenario == "eligible" {
				if code != 0 {
					t.Fatalf("save=%d", code)
				}
				p, err := main.ReadAutomationResponsePlan(path)
				if err != nil || len(p.Actions) != 1 || p.Actions[0].RunID != inspectorRunID || *p.Actions[0].Cancellation.ExpectedResumeCount != 1 {
					t.Fatalf("saved plan=%+v %v", p, err)
				}
			} else {
				if code == 0 {
					t.Fatal("saved ineligible plan")
				}
				if _, err := main.ReadAutomationResponsePlan(path); err == nil {
					t.Fatal("saved invalid selection")
				}
			}
		})
	}
}
