package operatorobs

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

func TriageHistoryPath(id string) string {
	return "/v1/admin/obs/incidents/platform_alert/" + id + "/triage-history"
}

// TriageHistory keeps cursors on one immutable episode and revision upper bound.
// SnapshotAt limits cursor lifetime; later reads can observe audit retention.
func TriageHistory(ctx context.Context, store state.OperatorAlertTriageHistoryReader, id, rawCursor string, limit int, now time.Time) (api.ObsAlertTriageHistory, error) {
	f, at, err := triageHistoryFilter(id, rawCursor, limit, now)
	if err != nil {
		return api.ObsAlertTriageHistory{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsAlertTriageHistoryBudget)
	defer cancel()
	snapshot, err := store.OperatorAlertTriageHistory(ctx, f)
	if err != nil {
		return api.ObsAlertTriageHistory{}, err
	}
	return projectTriageHistory(snapshot, limit, at, now.UTC()), nil
}

func triageHistoryFilter(id, rawCursor string, limit int, now time.Time) (state.OperatorAlertTriageHistoryFilter, time.Time, error) {
	f := state.OperatorAlertTriageHistoryFilter{ID: id, Limit: limit}
	at := now.UTC().Truncate(time.Microsecond)
	if ValidateReference("platform_alert", id) != nil || limit < 1 || limit > api.ObsAlertTriageHistoryLimitMax || now.IsZero() || len(rawCursor) > api.ObsAlertTriageHistoryCursorMaxBytes {
		return f, at, state.ErrInvalidArgument
	}
	if rawCursor == "" {
		return f, at, nil
	}
	key, err := cursor.Decode(rawCursor)
	if err != nil {
		return f, at, state.ErrInvalidArgument
	}
	parts := strings.Split(key.ID, "|")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "triage-history/v1/") {
		return f, at, state.ErrInvalidArgument
	}
	episode := strings.TrimPrefix(parts[0], "triage-history/v1/")
	upper, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || strconv.FormatInt(upper, 10) != parts[1] || upper < 1 || upper > api.ObsAlertTriageRevisionMax {
		return f, at, state.ErrInvalidArgument
	}
	before, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || strconv.FormatInt(before, 10) != parts[2] || before < 1 || before > upper || ValidateReference("platform_alert", episode) != nil || key.CreatedAt.Before(now.Add(-api.ObsAlertTriageHistoryCursorTTL)) || key.CreatedAt.After(now.Add(api.ObsHealthClockSkewTolerance)) {
		return f, at, state.ErrInvalidArgument
	}
	f.ExpectedEpisodeID, f.SnapshotRevision, f.BeforeRevision = episode, &upper, before
	return f, key.CreatedAt, nil
}

func projectTriageHistory(s state.OperatorAlertTriageHistorySnapshot, limit int, snapshotAt, now time.Time) api.ObsAlertTriageHistory {
	out := api.ObsAlertTriageHistory{IncidentID: "platform_alert/" + s.Episode.ID, GeneratedAt: now, SnapshotAt: snapshotAt, SnapshotRevision: s.SnapshotRevision, CurrentRevision: s.Episode.Triage.Revision, Items: s.Items, Limit: limit, Truncated: s.Truncated, Coverage: s.Coverage, HistoryPath: TriageHistoryPath(s.Episode.ID)}
	for i := range out.Items {
		if tracePattern.MatchString(out.Items[i].TraceID) {
			out.Items[i].TracePath = "/v1/admin/obs/traces/" + out.Items[i].TraceID
		} else {
			out.Items[i].TraceID = ""
		}
	}
	if out.Truncated && len(out.Items) > 0 {
		last := out.Items[len(out.Items)-1]
		out.NextCursor = cursor.Encode(cursor.Key{CreatedAt: snapshotAt, ID: "triage-history/v1/" + s.Episode.ID + "|" + strconv.FormatInt(s.SnapshotRevision, 10) + "|" + strconv.FormatInt(last.Revision, 10)})
	}
	return out
}

// Load retained evidence and human history in one snapshot. The old narrow
// reader remains supported for compatibility adapters, with explicit missing coverage.
func retainedAlertSnapshot(ctx context.Context, store Store, id string, out *api.ObsIncidentEvidenceResponse) (state.OperatorAlertEpisode, state.OperatorAlertCollection, error) {
	if reader, ok := store.(state.OperatorAlertTriageHistoryReader); ok {
		ctx, cancel := context.WithTimeout(ctx, api.ObsAlertTriageHistoryBudget)
		defer cancel()
		snapshot, err := reader.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: id, Limit: out.Limit})
		if err == nil {
			history := projectTriageHistory(snapshot, out.Limit, out.GeneratedAt, out.GeneratedAt)
			out.TriageHistory = &history
		}
		return snapshot.Episode, snapshot.Collection, err
	}
	out.UnavailableSources = append(out.UnavailableSources, "platform_alert_triage_history")
	return store.(state.OperatorAlertHistoryReader).OperatorAlertEpisode(ctx, id)
}
