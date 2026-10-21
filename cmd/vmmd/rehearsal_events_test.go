// adr: 20261020090000
package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

type eventCaptureStub struct {
	captured bool
	err      error
	app, run string
	event    state.ScenarioEventCapture
}

func (s *eventCaptureStub) CaptureScenarioEvent(_ context.Context, appID, installedRunID string, event state.ScenarioEventCapture) (bool, error) {
	s.app, s.run, s.event = appID, installedRunID, event
	return s.captured, s.err
}

func TestCaptureGuestEventFailsClosedForInstalledRuns(t *testing.T) {
	envelope := events.Envelope{Source: "orders", Type: "order.created", SchemaVersion: "1", Data: json.RawMessage(`{"order":1}`)}
	// Without capture storage an ordinary app publishes normally, but a
	// contained instance never does.
	if captured, err := captureGuestEvent(t.Context(), struct{}{}, "app", "", "account", envelope); captured || err != nil {
		t.Fatalf("ordinary app without capture storage: %v %v", captured, err)
	}
	if captured, err := captureGuestEvent(t.Context(), struct{}{}, "app", "run", "account", envelope); !captured || err == nil {
		t.Fatalf("contained app without capture storage: %v %v", captured, err)
	}
	// A retried publish of an event the run holds is a success, as a
	// duplicate production publish is.
	if captured, err := captureGuestEvent(t.Context(), &eventCaptureStub{captured: true, err: state.ErrScenarioEventDuplicate}, "app", "run", "account", envelope); !captured || err != nil {
		t.Fatalf("duplicate capture: %v %v", captured, err)
	}
	stub := &eventCaptureStub{captured: true, err: errors.New("sealed")}
	captured, err := captureGuestEvent(t.Context(), stub, "app", "run", "account", envelope)
	if !captured || err == nil || stub.app != "app" || stub.run != "run" || stub.event.AccountID != "account" || stub.event.Source != "orders" ||
		stub.event.Type != "order.created" || stub.event.SchemaVersion != "1" || string(stub.event.Data) != `{"order":1}` {
		t.Fatalf("capture passthrough: captured=%v err=%v stub=%+v", captured, err, stub)
	}
}
