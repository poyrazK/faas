// adr: 20261021090000
package state

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestScenarioQueueCaptureSealsIntoRunEvidence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			ctx := t.Context()
			capturer := f.base.(ScenarioQueueCaptureStore)
			ordinary, err := f.base.CreateApp(ctx, App{AccountID: f.account.ID, Slug: "ordinary-" + uuid.NewString()[:8], Type: AppTypeApp, Runtime: "node22", RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			send := ScenarioQueueCapture{Kind: api.ScenarioQueueKindQueue, Slug: f.app.Slug, QueueName: "imports", Message: json.RawMessage(`{"payload":{"order":1}}`)}
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, ordinary.ID, send); captured || err != nil {
				t.Fatalf("an ordinary app's send was captured: %v %v", captured, err)
			}
			missing := send
			missing.Slug = "missing-" + uuid.NewString()[:8]
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, f.app.ID, missing); !captured || !errors.Is(err, ErrNotFound) {
				t.Fatalf("send to an unknown app: %v %v", captured, err)
			}
			invalid := send
			invalid.Schedule = "delay:5"
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, f.app.ID, invalid); !captured || !errors.Is(err, ErrConflict) {
				t.Fatalf("queue send with a schedule: %v %v", captured, err)
			}
			for range 2 { // repeated sends stay separate intents
				if id, captured, err := capturer.CaptureScenarioQueueMessage(ctx, f.app.ID, send); !captured || err != nil || uuid.Validate(id) != nil {
					t.Fatalf("member send: %q %v %v", id, captured, err)
				}
			}
			task := ScenarioQueueCapture{Kind: api.ScenarioQueueKindDelayedTask, Slug: ordinary.Slug, Schedule: "delay:60", Message: json.RawMessage(`{"payload":{"invoice":7}}`)}
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, f.app.ID, task); !captured || err != nil {
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
			if got.Workload != "primary" || got.Kind != api.ScenarioQueueKindDelayedTask || got.Target != "app:"+ordinary.Slug || got.Schedule != "delay:60" || compactEffectJSON(got.Message) != `{"payload":{"invoice":7}}` {
				t.Fatalf("captured delayed task = %+v", got)
			}
			for _, got := range snapshot.QueueMessages[1:] {
				if got.Workload != "primary" || got.Kind != api.ScenarioQueueKindQueue || got.Target != "workload:primary" || got.QueueName != "imports" || got.Schedule != "" || compactEffectJSON(got.Message) != `{"payload":{"order":1}}` {
					t.Fatalf("captured queue send = %+v", got)
				}
			}
			if _, captured, err := capturer.CaptureScenarioQueueMessage(ctx, f.app.ID, send); !captured || !errors.Is(err, ErrScenarioActionsSealed) {
				t.Fatalf("late send after seal: %v %v", captured, err)
			}
		})
	}
}

func TestScenarioQueueCaptureOversizedMessageIsIncomplete(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			msg := ScenarioQueueCapture{Kind: api.ScenarioQueueKindDelayedTask, Slug: f.app.Slug, Oversized: true}
			if _, captured, err := f.base.(ScenarioQueueCaptureStore).CaptureScenarioQueueMessage(t.Context(), f.app.ID, msg); !captured || err != nil {
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
