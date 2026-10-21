// adr: 20261020090000
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioEventCaptureSealsIntoRunEvidence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			base, account, app := *f.Base(), f.Account(), f.App()
			capturer := base.(state.ScenarioEventCaptureStore)
			event := state.ScenarioEventCapture{AccountID: account.ID, Source: "orders", Type: "order.created", SchemaVersion: "1", Data: json.RawMessage(`{"order":1}`)}
			ordinary, err := base.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "ordinary-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			if captured, err := capturer.CaptureScenarioEvent(ctx, ordinary.ID, "", event); captured || err != nil {
				t.Fatalf("an ordinary app's event was captured: %v %v", captured, err)
			}
			// A contained instance never publishes without its durable policy,
			// nor into a run other than the one installed on it.
			if captured, err := capturer.CaptureScenarioEvent(ctx, ordinary.ID, state.ActionCaptureTestRun, event); !captured || !errors.Is(err, state.ErrConflict) {
				t.Fatalf("contained publish without policy: %v %v", captured, err)
			}
			if captured, err := capturer.CaptureScenarioEvent(ctx, app.ID, strings.Repeat("c", 32), event); !captured || !errors.Is(err, state.ErrConflict) {
				t.Fatalf("contained publish into another run: %v %v", captured, err)
			}
			for range 2 { // repeated publishes without an envelope ID stay separate intents
				if captured, err := capturer.CaptureScenarioEvent(ctx, app.ID, "", event); !captured || err != nil {
					t.Fatalf("capture-only publish: %v %v", captured, err)
				}
			}
			for range 2 {
				row, err := base.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{AppID: app.ID, AccountID: account.ID, WebhookID: f.AppHook().ID, Event: state.AppWebhookEventDeploymentLive, Payload: json.RawMessage(`{"deployment":"generated"}`)})
				if err != nil || row.LastError != state.ScenarioWebhookSuppressed {
					t.Fatalf("ordinary event not suppressed: %+v %v", row, err)
				}
			}
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || snapshot.Status != "complete" || snapshot.EventCaptureVersion != api.ScenarioEventCaptureVersion {
				t.Fatalf("invalid sealed event evidence: %+v %v", snapshot, err)
			}
			if len(snapshot.PublishedEvents) != 2 {
				t.Fatalf("published events = %+v, want 2", snapshot.PublishedEvents)
			}
			for _, got := range snapshot.PublishedEvents {
				if got.Workload != "primary" || got.Source != "orders" || got.Type != "order.created" || got.SchemaVersion != "1" || state.CompactEffectJSON(got.Data) != `{"order":1}` {
					t.Fatalf("captured event = %+v", got)
				}
			}
			want := []api.ScenarioSuppressedWebhookEvents{{Workload: "primary", Event: string(state.AppWebhookEventDeploymentLive), Count: 2}}
			if len(snapshot.SuppressedWebhookEvents) != 1 || snapshot.SuppressedWebhookEvents[0] != want[0] {
				t.Fatalf("suppressed webhook events = %+v, want %+v", snapshot.SuppressedWebhookEvents, want)
			}
			if captured, err := capturer.CaptureScenarioEvent(ctx, app.ID, "", event); !captured || !errors.Is(err, state.ErrScenarioActionsSealed) {
				t.Fatalf("late publish after seal: %v %v", captured, err)
			}
		})
	}
}

func TestScenarioEventCaptureOversizedDataIsIncomplete(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			large := json.RawMessage(`{"blob":"` + strings.Repeat("x", api.MaxScenarioCapturedEventDataBytes) + `"}`)
			event := state.ScenarioEventCapture{AccountID: f.Account().ID, Source: "orders", Type: "order.created", Data: large}
			if captured, err := (*f.Base()).(state.ScenarioEventCaptureStore).CaptureScenarioEvent(t.Context(), f.App().ID, "", event); !captured || err != nil {
				t.Fatalf("oversized publish: %v %v", captured, err)
			}
			snapshot := sealTestActions(t, f)
			if snapshot.Status != "incomplete" || snapshot.IncompleteCapturedEvents != 1 || len(snapshot.PublishedEvents) != 0 {
				t.Fatalf("oversized event was presented as complete: %+v", snapshot)
			}
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err == nil {
				t.Fatal("incomplete event evidence validated")
			}
		})
	}
}
