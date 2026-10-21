// adr: 20261021090000
package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioQueueCaptureSealsIntoRunEvidence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			base, app := *f.Base(), f.App()
			capturer := base.(state.ScenarioQueueCaptureStore)
			ordinary, err := base.CreateApp(ctx, state.App{AccountID: f.Account().ID, Slug: "ordinary-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			send := state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindQueue, Slug: app.Slug, QueueName: "imports", Message: json.RawMessage(`{"payload":{"order":1}}`)}
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, ordinary.ID, send); captured || err != nil {
				t.Fatalf("an ordinary app's send was captured: %v %v", captured, err)
			}
			missing := send
			missing.Slug = "missing-" + uuid.NewString()[:8]
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, app.ID, missing); !captured || !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("send to an unknown app: %v %v", captured, err)
			}
			invalid := send
			invalid.Schedule = "delay:5"
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, app.ID, invalid); !captured || !errors.Is(err, state.ErrConflict) {
				t.Fatalf("queue send with a schedule: %v %v", captured, err)
			}
			for range 2 { // repeated sends stay separate intents
				if id, captured, err := capturer.CaptureScenarioQueueMessage(ctx, app.ID, send); !captured || err != nil || uuid.Validate(id) != nil {
					t.Fatalf("member send: %q %v %v", id, captured, err)
				}
			}
			task := state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindDelayedTask, Slug: ordinary.Slug, Schedule: "delay:60", Message: json.RawMessage(`{"payload":{"invoice":7}}`)}
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, app.ID, task); !captured || err != nil {
				t.Fatalf("delayed task to an account app: %v %v", captured, err)
			}
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || snapshot.Status != "complete" || snapshot.QueueCaptureVersion != api.ScenarioQueueCaptureVersion {
				t.Fatalf("invalid sealed queue evidence: %+v %v", snapshot, err)
			}
			if len(snapshot.QueueMessages) != 3 {
				t.Fatalf("queue messages = %+v, want 3", snapshot.QueueMessages)
			}
			// Sorted by workload, kind, target: the delayed task comes first.
			got := snapshot.QueueMessages[0]
			if got.Workload != "primary" || got.Kind != api.ScenarioQueueKindDelayedTask || got.Target != "app:"+ordinary.Slug || got.Schedule != "delay:60" || state.CompactEffectJSON(got.Message) != `{"payload":{"invoice":7}}` {
				t.Fatalf("captured delayed task = %+v", got)
			}
			for _, got := range snapshot.QueueMessages[1:] {
				if got.Workload != "primary" || got.Kind != api.ScenarioQueueKindQueue || got.Target != "workload:primary" || got.QueueName != "imports" || got.Schedule != "" || state.CompactEffectJSON(got.Message) != `{"payload":{"order":1}}` {
					t.Fatalf("captured queue send = %+v", got)
				}
			}
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, app.ID, send); !captured || !errors.Is(err, state.ErrScenarioActionsSealed) {
				t.Fatalf("late send after seal: %v %v", captured, err)
			}
		})
	}
}

func TestScenarioQueueCaptureOversizedMessageIsIncomplete(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			msg := state.ScenarioQueueCapture{Kind: api.ScenarioQueueKindDelayedTask, Slug: f.App().Slug, Oversized: true}
			if _, captured, err := (*f.Base()).(state.ScenarioQueueCaptureStore).CaptureScenarioQueueMessage(t.Context(), f.App().ID, msg); !captured || err != nil {
				t.Fatalf("oversized send: %v %v", captured, err)
			}
			snapshot := sealTestActions(t, f)
			if snapshot.Status != "incomplete" || snapshot.IncompleteQueueMessages != 1 || len(snapshot.QueueMessages) != 0 {
				t.Fatalf("oversized message was presented as complete: %+v", snapshot)
			}
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err == nil {
				t.Fatal("incomplete queue evidence validated")
			}
		})
	}
}
