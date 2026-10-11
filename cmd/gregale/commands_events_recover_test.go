package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const recoveryInvocationID = "11111111-1111-4111-8111-111111111111"
const recoveryChildID = "22222222-2222-4222-8222-222222222222"
const recoveryDeadLetterID = "33333333-3333-4333-8333-333333333333"

func eventRecoveryAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	resetJSONOut(t)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
}

func recoveryRecipient(kind string) api.EventReceiptRecipientResponse {
	r := api.EventReceiptRecipientResponse{SubscriptionID: "selected", AppSlug: "consumer", Routing: api.EventReceiptRoutingResponse{State: "enqueued"},
		Execution: &api.EventReceiptExecutionResponse{InvocationID: recoveryInvocationID, State: "failed"}}
	a := api.EventReceiptRecoveryAction{Kind: kind, Method: http.MethodPost}
	switch kind {
	case "routing_replay":
		r.Execution = nil
		r.Origin = "backfill"
		r.Routing.State = "failed"
		a.URL = "/v1/apps/consumer/event-deliveries:replay-fanout-failure"
		a.Body = &api.ReplayEventFanoutFailureRequest{EventSource: "orders/?+", EventID: "evt&1", SubscriptionID: r.SubscriptionID}
	case "handler_replay":
		a.URL = "/v1/invocations/" + recoveryInvocationID + "/replay"
	case "keyed_handler_replay":
		r.Recovery = &api.EventReceiptRecoveryResponse{LatestReplay: &api.EventReceiptExecutionResponse{InvocationID: recoveryChildID, State: "failed"}}
		a.URL = "/v1/invocations/" + recoveryChildID + "/replay-keyed"
	case "dead_letter_replay":
		r.Execution.State = "dead_letter"
		a.URL = "/v1/apps/consumer/dlq/" + recoveryDeadLetterID + "/replay"
	}
	r.RecoveryActions = []api.EventReceiptRecoveryAction{a}
	return r
}

func TestCmdEventsRecoverSelectsReceiptAction(t *testing.T) {
	for _, kind := range []string{"routing_replay", "workflow_routing_replay", "handler_replay", "keyed_handler_replay", "dead_letter_replay"} {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%t", kind, dryRun), func(t *testing.T) {
				actionKind := kind
				if kind == "workflow_routing_replay" {
					actionKind = "routing_replay"
				}
				recipient := recoveryRecipient(actionKind)
				if kind == "workflow_routing_replay" {
					recipient.Origin, recipient.WorkflowName = "acceptance", "paid"
				}
				gets, posts := 0, 0
				eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing authentication")
					}
					if r.Method == http.MethodGet {
						gets++
						if r.URL.Path != "/v1/events/receipt" || r.URL.Query().Get("source") != "orders/?+" || r.URL.Query().Get("id") != "evt&1" || r.URL.Query().Get("limit") != "200" {
							t.Errorf("receipt lookup: %s", r.URL)
						}
						page := api.EventReceiptResponse{EventSource: "orders/?+", EventID: "evt&1", SnapshotCaptured: true}
						if gets == 1 {
							page.Recipients = []api.EventReceiptRecipientResponse{recoveryRecipient("handler_replay")}
							page.Recipients[0].SubscriptionID = "successful-sibling"
							page.Recipients[0].Execution.State = "completed"
							page.Recipients[0].RecoveryActions = nil
							page.NextAfter = "erc1.next/?+"
						} else {
							if r.URL.Query().Get("after") != "erc1.next/?+" {
								t.Errorf("cursor lost: %s", r.URL)
							}
							page.Recipients = []api.EventReceiptRecipientResponse{recipient}
						}
						_ = json.NewEncoder(w).Encode(page)
						return
					}
					posts++
					if r.Method != http.MethodPost || r.URL.Path != recipient.RecoveryActions[0].URL || r.Header.Get("Idempotency-Key") == "" {
						t.Errorf("unexpected recovery: %s %s", r.Method, r.URL)
					}
					if actionKind == "routing_replay" {
						var body api.ReplayEventFanoutFailureRequest
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body != *recipient.RecoveryActions[0].Body {
							t.Errorf("replay body=%+v err=%v", body, err)
							return
						}
					}
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"id":"` + recoveryChildID + `"}`))
				})
				jsonOutput = true
				stdout, restore := swapStdout(t)
				defer restore()
				args := []string{"recover", "--source", "orders/?+", "--id", "evt&1", "--subscription", "selected"}
				if dryRun {
					args = append(args, "--dry-run")
				}
				if code := cmdEvents(args); code != 0 {
					t.Fatalf("exit=%d output=%s", code, stdout)
				}
				var got eventRecoveryResult
				if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Action == nil || got.Action.Kind != actionKind || got.SubscriptionID != "selected" || got.DryRun != dryRun {
					t.Fatalf("output=%s err=%v", stdout, err)
				}
				wantPosts, wantStatus := 1, "queued"
				if dryRun {
					wantPosts, wantStatus = 0, "available"
				}
				if gets != 2 || posts != wantPosts || got.Status != wantStatus || !dryRun && got.Result == nil {
					t.Fatalf("gets=%d posts=%d result=%+v", gets, posts, got)
				}
			})
		}
	}
}

func TestCmdEventsRecoverUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*api.EventReceiptRecipientResponse)
		want   string
	}{
		{"success", func(r *api.EventReceiptRecipientResponse) { r.Execution.State = "completed" }, "already completed"},
		{"recovered", func(r *api.EventReceiptRecipientResponse) {
			r.Recovery = &api.EventReceiptRecoveryResponse{LatestReplay: &api.EventReceiptExecutionResponse{State: "completed"}}
		}, "already completed"},
		{"active replay", func(r *api.EventReceiptRecipientResponse) {
			r.Recovery = &api.EventReceiptRecoveryResponse{LatestReplay: &api.EventReceiptExecutionResponse{State: "pending"}}
		}, "still in progress"},
		{"routing backoff", func(r *api.EventReceiptRecipientResponse) { r.Execution = nil; r.Routing.State = "pending" }, "Routing is still in progress"},
		{"cancel", func(r *api.EventReceiptRecipientResponse) { r.Cancellation = &api.EventReceiptCancellationResponse{} }, "cancelled"},
		{"pruned", func(r *api.EventReceiptRecipientResponse) { r.ExecutionUnavailable = "record_unavailable" }, "record_unavailable"},
		{"recovered after original pruned", func(r *api.EventReceiptRecipientResponse) {
			r.Execution = nil
			r.ExecutionUnavailable = "record_unavailable"
			r.Recovery = &api.EventReceiptRecoveryResponse{LatestReplay: &api.EventReceiptExecutionResponse{State: "completed"}}
		}, "already completed"},
		{"deleted app", func(r *api.EventReceiptRecipientResponse) { r.AppSlug = "" }, "application is unavailable"},
		{"workflow admitted", func(r *api.EventReceiptRecipientResponse) { r.WorkflowName = "paid" }, "Workflow admission already completed"},
		{"workflow pending", func(r *api.EventReceiptRecipientResponse) { r.WorkflowName = "paid"; r.Routing.State = "pending" }, "runtime is enabled"},
		{"workflow filtered", func(r *api.EventReceiptRecipientResponse) { r.WorkflowName = "paid"; r.Routing.State = "filtered" }, "workflow trigger filtered"},
		{"workflow unavailable", func(r *api.EventReceiptRecipientResponse) { r.WorkflowName = "paid"; r.Routing.State = "failed" }, "no workflow routing recovery action"},
		{"filtered", func(r *api.EventReceiptRecipientResponse) { r.Execution = nil; r.Routing.State = "filtered" }, "filtered out"},
		{"expired or blocked", func(r *api.EventReceiptRecipientResponse) {}, "no recovery action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recipient := recoveryRecipient("handler_replay")
			recipient.RecoveryActions = nil
			tc.modify(&recipient)
			posts := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					posts++
				}
				_ = json.NewEncoder(w).Encode(api.EventReceiptResponse{EventSource: "orders", EventID: "evt", SnapshotCaptured: true, Recipients: []api.EventReceiptRecipientResponse{recipient}})
			})
			stdout, restore := swapStdout(t)
			defer restore()
			for _, dryRun := range []bool{false, true} {
				args := []string{"--source", "orders", "--id", "evt", "--subscription", "selected"}
				wantCode := 1
				if dryRun {
					args = append(args, "--dry-run")
					wantCode = 0
				}
				stdout.Reset()
				if code := cmdEventsRecover(args); code != wantCode || !strings.Contains(stdout.String(), tc.want) || posts != 0 {
					t.Fatalf("dry=%t exit=%d posts=%d output=%s", dryRun, code, posts, stdout)
				}
			}
		})
	}
}

func TestCmdEventsRecoverRejectsUnsafeOrAmbiguousActions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*api.EventReceiptRecipientResponse)
	}{
		{"absolute URL", func(r *api.EventReceiptRecipientResponse) { r.RecoveryActions[0].URL = "https://elsewhere.test/replay" }},
		{"wrong execution", func(r *api.EventReceiptRecipientResponse) {
			r.RecoveryActions[0].URL = "/v1/invocations/" + recoveryChildID + "/replay"
		}},
		{"wrong method", func(r *api.EventReceiptRecipientResponse) { r.RecoveryActions[0].Method = http.MethodDelete }},
		{"unknown kind", func(r *api.EventReceiptRecipientResponse) { r.RecoveryActions[0].Kind = "future_replay" }},
		{"workflow handler replay", func(r *api.EventReceiptRecipientResponse) { r.WorkflowName = "paid" }},
		{"ambiguous", func(r *api.EventReceiptRecipientResponse) {
			r.RecoveryActions = append(r.RecoveryActions, r.RecoveryActions[0])
		}},
		{"wrong routing body", func(r *api.EventReceiptRecipientResponse) {
			*r = recoveryRecipient("routing_replay")
			r.RecoveryActions[0].Body.SubscriptionID = "sibling"
		}},
		{"wrong DLQ app", func(r *api.EventReceiptRecipientResponse) {
			*r = recoveryRecipient("dead_letter_replay")
			r.RecoveryActions[0].URL = "/v1/apps/sibling/dlq/" + recoveryDeadLetterID + "/replay"
		}},
		{"DLQ query", func(r *api.EventReceiptRecipientResponse) {
			*r = recoveryRecipient("dead_letter_replay")
			r.RecoveryActions[0].URL += "?other=1"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recipient := recoveryRecipient("handler_replay")
			tc.modify(&recipient)
			posts := 0
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					posts++
				}
				_ = json.NewEncoder(w).Encode(api.EventReceiptResponse{EventSource: "orders/?+", EventID: "evt&1", SnapshotCaptured: true, Recipients: []api.EventReceiptRecipientResponse{recipient}})
			})
			code, _ := runWithStderr(t, func() int {
				return cmdEventsRecover([]string{"--source", "orders/?+", "--id", "evt&1", "--subscription", "selected"})
			})
			if code != 1 || posts != 0 {
				t.Fatalf("exit=%d posts=%d", code, posts)
			}
		})
	}
}

func TestCmdEventsRecoverLookupErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		page api.EventReceiptResponse
		want string
	}{
		{"legacy", api.EventReceiptResponse{EventSource: "orders", EventID: "evt"}, "legacy event"},
		{"missing recipient", api.EventReceiptResponse{EventSource: "orders", EventID: "evt", SnapshotCaptured: true}, "not a retained recipient"},
		{"wrong identity", api.EventReceiptResponse{EventSource: "elsewhere", EventID: "evt", SnapshotCaptured: true}, "identity does not match"},
		{"cursor cycle", api.EventReceiptResponse{EventSource: "orders", EventID: "evt", SnapshotCaptured: true, NextAfter: "repeat"}, "pagination did not advance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("lookup error caused a mutation")
				}
				_ = json.NewEncoder(w).Encode(tc.page)
			})
			code, stderr := runWithStderr(t, func() int {
				return cmdEventsRecover([]string{"--source", "orders", "--id", "evt", "--subscription", "selected"})
			})
			if code != 1 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit=%d stderr=%s", code, stderr)
			}
		})
	}
}

func TestCmdEventsRecoverStaleActionDoesNotFallBack(t *testing.T) {
	posts := 0
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(api.EventReceiptResponse{EventSource: "orders", EventID: "evt", SnapshotCaptured: true, Recipients: []api.EventReceiptRecipientResponse{recoveryRecipient("handler_replay")}})
			return
		}
		posts++
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"status":409,"title":"Replay unavailable","detail":"eligibility changed","code":"invocation_replay_unavailable"}`))
	})
	code, stderr := runWithStderr(t, func() int {
		return cmdEventsRecover([]string{"--source", "orders", "--id", "evt", "--subscription", "selected"})
	})
	if code == 0 || posts != 1 || !strings.Contains(stderr, "eligibility changed") {
		t.Fatalf("exit=%d posts=%d stderr=%s", code, posts, stderr)
	}
}

func TestCmdEventsRecoverRejectsInvalidArguments(t *testing.T) {
	resetJSONOut(t)
	for _, args := range [][]string{nil, {"--source", "orders", "--id", "evt"}, {"app", "--source", "orders", "--id", "evt", "--subscription", "sub"}, {"--source", " ", "--id", "evt", "--subscription", "sub"}, {"--source", "orders", "--id", "evt", "--subscription", "sub", "--dry-run=invalid"}} {
		code, _ := runWithStderr(t, func() int { return cmdEventsRecover(args) })
		if code != 1 {
			t.Fatalf("arguments %v exit=%d", args, code)
		}
	}
}
