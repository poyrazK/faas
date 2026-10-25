// adr: 20261020090000
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// normalizeGuestEvent decodes and validates one in-guest publish exactly as an
// ordinary publish does, so a rehearsal captures only events production would
// have accepted.
func normalizeGuestEvent(ctx context.Context, store any, accountID string, body []byte, now time.Time) (events.Envelope, error) {
	var req api.PublishEventRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return events.Envelope{}, fmt.Errorf("decode in-guest event publish: %w", err)
	}
	registry, _ := store.(state.EventSchemaStore)
	envelope, err := events.NormalizeCapturedPublish(ctx, registry, accountID, req, now)
	if err != nil {
		return events.Envelope{}, fmt.Errorf("validate in-guest event publish: %w", err)
	}
	return envelope, nil
}

// captureGuestEvent redirects an enrolled app's event into its run's evidence.
// captured is false only when the app has no rehearsal policy; an installed
// (contained) run that cannot be confirmed is never published. A repeated
// publish of an event the run already holds succeeds without a second event.
func captureGuestEvent(ctx context.Context, store any, appID, installedRunID, accountID string, envelope events.Envelope) (bool, error) {
	capturer, ok := store.(state.ScenarioEventCaptureStore)
	if !ok {
		if installedRunID != "" {
			return true, errors.New("rehearsal event capture storage is unavailable")
		}
		return false, nil
	}
	event := events.ScenarioCapture(envelope)
	event.AccountID = accountID
	captured, err := capturer.CaptureScenarioEvent(ctx, appID, installedRunID, event)
	if errors.Is(err, state.ErrScenarioEventDuplicate) {
		err = nil
	}
	return captured, err
}
