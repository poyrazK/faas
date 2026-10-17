package state

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
)

func sealTestActions(t *testing.T, f effectFixture) api.ScenarioActionSnapshot {
	t.Helper()
	snapshot, err := f.base.(ScenarioActionSnapshotStore).SealScenarioTestActions(t.Context(), f.account.ID, actionCaptureTestRun)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestScenarioActionSnapshotWaitsRetainsCommittedReceiverAndSurvivesCleanup(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			ctx := t.Context()
			op, claim := f.claim(t, "")
			if snapshot := sealTestActions(t, f); snapshot.Status != "pending" || snapshot.PendingManagedWork != 1 || snapshot.SealedAt != "" {
				t.Fatalf("active work sealed: %+v", snapshot)
			}
			if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.appHook, "notify")}); err != nil {
				t.Fatal(err)
			}
			changedURL := "https://changed.example.invalid/private-token"
			if _, err := f.base.UpdateAppWebhook(ctx, f.appHook.ID, UpdateAppWebhookParams{TargetURL: &changedURL}); err != nil {
				t.Fatal(err)
			}
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || len(snapshot.Intents) != 1 {
				t.Fatalf("invalid completed inventory: %+v %v", snapshot, err)
			}
			intent := snapshot.Intents[0]
			wantHash := api.ScenarioReceiverFingerprint(string(f.appHook.Scope), "", f.appHook.TargetURL, string(f.appHook.DeliveryFormat), string(f.appHook.RetryPolicy))
			if intent.ReceiverSHA256 != wantHash || canonicalMemUUID(intent.OperationID) != canonicalMemUUID(op.ID) || compactEffectJSON(intent.Payload) != `{"order_id":123}` {
				t.Fatalf("capture changed with receiver config: %+v", intent)
			}
			body, _ := json.Marshal(snapshot)
			if strings.Contains(string(body), "private-token") || strings.Contains(string(body), f.appHook.TargetURL) {
				t.Fatal("snapshot exported receiver credentials")
			}
			snapshot.Intents[0].Payload[0] = '!'
			if mem, ok := f.base.(*MemStore); ok {
				mem.mu.Lock()
				payload := string(mem.exclusiveEffects[op.ID][0].Effect.Payload)
				mem.mu.Unlock()
				if !json.Valid([]byte(payload)) {
					t.Fatal("snapshot payload aliased retained completion state")
				}
			}
			if _, err := f.base.(ScenarioActionSnapshotStore).SealScenarioTestActions(ctx, uuid.NewString(), actionCaptureTestRun); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign account read capture: %v", err)
			}
			if _, err := f.base.SoftDeleteAppCascade(ctx, f.app.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.base.DeleteScenarioTestMembers(ctx, f.account.ID, actionCaptureTestRun); err != nil {
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
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			ctx := t.Context()
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || snapshot.CapturedCount != 0 {
				t.Fatalf("explicit empty capture invalid: %+v %v", snapshot, err)
			}
			op, claim := f.claim(t, "")
			if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.appHook, "late")}); !errors.Is(err, ErrOperationEffectDestination) {
				t.Fatalf("late completion permitted: %v", err)
			}
			if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{{Name: "custom", Payload: json.RawMessage(`{}`)}}); err == nil {
				t.Fatal("generic managed effect reopened sealed window")
			}
			got, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
			if err != nil || len(got.Effects) != 0 || got.State != "running" {
				t.Fatalf("sealed completion partially persisted: %+v %v", got, err)
			}
			for _, mode := range []string{"", ScenarioWebhookCaptureMode} {
				if err := f.base.RegisterScenarioTestMembers(ctx, f.account.ID, actionCaptureTestRun, []ScenarioTestMember{{AppID: f.app.ID, Workload: "other", WebhookMode: mode}}); !errors.Is(err, ErrConflict) {
					t.Fatalf("sealed run enrollment changed: %v", err)
				}
			}
			if f.pool != nil {
				_, err := f.pool.Exec(ctx, `INSERT INTO exclusive_work_effects(id,operation_id,generation,name,payload,webhook_id,event_type) VALUES($1,$2,1,'legacy','{}',$3,'order.fulfilled')`, uuid.NewString(), op.ID, f.appHook.ID)
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
				f := newEffectFixture(t, backend, actionCaptureTestRun)
				_, claim := f.claim(t, "")
				start, result := make(chan struct{}), make(chan error, 1)
				go func() {
					<-start
					result <- f.owners.CommitExclusiveOperation(t.Context(), claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.appHook, "notify")})
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
				f := newEffectFixture(t, backend, actionCaptureTestRun)
				count := 1
				if mode == "intent-count" {
					count = api.MaxScenarioActionIntents + 1
				}
				if mode == "payload-bytes" {
					count = 18
				}
				for completed := 0; completed < count; {
					op, claim := f.claim(t, "")
					var effects []exclusivework.Effect
					for len(effects) < api.MaxExclusiveEffectsPerCommit && completed < count {
						effect := webhookEffect(f.appHook, fmt.Sprintf("notify-%d", completed))
						if mode == "payload-bytes" {
							effect.Payload = json.RawMessage(`"` + strings.Repeat("x", 30<<10) + `"`)
						}
						effects = append(effects, effect)
						completed++
					}
					if err := f.owners.CommitExclusiveOperation(t.Context(), claim, json.RawMessage(`null`), effects); err != nil {
						t.Fatal(err)
					}
					if mode == "missing-fingerprint" {
						if mem, ok := f.base.(*MemStore); ok {
							mem.mu.Lock()
							stored := mem.exclusiveEffects[op.ID]
							stored[0].ReceiverSHA256 = ""
							mem.exclusiveEffects[op.ID] = stored
							mem.mu.Unlock()
						} else if _, err := f.pool.Exec(t.Context(), `UPDATE exclusive_work_effects SET receiver_sha256='' WHERE operation_id=$1`, op.ID); err != nil {
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
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			ctx := t.Context()
			definition := api.WorkflowSpec{Name: "import", Steps: []api.WorkflowStepSpec{{Name: "notify", Path: "/import", ManagedOperation: true}}}
			body, _ := json.Marshal(definition)
			run := &WorkflowRun{AppID: f.app.ID, PlatformTenantID: f.tenant.ID, WorkflowName: "import", DefinitionSnapshot: body}
			if err := f.base.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			if err := f.base.CreateWorkflowSteps(ctx, run.ID, []*WorkflowStep{{StepName: "notify"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.base.ClaimNextDueWorkflowRun(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := f.base.StartWorkflowStep(ctx, run.ID, "notify", 1, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			id, err := api.ManagedWorkflowStepOperationID(run.ID, "notify")
			if err != nil {
				t.Fatal(err)
			}
			commit := ManagedWorkflowStepCommit{RunID: run.ID, StepName: "notify", OperationID: id, Attempt: 1, HTTPStatus: 200, Output: json.RawMessage(`{}`), Effects: []exclusivework.Effect{webhookEffect(f.hook, "notify")}}
			if err := f.base.(ManagedWorkflowStepCommitter).CommitManagedWorkflowStep(ctx, commit); err != nil {
				t.Fatal(err)
			}
			if snapshot := sealTestActions(t, f); snapshot.Status != "pending" {
				t.Fatalf("running workflow sealed: %+v", snapshot)
			}
			if err := f.base.MarkWorkflowRunStatus(ctx, run.ID, WorkflowRunStatusSucceeded, json.RawMessage(`{}`), nil); err != nil {
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
