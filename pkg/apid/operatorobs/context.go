package operatorobs

import (
	"context"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

// Context resolves verified current relationships independently of live
// Prometheus. Cursors bind the canonical incident and a fixed history window.
func Context(ctx context.Context, store state.OperatorIncidentContextReader, kind, id, rawCursor string, limit, historyLimit int, now time.Time) (api.ObsIncidentContextResponse, error) {
	if ValidateReference(kind, id) != nil {
		return api.ObsIncidentContextResponse{}, state.ErrInvalidArgument
	}
	now = now.UTC().Truncate(time.Microsecond)
	f := state.OperatorIncidentContextFilter{Kind: kind, ID: id, Now: now, Until: now, Limit: limit, HistoryLimit: historyLimit}
	if rawCursor != "" {
		key, err := cursor.Decode(rawCursor)
		if err != nil {
			return api.ObsIncidentContextResponse{}, state.ErrInvalidArgument
		}
		incident, before, ok := strings.Cut(key.ID, "|")
		cursorKind, cursorID, valid := strings.Cut(incident, "/")
		if !ok || before == "" || !valid || cursorKind != kind || ValidateReference(cursorKind, cursorID) != nil {
			return api.ObsIncidentContextResponse{}, state.ErrInvalidArgument
		}
		f.ExpectedIncidentID, f.Before, f.Until = incident, before, key.CreatedAt
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsIncidentContextBudget)
	defer cancel()
	out, err := store.OperatorIncidentContext(ctx, f)
	if err != nil {
		return out, err
	}
	resolvedKind, resolvedID, _ := strings.Cut(out.IncidentID, "/")
	out.EvidencePath = EvidencePath(resolvedKind, resolvedID)
	for i := range out.Timeline {
		entry := &out.Timeline[i].Entry
		if tracePattern.MatchString(entry.TraceID) {
			entry.TracePath = "/v1/admin/obs/traces/" + entry.TraceID
		} else {
			entry.TraceID = ""
		}
	}
	return out, nil
}
