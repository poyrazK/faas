// adr: 20261022082721
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioCapturedWorkReadsBackInsideItsRun(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			base, account, app := *f.Base(), f.Account(), f.App()
			reads := base.(state.ScenarioCapturedReadStore)
			queues := base.(state.ScenarioQueueCaptureStore)
			scheduled := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
			taskID, _, err := queues.CaptureScenarioQueueMessage(ctx, app.ID, state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindDelayedTask, Slug: app.Slug,
				Schedule: "delay:3600", ScheduledAt: &scheduled, Message: json.RawMessage(`{"payload":{"invoice":7}}`)})
			if err != nil {
				t.Fatal(err)
			}
			invokeID, _, err := queues.CaptureScenarioQueueMessage(ctx, app.ID, state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindAsyncInvoke, Slug: app.Slug,
				Message: json.RawMessage(`{"invoke":{"method":"POST","path":"/sync"},"deadline":""}`)})
			if err != nil {
				t.Fatalf("async invoke capture: %v", err)
			}
			if _, _, err := queues.CaptureScenarioQueueMessage(ctx, app.ID, state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindAsyncInvoke, Slug: app.Slug,
				Schedule: "delay:1", Message: json.RawMessage(`{}`)}); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("async invoke with a schedule: %v", err)
			}
			task, err := reads.ScenarioCapturedQueueMessage(ctx, account.ID, state.ActionCaptureTestRun, taskID)
			if err != nil || task.Kind != api.ScenarioQueueKindDelayedTask || task.ScheduledAt == nil || !task.ScheduledAt.Equal(scheduled) ||
				task.Target != "workload:primary" || state.CompactEffectJSON(task.Message) != `{"payload":{"invoice":7}}` || task.CreatedAt.IsZero() {
				t.Fatalf("delayed task read = %+v %v", task, err)
			}
			if invoke, err := reads.ScenarioCapturedQueueMessage(ctx, account.ID, state.ActionCaptureTestRun, invokeID); err != nil || invoke.Kind != api.ScenarioQueueKindAsyncInvoke || invoke.ScheduledAt != nil {
				t.Fatalf("async invoke read = %+v %v", invoke, err)
			}
			for _, wrong := range []struct{ account, run, id string }{
				{account.ID, strings.Repeat("c", 32), taskID},
				{uuid.NewString(), state.ActionCaptureTestRun, taskID},
				{account.ID, state.ActionCaptureTestRun, uuid.NewString()},
				{account.ID, state.ActionCaptureTestRun, "not-a-uuid"},
			} {
				if _, err := reads.ScenarioCapturedQueueMessage(ctx, wrong.account, wrong.run, wrong.id); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("read outside the run %+v: %v", wrong, err)
				}
			}
			start, _, err := base.(state.ScenarioPlatformCaptureStore).CaptureScenarioPlatformCall(ctx, app.ID, state.ScenarioPlatformCallCapture{
				Kind: api.ScenarioPlatformKindWorkflowRun, Address: app.Slug, Name: "invoice", Message: json.RawMessage(`{"input":{"order":3}}`)})
			if err != nil {
				t.Fatal(err)
			}
			if call, err := reads.ScenarioCapturedPlatformCall(ctx, account.ID, state.ActionCaptureTestRun, start.ID); err != nil || call.Kind != api.ScenarioPlatformKindWorkflowRun ||
				call.Name != "invoice" || state.CompactEffectJSON(call.Message) != `{"input":{"order":3}}` {
				t.Fatalf("workflow run read = %+v %v", call, err)
			}
			if _, err := reads.ScenarioCapturedPlatformCall(ctx, account.ID, strings.Repeat("c", 32), start.ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("workflow run read from another run: %v", err)
			}
			event := state.ScenarioEventCapture{ID: "evt-1", AccountID: account.ID, Source: "orders", Type: "order.created", SchemaVersion: "", Data: json.RawMessage(`{"order":1}`)}
			if _, err := base.(state.ScenarioEventCaptureStore).CaptureScenarioEvent(ctx, app.ID, "", event); err != nil {
				t.Fatal(err)
			}
			if got, err := reads.ScenarioCapturedEvent(ctx, account.ID, state.ActionCaptureTestRun, "orders", "evt-1"); err != nil || got.Type != "order.created" || got.CreatedAt.IsZero() {
				t.Fatalf("event read = %+v %v", got, err)
			}
			if _, err := reads.ScenarioCapturedEvent(ctx, account.ID, state.ActionCaptureTestRun, "billing", "evt-1"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("event read under another source: %v", err)
			}
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || snapshot.Status != "complete" || len(snapshot.QueueMessages) != 2 {
				t.Fatalf("sealed evidence = %+v %v", snapshot, err)
			}
		})
	}
}
