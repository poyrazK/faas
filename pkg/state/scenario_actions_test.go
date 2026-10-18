package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

const actionCaptureTestRun = "abababababababababababababababab"

func TestScenarioActionCaptureOperationAtomicityAndMultiplicity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/effects-%d", backend, count), func(t *testing.T) {
				f := newEffectFixture(t, backend, actionCaptureTestRun)
				ctx := t.Context()
				op, claim := f.claim(t, f.tenant.ID)
				bad := []exclusivework.Effect{webhookEffect(f.hook, "first"), webhookEffect(f.otherHook, "foreign")}
				if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`{"ok":true}`), bad); !errors.Is(err, ErrOperationEffectDestination) {
					t.Fatalf("authorization bypass: %v", err)
				}
				before, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
				if err != nil || len(before.Effects) != 0 || before.State != "running" {
					t.Fatalf("partial capture: %+v %v", before, err)
				}
				var effects []exclusivework.Effect
				for i := range count {
					effects = append(effects, webhookEffect(f.hook, fmt.Sprintf("notification-%d", i)))
				}
				if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`{"ok":true}`), effects); err != nil {
					t.Fatal(err)
				}
				if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`{"ok":true}`), effects); !errors.Is(err, exclusivework.ErrStaleOwner) {
					t.Fatalf("repeated completion: %v", err)
				}
				after, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
				if err != nil || after.State != "completed" || len(after.Effects) != count {
					t.Fatalf("lost captured effects: %+v %v", after, err)
				}
				seen := map[string]bool{}
				for _, effect := range after.Effects {
					if effect.Status != "captured" || effect.RehearsalRunID != actionCaptureTestRun || effect.DeliveryID != "" || effect.Attempt != 0 || seen[effect.ID] {
						t.Fatalf("invalid capture identity: %+v", effect)
					}
					seen[effect.ID] = true
					if _, err := f.base.AppWebhookDeliveryByID(ctx, effect.ID); !errors.Is(err, ErrNotFound) {
						t.Fatalf("captured effect queued a delivery: %v", err)
					}
				}
				rows, _, err := f.base.ListAppWebhookDeliveries(ctx, f.app.ID, f.hook.ID, 100, "")
				if err != nil || len(rows) != 0 {
					t.Fatalf("capture leaked an outbox row: %+v %v", rows, err)
				}
			})
		}
	}
}

func TestScenarioActionCaptureWorkflowAtomicity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			ctx := t.Context()
			definition := api.WorkflowSpec{Name: "fulfill", Steps: []api.WorkflowStepSpec{{Name: "commit", Path: "/fulfill", ManagedOperation: true}}}
			snapshot, _ := json.Marshal(definition)
			run := &WorkflowRun{AppID: f.app.ID, PlatformTenantID: f.tenant.ID, WorkflowName: definition.Name, DefinitionSnapshot: snapshot}
			if err := f.base.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			if err := f.base.CreateWorkflowSteps(ctx, run.ID, []*WorkflowStep{{StepName: "commit"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.base.ClaimNextDueWorkflowRun(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := f.base.StartWorkflowStep(ctx, run.ID, "commit", 1, json.RawMessage(`{"order_id":123}`)); err != nil {
				t.Fatal(err)
			}
			operationID, err := api.ManagedWorkflowStepOperationID(run.ID, "commit")
			if err != nil {
				t.Fatal(err)
			}
			commit := ManagedWorkflowStepCommit{RunID: run.ID, StepName: "commit", OperationID: operationID, Attempt: 1, HTTPStatus: 200, Output: json.RawMessage(`{"ok":true}`), Effects: []exclusivework.Effect{webhookEffect(f.hook, "first"), webhookEffect(f.otherHook, "foreign")}}
			committer := f.base.(ManagedWorkflowStepCommitter)
			if err := committer.CommitManagedWorkflowStep(ctx, commit); !errors.Is(err, ErrOperationEffectDestination) {
				t.Fatalf("workflow authorization bypass: %v", err)
			}
			attempts, err := f.base.GetWorkflowStepAttempts(ctx, run.ID, "commit")
			if err != nil || len(attempts) != 1 || len(attempts[0].Effects) != 0 || attempts[0].Status != WorkflowAttemptStatusRunning {
				t.Fatalf("partial workflow capture: %+v %v", attempts, err)
			}
			commit.Effects = []exclusivework.Effect{webhookEffect(f.hook, "notify")}
			if err := committer.CommitManagedWorkflowStep(ctx, commit); err != nil {
				t.Fatal(err)
			}
			attempts, err = f.base.GetWorkflowStepAttempts(ctx, run.ID, "commit")
			if err != nil || len(attempts) != 1 || len(attempts[0].Effects) != 1 {
				t.Fatalf("missing workflow capture: %+v %v", attempts, err)
			}
			effect := attempts[0].Effects[0]
			if effect.Status != "captured" || effect.RehearsalRunID != actionCaptureTestRun || effect.DeliveryID != "" || effect.Attempt != 0 {
				t.Fatalf("invalid workflow capture: %+v", effect)
			}
			if _, err := f.base.AppWebhookDeliveryByID(ctx, effect.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("workflow capture queued a delivery: %v", err)
			}
		})
	}
}

func TestScenarioActionPolicySuppressesOrdinaryEventsAndRetriesAfterCleanup(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			ctx := t.Context()
			row, err := f.base.RecordAppWebhookDelivery(ctx, AppWebhookDelivery{AppID: f.app.ID, AccountID: f.account.ID, WebhookID: f.appHook.ID, Event: AppWebhookEventDeploymentLive, Payload: json.RawMessage(`{"ordinary":true}`)})
			if err != nil || row.Status != AppWebhookDeliveryDead || row.LastError != ScenarioWebhookSuppressed {
				t.Fatalf("ordinary event not suppressed: %+v %v", row, err)
			}
			if err := f.base.ResetAppWebhookDeliveryFromDead(ctx, row.ID, row.WebhookID, f.account.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
			row, err = f.base.AppWebhookDeliveryByID(ctx, row.ID)
			if err != nil || row.Status != AppWebhookDeliveryDead {
				t.Fatalf("manual retry reopened a delivery: %+v %v", row, err)
			}
			claimed, err := f.base.ClaimDueAppWebhookDeliveries(ctx, 100, time.Now().Add(time.Hour))
			if err != nil || len(claimed) != 0 {
				t.Fatalf("suppressed delivery claimed: %+v %v", claimed, err)
			}
			if _, err := f.base.SoftDeleteAppCascade(ctx, f.app.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.base.DeleteScenarioTestMembers(ctx, f.account.ID, actionCaptureTestRun); err != nil {
				t.Fatal(err)
			}
			blocked, err := f.base.ScenarioWebhookDeliveryBlocked(ctx, f.app.ID, f.appHook.ID)
			if err != nil || !blocked {
				t.Fatalf("namespace deletion removed capture authority: %v %v", blocked, err)
			}
		})
	}
}

func TestScenarioActionPolicyRejectsLateEnrollmentAndExpiredCompletion(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			ctx := t.Context()
			expires := time.Now().Add(time.Hour)
			app, err := f.base.CreateApp(ctx, App{AccountID: f.account.ID, Slug: "late-" + uuid.NewString()[:8], PreviewOfSlug: "source", PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &expires})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.base.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:late", Status: DeployPending}); err != nil {
				t.Fatal(err)
			}
			if err := f.base.RegisterScenarioTestMembers(ctx, f.account.ID, strings.Repeat("c", 32), []ScenarioTestMember{{Workload: "late", AppID: app.ID, WebhookMode: ScenarioWebhookCaptureMode}}); !errors.Is(err, ErrConflict) {
				t.Fatalf("late enrollment permitted: %v", err)
			}
			op, claim := f.claim(t, f.tenant.ID)
			past := time.Now().Add(-time.Minute)
			if mem, ok := f.base.(*MemStore); ok {
				mem.mu.Lock()
				app := mem.apps[f.app.ID]
				app.PreviewExpiresAt = &past
				mem.apps[f.app.ID] = app
				mem.mu.Unlock()
			} else {
				if _, err := f.pool.Exec(ctx, "UPDATE apps SET preview_expires_at=$2 WHERE id=$1", f.app.ID, past); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.owners.CommitExclusiveOperation(ctx, claim, json.RawMessage(`null`), []exclusivework.Effect{webhookEffect(f.hook, "notify")}); !errors.Is(err, ErrOperationEffectDestination) {
				t.Fatalf("expired capture completed: %v", err)
			}
			got, err := f.owners.ExclusiveOperationByID(ctx, f.account.ID, op.ID)
			if err != nil || len(got.Effects) != 0 {
				t.Fatalf("expired capture persisted effects: %+v %v", got, err)
			}
			if blocked, err := f.base.ScenarioWebhookDeliveryBlocked(ctx, f.app.ID, f.hook.ID); err != nil || !blocked {
				t.Fatalf("expired policy released dispatch: %v %v", blocked, err)
			}
		})
	}
}

func TestScenarioActionPolicyEnrollmentRaceNeverLeavesRunnableDelivery(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend)
			ctx := t.Context()
			for iteration := 1; iteration <= 4; iteration++ {
				expires := time.Now().Add(time.Hour)
				app, err := f.base.CreateApp(ctx, App{AccountID: f.account.ID, Slug: "race-" + uuid.NewString()[:8], PreviewOfSlug: "source", PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &expires})
				if err != nil {
					t.Fatal(err)
				}
				hook, err := f.base.CreateAppWebhook(ctx, AppWebhook{AccountID: f.account.ID, AppID: app.ID, TargetURL: "https://receiver.example.invalid", SecretSealed: []byte("sealed"), Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				start := make(chan struct{})
				registered := make(chan error, 1)
				type queuedResult struct {
					row AppWebhookDelivery
					err error
				}
				queued := make(chan queuedResult, 1)
				go func() {
					<-start
					registered <- f.base.RegisterScenarioTestMembers(ctx, f.account.ID, strings.Repeat(fmt.Sprintf("%x", iteration), 32), []ScenarioTestMember{{AppID: app.ID, Workload: "primary", WebhookMode: ScenarioWebhookCaptureMode}})
				}()
				go func() {
					<-start
					row, err := f.base.RecordAppWebhookDelivery(ctx, AppWebhookDelivery{AppID: app.ID, AccountID: f.account.ID, WebhookID: hook.ID, Event: AppWebhookEventDeploymentLive, Payload: json.RawMessage(`{}`)})
					queued <- queuedResult{row, err}
				}()
				close(start)
				registrationErr, result := <-registered, <-queued
				if result.err != nil {
					t.Fatal(result.err)
				}
				if registrationErr == nil {
					if result.row.Status != AppWebhookDeliveryDead || result.row.LastError != ScenarioWebhookSuppressed {
						t.Fatalf("capture registration raced into live dispatch: %+v", result.row)
					}
				} else if !errors.Is(registrationErr, ErrConflict) {
					t.Fatalf("registration race: %v", registrationErr)
				}
			}
		})
	}
}
