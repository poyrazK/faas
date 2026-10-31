// adr: 20261021120000
package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	// ErrPublishAccountMismatch is the 403 an ordinary publish returns for an
	// account_id other than the caller's.
	ErrPublishAccountMismatch = errors.New("account_id must match the authenticated account")
	// ErrPublishInvalid wraps the 400 an ordinary publish returns for an
	// invalid envelope or a reserved source.
	ErrPublishInvalid = errors.New("invalid event publish")
)

// NormalizeCapturedPublish validates one publish exactly as an ordinary
// publish does, so a rehearsal captures only events production would have
// accepted. Schema failures keep the state.ErrEventSchema* and
// state.ErrEventDataInvalid sentinels; any other error is a lookup failure.
func NormalizeCapturedPublish(ctx context.Context, registry state.EventSchemaStore, accountID string, req api.PublishEventRequest, now time.Time) (Envelope, error) {
	if req.AccountID != "" && req.AccountID != accountID {
		return Envelope{}, ErrPublishAccountMismatch
	}
	occurredAt := time.Time{}
	if req.Time != nil {
		occurredAt = *req.Time
	}
	envelope, err := (Envelope{
		ID: req.ID, Source: req.Source, Type: req.Type,
		Time: occurredAt, DataContentType: req.DataContentType,
		Data: req.Data, AccountID: req.AccountID, SchemaVersion: req.SchemaVersion,
	}).Normalize(accountID, now)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrPublishInvalid, err)
	}
	if strings.HasPrefix(envelope.Source, "gregale.") {
		return Envelope{}, fmt.Errorf("%w: gregale.* sources are reserved for platform events", ErrPublishInvalid)
	}
	if registry == nil {
		return Envelope{}, errors.New("event schema registry is unavailable")
	}
	if err := state.ValidatePublishedEventSchema(ctx, registry, accountID, envelope.Source, envelope.Type, envelope.SchemaVersion, envelope.Data); err != nil {
		return Envelope{}, fmt.Errorf("validate event schema: %w", err)
	}
	return envelope, nil
}

// ScenarioCapture is the capture record of a normalized envelope.
func ScenarioCapture(e Envelope) state.ScenarioEventCapture {
	return state.ScenarioEventCapture{ID: e.ID, AccountID: e.AccountID, Source: e.Source, Type: e.Type, SchemaVersion: e.SchemaVersion, Data: e.Data}
}
