package apidserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
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
	if journal, found := s.findNodeRequestJournal(ctx, accountID, appID, identifier, from, now); found {
		return journal, true, nil
	}
	return state.RequestIDJournalEntry{}, false, nil
}

// findNodeRequestJournal asks the gateways that keep the journal on their node
// (ADR-20261101085552). A platform-minted ID carries its minting time, so the
// first ask covers that minute; a miss there, or an ID without the time, asks
// across the whole window. A gateway that does not answer is logged rather
// than failing the lookup: during the switch-over the record may still be in
// the control-plane table, which was read first.
func (s *server) findNodeRequestJournal(ctx context.Context, accountID, appID, identifier string, from, now time.Time) (state.RequestIDJournalEntry, bool) {
	if s.requestIDLookups == nil || s.requestIDLookups.Gateways() == 0 {
		return state.RequestIDJournalEntry{}, false
	}
	until := now.Add(debugRequestJournalSkew)
	for _, candidate := range debugRequestIDCandidates(identifier) {
		windows := [][2]time.Time{{from, until}}
		if minted, ok := wire.RequestIDMintedAt(candidate); ok && !minted.Before(from) && minted.Before(until) {
			windows = [][2]time.Time{{maxTime(from, minted.Add(-time.Minute)), minTime(until, minted.Add(time.Minute))}, {from, until}}
		}
		for _, window := range windows {
			result := s.requestIDLookups.Lookup(ctx, accountID, appID, candidate, window[0], window[1], api.RequestJournalNodeLookupTimeout)
			if result.Found {
				return state.RequestIDJournalEntry{AccountID: accountID, AppID: appID, RequestID: candidate,
					TraceID: result.TraceID, ReceivedAt: result.ReceivedAt, ExpiresAt: result.ExpiresAt}, true
			}
			if result.Incomplete && s.log != nil {
				s.log.Warn("request ID lookup: a gateway did not answer; the request may be unresolvable", "app_id", appID, "asked", result.Asked)
			}
		}
	}
	return state.RequestIDJournalEntry{}, false
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
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
