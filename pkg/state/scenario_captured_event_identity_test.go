// adr: 20261021120000
package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioEventCaptureDeduplicatesEventIdentity(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			capturer := (*f.Base()).(state.ScenarioEventCaptureStore)
			app := f.App()
			event := state.ScenarioEventCapture{ID: "evt-1", AccountID: f.Account().ID, Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order":1}`)}
			if captured, err := capturer.CaptureScenarioEvent(t.Context(), app.ID, "", event); !captured || err != nil {
				t.Fatalf("first publish: %v %v", captured, err)
			}
			retry := event
			retry.Data = json.RawMessage(`{ "order" : 1.0 }`) // the same JSON value
			if captured, err := capturer.CaptureScenarioEvent(t.Context(), app.ID, "", retry); !captured || !errors.Is(err, state.ErrScenarioEventDuplicate) {
				t.Fatalf("retried publish: %v %v", captured, err)
			}
			changed := event
			changed.Data = json.RawMessage(`{"order":2}`)
			if captured, err := capturer.CaptureScenarioEvent(t.Context(), app.ID, "", changed); !captured || !errors.Is(err, state.ErrScenarioEventIdentityConflict) || !errors.Is(err, state.ErrConflict) {
				t.Fatalf("conflicting publish: %v %v", captured, err)
			}
			other := event
			other.Source = "billing" // identity is per source
			if captured, err := capturer.CaptureScenarioEvent(t.Context(), app.ID, "", other); !captured || err != nil {
				t.Fatalf("same id from another source: %v %v", captured, err)
			}
			snapshot := sealTestActions(t, f)
			if snapshot.Status != "complete" || len(snapshot.PublishedEvents) != 2 {
				t.Fatalf("sealed events = %+v (%s)", snapshot.PublishedEvents, snapshot.Status)
			}
		})
	}
}
