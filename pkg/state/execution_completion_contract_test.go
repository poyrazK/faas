package state_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestMemExecutionFinishedDelivery(t *testing.T) {
	exerciseExecutionFinishedDelivery(t, state.NewMemStore())
}
func exerciseExecutionFinishedDelivery(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	limits, _ := api.LimitsFor(acct.Plan)
	hook, err := store.CreateAccountReleaseWebhookIfUnderQuota(ctx, state.AppWebhook{AccountID: acct.ID, TargetURL: "https://example.com/completion", SecretSealed: []byte("sealed"), Enabled: true, EventFilter: []string{api.ExecutionFinishedWebhookEvent}}, limits)
	if err != nil {
		t.Fatal("receiver", err)
	}
	expected := 0
	for _, kind := range []string{"succeeded", "failed", "timed_out", "out_of_memory", "cancelled", "queued_cancel", "queued_expiry", "owner_loss"} {
		if !t.Run(kind, func(t *testing.T) {
			base := time.Now().UTC()
			params := executionTestParams(t, acct.ID, base, 0, "PRIVATE_SOURCE_INPUT")
			run, err := store.CreateExecution(ctx, params)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "queued_cancel":
				_, err = store.RequestExecutionCancellation(ctx, acct.ID, run.ID, base.Add(time.Millisecond))
			case "queued_expiry":
				_, err = store.SweepExecutions(ctx, run.DeadlineAt.Add(time.Second), 100)
			default:
				claim, e := store.ClaimExecution(ctx, "completion-test", base.Add(time.Millisecond), time.Second)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = store.MarkExecutionRunning(ctx, run.ID, *claim.LeaseToken, base.Add(2*time.Millisecond)); e != nil {
					t.Fatal(e)
				}
				if kind == "owner_loss" {
					_, err = store.SweepExecutions(ctx, base.Add(2*time.Second), 100)
				} else {
					status := api.ExecutionStatus(kind)
					finish := base.Add(4 * time.Millisecond)
					if status == api.ExecutionStatusTimedOut {
						finish = base.Add(4 * time.Millisecond)
					}
					code, msg := "customer_error", "PRIVATE_FAILURE_MESSAGE"
					p := state.CompleteExecutionParams{ID: run.ID, LeaseToken: *claim.LeaseToken, Status: status, FinishedAt: finish, Stdout: "PRIVATE_STDOUT", Stderr: "PRIVATE_STDERR"}
					if status == api.ExecutionStatusSucceeded {
						p.Result = []byte(`{"private":"PRIVATE_RESULT"}`)
					} else if status != api.ExecutionStatusCancelled {
						p.FailureCode = &code
						p.FailureMessage = &msg
					}
					if status == api.ExecutionStatusTimedOut {
						_, err = store.SweepExecutions(ctx, run.DeadlineAt.Add(time.Millisecond), 100)
					} else {
						if status == api.ExecutionStatusCancelled {
							_, err = store.RequestExecutionCancellation(ctx, acct.ID, run.ID, base.Add(3*time.Millisecond))
							if err != nil {
								t.Fatal(err)
							}
						}
						_, err = store.CompleteExecution(ctx, p)
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			expected++
			_, _ = store.RequestExecutionCancellation(ctx, acct.ID, run.ID, time.Now().UTC().Add(time.Minute))
			rows, _, e := store.ListAccountReleaseWebhookDeliveries(ctx, acct.ID, hook.ID, 100, "")
			if e != nil || len(rows) != expected {
				t.Fatal("duplicate or missing deliveries", len(rows), expected, e)
			}
			var found bool
			for _, d := range rows {
				var payload api.ExecutionFinishedWebhookPayload
				if e = json.Unmarshal(d.Payload, &payload); e != nil {
					t.Fatal(e)
				}
				if strings.Contains(string(d.Payload), "PRIVATE_") {
					t.Fatal("private content in notification")
				}
				if payload.ExecutionID == run.ID {
					found = true
					if payload.EventID != run.ID || payload.ReceiptURL != "/v1/executions/"+run.ID || payload.FinishedAt == "" {
						t.Fatal(payload)
					}
				}
			}
			if !found {
				t.Fatal("notification missing")
			}
		}) {
			return
		}
	}
}
