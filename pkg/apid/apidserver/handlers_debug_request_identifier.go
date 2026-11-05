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

// findStoredRequestJournal reads the control-plane request-ID journal,
// trying each spelling debugRequestIDCandidates returns.
func (s *server) findStoredRequestJournal(ctx context.Context, accountID, appID, identifier string, from, now time.Time) (state.RequestIDJournalEntry, bool, error) {
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

// debugRequestResolution is one request identifier resolved for the
// debugger's get, evidence and replay.
type debugRequestResolution struct {
	row   sqlc.GetRequestTelemetryByAppAndIdentifierRow
	found bool // row holds the request's telemetry
	// journal is set when the identifier is a public request ID the
	// request-ID journal knows, even when its telemetry row was not kept.
	journal   state.RequestIDJournalEntry
	journaled bool
}

// resolveDebugRequest finds one request's telemetry. A public request ID
// names exactly one request, so the control-plane journal is read first:
// among the rows of its trace, the request is the one that started nearest
// its journaled start (several requests can share a trace). Otherwise the
// identifier is matched as a telemetry row ID or trace ID, one query; only
// when that misses are the gateways that keep the journal on their node
// asked (ADR-20261101085552), since that fan-out can take a second. App
// errors record the public request ID, so their links open the request's
// evidence. The lookup stays app-scoped either way.
func (s *server) resolveDebugRequest(ctx context.Context, accountID, appID, identifier string, from, until, now time.Time) (debugRequestResolution, error) {
	journal, journaled, err := s.findStoredRequestJournal(ctx, accountID, appID, identifier, from, now)
	if err != nil {
		return debugRequestResolution{}, err
	}
	if !journaled {
		row, err := s.store.GetRequestTelemetryByAppAndIdentifier(ctx, sqlc.GetRequestTelemetryByAppAndIdentifierParams{
			AppID: stringToPgUUID(appID), Identifier: identifier,
			ReceivedFrom: pgtype.Timestamptz{Time: from, Valid: true}, ReceivedUntil: pgtype.Timestamptz{Time: until, Valid: true},
		})
		if err == nil {
			return debugRequestResolution{row: row, found: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return debugRequestResolution{}, err
		}
		if journal, journaled = s.findNodeRequestJournal(ctx, accountID, appID, identifier, from, now); !journaled {
			return debugRequestResolution{}, nil
		}
	}
	res := debugRequestResolution{journal: journal, journaled: true}
	if journal.TraceID == "" {
		return res, nil
	}
	row, err := s.requestTelemetryForJournal(ctx, appID, journal, from, until)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, nil
	}
	if err != nil {
		return debugRequestResolution{}, err
	}
	res.row, res.found = row, true
	return res, nil
}

// requestTelemetryForJournal reads the journaled request's row: the row of
// its trace that started nearest the journaled start, or the trace's newest
// row on a store that cannot order by start.
func (s *server) requestTelemetryForJournal(ctx context.Context, appID string, journal state.RequestIDJournalEntry, from, until time.Time) (sqlc.GetRequestTelemetryByAppAndIdentifierRow, error) {
	received := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	if resolver, ok := s.store.(state.RequestTelemetryTraceResolver); ok {
		row, err := resolver.GetRequestTelemetryByAppTraceNearest(ctx, sqlc.GetRequestTelemetryByAppTraceNearestParams{
			AppID: stringToPgUUID(appID), TraceID: journal.TraceID,
			ReceivedFrom: received(from), ReceivedUntil: received(until), StartedAt: received(journal.ReceivedAt),
		})
		return sqlc.GetRequestTelemetryByAppAndIdentifierRow(row), err
	}
	return s.store.GetRequestTelemetryByAppAndIdentifier(ctx, sqlc.GetRequestTelemetryByAppAndIdentifierParams{
		AppID: stringToPgUUID(appID), Identifier: journal.TraceID, ReceivedFrom: received(from), ReceivedUntil: received(until),
	})
}

// getDebugRequestTelemetry is resolveDebugRequest for callers that need the
// row itself: a request without one reads as pgx.ErrNoRows.
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

func (s *server) getDebugRequestTelemetry(ctx context.Context, accountID, appID, identifier string, from, until, now time.Time) (sqlc.GetRequestTelemetryByAppAndIdentifierRow, error) {
	res, err := s.resolveDebugRequest(ctx, accountID, appID, identifier, from, until, now)
	if err != nil {
		return sqlc.GetRequestTelemetryByAppAndIdentifierRow{}, err
	}
	if !res.found {
		return sqlc.GetRequestTelemetryByAppAndIdentifierRow{}, pgx.ErrNoRows
	}
	return res.row, nil
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
