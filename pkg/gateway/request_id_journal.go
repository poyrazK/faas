package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RequestIDJournalRecord is the minimum data required to resolve a public
// request ID after the sampled request-telemetry row has been discarded.
// Deliberately excludes URL, payload, headers, and response data.
type RequestIDJournalRecord struct {
	ID         string
	AccountID  string
	AppID      string
	RequestID  string
	TraceID    string
	ReceivedAt time.Time
}

// RequestIDJournalWriter persists an exact public-ID mapping. Production
// installs RequestIDJournalQueue.Submit over an apid-backed writer, so the
// handler never waits on apid (ADR-634); tests and local handlers may omit it.
type RequestIDJournalWriter func(context.Context, RequestIDJournalRecord) error

func (h *Handler) recordRequestIDJournal(ctx context.Context, app App, requestID string, receivedAt time.Time) error {
	if h.requestIDJournal == nil {
		return nil
	}
	if len(requestID) == 0 || len(requestID) > 128 || strings.IndexFunc(requestID, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("gateway: invalid public request id")
	}
	accountID, err := uuid.Parse(app.AccountID)
	if err != nil {
		return fmt.Errorf("gateway: invalid request journal account id: %w", err)
	}
	appID, err := uuid.Parse(app.ID)
	if err != nil {
		return fmt.Errorf("gateway: invalid request journal app id: %w", err)
	}
	err = h.requestIDJournal(ctx, RequestIDJournalRecord{
		ID:         uuid.NewString(),
		AccountID:  accountID.String(),
		AppID:      appID.String(),
		RequestID:  requestID,
		TraceID:    traceIDForTelemetry(ctx),
		ReceivedAt: receivedAt.UTC(),
	})
	return err
}
