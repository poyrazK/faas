package apidserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// debugRequestJournalSkew matches the request-ID journal RPC's accepted
// future clock skew between gateway and apid hosts.
const debugRequestJournalSkew = time.Minute

// findDebugRequestJournal resolves a public x-faas-request-id through the
// request-ID journal, trying each spelling debugRequestIDCandidates returns.
// A miss is (zero, false, nil); a read failure is returned.
func (s *server) findDebugRequestJournal(ctx context.Context, accountID, appID, identifier string, from, now time.Time) (state.RequestIDJournalEntry, bool, error) {
	journalStore, ok := s.store.(state.RequestIDJournalReader)
	if !ok {
		return state.RequestIDJournalEntry{}, false, nil
	}
	for _, candidate := range debugRequestIDCandidates(identifier) {
		journal, err := journalStore.FindRequestIDJournalByAppAndIdentifier(ctx, accountID, appID, candidate, from, now.Add(debugRequestJournalSkew), now)
		if err == nil {
			return journal, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return state.RequestIDJournalEntry{}, false, err
		}
	}
	return state.RequestIDJournalEntry{}, false, nil
}

// getDebugRequestTelemetry finds one request's telemetry for evidence. The
// identifier is matched as a telemetry row ID or trace ID first; only when
// that misses is it resolved as a public request ID through the journal, so
// row and trace IDs cost a single query. App errors record the public
// request ID, so their links open the request's evidence. The lookup stays
// app-scoped either way.
func (s *server) getDebugRequestTelemetry(ctx context.Context, accountID, appID, identifier string, from, until, now time.Time) (sqlc.GetRequestTelemetryByAppAndIdentifierRow, error) {
	params := sqlc.GetRequestTelemetryByAppAndIdentifierParams{
		AppID:         stringToPgUUID(appID),
		Identifier:    identifier,
		ReceivedFrom:  pgtype.Timestamptz{Time: from, Valid: true},
		ReceivedUntil: pgtype.Timestamptz{Time: until, Valid: true},
	}
	row, err := s.store.GetRequestTelemetryByAppAndIdentifier(ctx, params)
	if !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	journal, found, journalErr := s.findDebugRequestJournal(ctx, accountID, appID, identifier, from, now)
	if journalErr != nil {
		return row, journalErr
	}
	if !found || journal.TraceID == "" || journal.TraceID == identifier {
		return row, err
	}
	params.Identifier = journal.TraceID
	return s.store.GetRequestTelemetryByAppAndIdentifier(ctx, params)
}

// debugRequestIDCandidates returns the spellings of a request ID to look up.
// The gateway issues x-faas-request-id as 32 lowercase hex characters, while
// app errors store it in a uuid column and return the dashed form; a dashed
// UUID is therefore also tried compact.
func debugRequestIDCandidates(identifier string) []string {
	candidates := []string{identifier}
	if strings.Contains(identifier, "-") {
		if parsed, err := uuid.Parse(identifier); err == nil {
			candidates = append(candidates, strings.ReplaceAll(parsed.String(), "-", ""))
		}
	}
	return candidates
}
