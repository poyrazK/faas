package apidserver

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// debugRequestJournalSkew matches the request-ID journal RPC's accepted
// future clock skew between gateway and apid hosts.
const debugRequestJournalSkew = time.Minute

// resolveDebugRequestTraceIdentifier maps a public x-faas-request-id to the
// request's trace ID through the request-ID journal, so links built from
// app errors (which record the public request ID) open the request's
// telemetry and evidence. Telemetry row IDs, trace IDs and request IDs the
// journal does not hold pass through unchanged; the telemetry lookup stays
// app-scoped either way.
func (s *server) resolveDebugRequestTraceIdentifier(ctx context.Context, accountID, appID, identifier string, from, now time.Time) string {
	journalStore, ok := s.store.(state.RequestIDJournalReader)
	if !ok {
		return identifier
	}
	for _, candidate := range debugRequestIDCandidates(identifier) {
		journal, err := journalStore.FindRequestIDJournalByAppAndIdentifier(ctx, accountID, appID, candidate, from, now.Add(debugRequestJournalSkew), now)
		if err == nil && journal.TraceID != "" {
			return journal.TraceID
		}
	}
	return identifier
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
