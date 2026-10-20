package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/state"
)

func sealTestActions(t *testing.T, f state.EffectFixture) api.ScenarioActionSnapshot {
	t.Helper()
	snapshot, err := (*f.Base()).(state.ScenarioActionSnapshotStore).SealScenarioTestActions(t.Context(), (*f.Account()).ID, state.ActionCaptureTestRun)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestScenarioActionSnapshotWaitsRetainsCommittedReceiverAndSurvivesCleanup(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			op, claim := f.Claim(t, "")
			if snapshot := sealTestActions(t, f); snapshot.Status != "pending" || snapshot.PendingManagedWork != 1 || snapshot.SealedAt != "" {
				t.Fatalf("active work sealed: %+v", snapshot)
			}
			if err := (*f.Owners()).CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{state.WebhookEffect((*f.AppHook()), "notify")}); err != nil {
				t.Fatal(err)
			}
			changedURL := "https://changed.example.invalid/private-token"
			if _, err := (*f.Base()).UpdateAppWebhook(ctx, (*f.AppHook()).ID, state.UpdateAppWebhookParams{TargetURL: &changedURL}); err != nil {
				t.Fatal(err)
			}
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || len(snapshot.Intents) != 1 {
				t.Fatalf("invalid completed inventory: %+v %v", snapshot, err)
			}
			intent := snapshot.Intents[0]
			wantHash := api.ScenarioReceiverFingerprint(string((*f.AppHook()).Scope), "", (*f.AppHook()).TargetURL, string((*f.AppHook()).DeliveryFormat), string((*f.AppHook()).RetryPolicy))
			if intent.ReceiverSHA256 != wantHash || state.CanonicalMemUUID(intent.OperationID) != state.CanonicalMemUUID(op.ID) || state.CompactEffectJSON(intent.Payload) != `{"order_id":123}` {
				t.Fatalf("capture changed with receiver config: %+v", intent)
			}
			body, _ := json.Marshal(snapshot)
			if strings.Contains(string(body), "private-token") || strings.Contains(string(body), (*f.AppHook()).TargetURL) {
				t.Fatal("snapshot exported receiver credentials")
			}
			snapshot.Intents[0].Payload[0] = '!'
			if mem, ok := (*f.Base()).(*state.MemStore); ok {
				(*mem.Mu()).Lock()
				payload := string((*mem.ExclusiveEffects())[op.ID][0].Effect.Payload)
				(*mem.Mu()).Unlock()
				if !json.Valid([]byte(payload)) {
					t.Fatal("snapshot payload aliased retained completion state")
				}
			}
			if _, err := (*f.Base()).(state.ScenarioActionSnapshotStore).SealScenarioTestActions(ctx, uuid.NewString(), state.ActionCaptureTestRun); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign account read capture: %v", err)
			}
			if _, err := (*f.Base()).SoftDeleteAppCascade(ctx, (*f.App()).ID); err != nil {
				t.Fatal(err)
			}
			if err := (*f.Base()).DeleteScenarioTestMembers(ctx, (*f.Account()).ID, state.ActionCaptureTestRun); err != nil {
				t.Fatal(err)
			}
			replayed, _ := json.Marshal(sealTestActions(t, f))
			if string(replayed) != string(body) {
				t.Fatalf("cleanup changed sealed evidence: %s → %s", body, replayed)
			}
		})
	}
}

func TestScenarioActionSnapshotClosesCompletionAndEnrollmentWindow(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || snapshot.CapturedCount != 0 {
				t.Fatalf("explicit empty capture invalid: %+v %v", snapshot, err)
			}
			op, claim := f.Claim(t, "")
			if err := (*f.Owners()).CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{state.WebhookEffect((*f.AppHook()), "late")}); !errors.Is(err, state.ErrOperationEffectDestination) {
				t.Fatalf("late completion permitted: %v", err)
			}
			if err := (*f.Owners()).CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{{Name: "custom", Payload: json.RawMessage(`{}`)}}); err == nil {
				t.Fatal("generic managed effect reopened sealed window")
			}
			got, err := (*f.Owners()).ExclusiveOperationByID(ctx, (*f.Account()).ID, op.ID)
			if err != nil || len(got.Effects) != 0 || got.State != "running" {
				t.Fatalf("sealed completion partially persisted: %+v %v", got, err)
			}
			for _, mode := range []string{"", state.ScenarioWebhookCaptureMode} {
				if err := (*f.Base()).RegisterScenarioTestMembers(ctx, (*f.Account()).ID, state.ActionCaptureTestRun, []state.ScenarioTestMember{{AppID: (*f.App()).ID, Workload: "other", WebhookMode: mode}}); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("sealed run enrollment changed: %v", err)
				}
			}
			if (*f.Pool()) != nil {
				_, err := (*f.Pool()).Exec(ctx, `INSERT INTO exclusive_work_effects(id,operation_id,generation,name,payload,webhook_id,event_type) VALUES($1,$2,1,'legacy','{}',$3,'order.fulfilled')`, uuid.NewString(), op.ID, (*f.AppHook()).ID)
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "23514" || !strings.Contains(pgErr.Message, "window is sealed") {
					t.Fatalf("legacy worker bypassed sealed database fence: %v", err)
				}
			}
		})
	}
}

func TestScenarioActionSnapshotSerializesConcurrentCompletion(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for range 4 {
				f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
				_, claim := f.Claim(t, "")
				start, result := make(chan struct{}), make(chan error, 1)
				go func() {
					<-start
					result <- (*f.Owners()).CommitExclusiveOperation(t.Context(), claim, json.RawMessage(`null`), []exclusivework.Effect{state.WebhookEffect((*f.AppHook()), "notify")})
				}()
				close(start)
				first := sealTestActions(t, f)
				if err := <-result; err != nil {
					t.Fatal(err)
				}
				if first.Status != "pending" && (first.Status != "complete" || first.CapturedCount != 1) {
					t.Fatalf("capture raced into empty seal: %+v", first)
				}
				if final := sealTestActions(t, f); final.Status != "complete" || final.CapturedCount != 1 || len(final.Intents) != 1 {
					t.Fatalf("concurrent completion lost: %+v", final)
				}
			}
		})
	}
}

func TestScenarioActionSnapshotBoundsAndLegacyGapsNeverLookEmpty(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, mode := range []string{"missing-fingerprint", "intent-count", "payload-bytes"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
				count := 1
				if mode == "intent-count" {
					count = api.MaxScenarioActionIntents + 1
				}
				if mode == "payload-bytes" {
					count = 18
				}
				for completed := 0; completed < count; {
					op, claim := f.Claim(t, "")
					var effects []exclusivework.Effect
					for len(effects) < api.MaxExclusiveEffectsPerCommit && completed < count {
						effect := state.WebhookEffect((*f.AppHook()), fmt.Sprintf("notify-%d", completed))
						if mode == "payload-bytes" {
							effect.Payload = json.RawMessage(`"` + strings.Repeat("x", 30<<10) + `"`)
						}
						effects = append(effects, effect)
						completed++
					}
					if err := (*f.Owners()).CommitExclusiveOperation(t.Context(), claim, json.RawMessage(`null`), effects); err != nil {
						t.Fatal(err)
					}
					if mode == "missing-fingerprint" {
						if mem, ok := (*f.Base()).(*state.MemStore); ok {
							(*mem.Mu()).Lock()
							stored := (*mem.ExclusiveEffects())[op.ID]
							stored[0].ReceiverSHA256 = ""
							(*mem.ExclusiveEffects())[op.ID] = stored
							(*mem.Mu()).Unlock()
						} else if _, err := (*f.Pool()).Exec(t.Context(), `UPDATE exclusive_work_effects SET receiver_sha256='' WHERE operation_id=$1`, op.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				snapshot := sealTestActions(t, f)
				if snapshot.Status != "incomplete" || snapshot.SealedAt == "" || len(snapshot.Intents) != 0 || snapshot.CapturedCount != int64(count) || snapshot.Reason == "" {
					t.Fatalf("partial evidence appeared complete: %+v", snapshot)
				}
				if snapshot.Truncated != (mode != "missing-fingerprint") {
					t.Fatalf("wrong truncation: %+v", snapshot)
				}
				if _, err := actioncompare.ValidateSnapshot(&snapshot); err == nil {
					t.Fatal("incomplete snapshot passed validation")
				}
			})
		}
	}
}

func TestScenarioActionSnapshotIncludesWorkflowIntent(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			definition := api.WorkflowSpec{Name: "import", Steps: []api.WorkflowStepSpec{{Name: "notify", Path: "/import", ManagedOperation: true}}}
			body, _ := json.Marshal(definition)
			run := &state.WorkflowRun{AppID: (*f.App()).ID, PlatformTenantID: (*f.Tenant()).ID, WorkflowName: "import", DefinitionSnapshot: body}
			if err := (*f.Base()).CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			if err := (*f.Base()).CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "notify"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := (*f.Base()).ClaimNextDueWorkflowRun(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := (*f.Base()).StartWorkflowStep(ctx, run.ID, "notify", 1, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			id, err := api.ManagedWorkflowStepOperationID(run.ID, "notify")
			if err != nil {
				t.Fatal(err)
			}
			commit := state.ManagedWorkflowStepCommit{RunID: run.ID, StepName: "notify", OperationID: id, Attempt: 1, HTTPStatus: 200, Output: json.RawMessage(`{}`), Effects: []exclusivework.Effect{state.WebhookEffect((*f.Hook()), "notify")}}
			if err := (*f.Base()).(state.ManagedWorkflowStepCommitter).CommitManagedWorkflowStep(ctx, commit); err != nil {
				t.Fatal(err)
			}
			if snapshot := sealTestActions(t, f); snapshot.Status != "pending" {
				t.Fatalf("running workflow sealed: %+v", snapshot)
			}
			if err := (*f.Base()).MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusSucceeded, json.RawMessage(`{}`), nil); err != nil {
				t.Fatal(err)
			}
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || len(snapshot.Intents) != 1 {
				t.Fatalf("workflow capture: %+v %v", snapshot, err)
			}
			intent := snapshot.Intents[0]
			if intent.Source != "workflow" || intent.Workflow != "import" || intent.Step != "notify" || intent.ReceiverSHA256 == "" {
				t.Fatalf("workflow identity lost: %+v", intent)
			}
		})
	}
}
