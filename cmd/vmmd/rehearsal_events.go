// adr: 20261020090000
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	occurredAt := time.Time{}
	if req.Time != nil {
		occurredAt = *req.Time
	}
	envelope, err := (events.Envelope{
		ID: req.ID, Source: req.Source, Type: req.Type,
		Time: occurredAt, DataContentType: req.DataContentType,
		Data: req.Data, AccountID: req.AccountID, SchemaVersion: req.SchemaVersion,
	}).Normalize(accountID, now)
	if err != nil {
		return events.Envelope{}, fmt.Errorf("validate in-guest event publish: %w", err)
	}
	if strings.HasPrefix(envelope.Source, "gregale.") {
		return events.Envelope{}, errors.New("gregale.* event sources are reserved for platform events")
	}
	registry, ok := store.(state.EventSchemaStore)
	if !ok {
		return events.Envelope{}, errors.New("event schema registry is unavailable")
	}
	if err := state.ValidatePublishedEventSchema(ctx, registry, accountID, envelope.Source, envelope.Type, envelope.SchemaVersion, envelope.Data); err != nil {
		return events.Envelope{}, fmt.Errorf("validate in-guest event schema: %w", err)
	}
	return envelope, nil
}

// captureGuestEvent redirects an enrolled app's event into its run's evidence.
// captured is false only when the app has no rehearsal policy; an installed
// (contained) run that cannot be confirmed is never published.
func captureGuestEvent(ctx context.Context, store any, appID, installedRunID, accountID string, envelope events.Envelope) (bool, error) {
	capturer, ok := store.(state.ScenarioEventCaptureStore)
	if !ok {
		if installedRunID != "" {
			return true, errors.New("rehearsal event capture storage is unavailable")
		}
		return false, nil
	}
	return capturer.CaptureScenarioEvent(ctx, appID, installedRunID, state.ScenarioEventCapture{AccountID: accountID,
		Source: envelope.Source, Type: envelope.Type, SchemaVersion: envelope.SchemaVersion, Data: envelope.Data})
}
