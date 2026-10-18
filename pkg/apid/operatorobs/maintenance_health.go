package operatorobs

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type MaintenanceHealthStore interface {
	OperatorAlertCollectionStatus(context.Context) (state.OperatorAlertCollection, error)
	OperatorAlertRetentionStatus(context.Context, time.Time) (state.OperatorAlertRetention, error)
}

func MaintenanceHealth(ctx context.Context, store MaintenanceHealthStore, enabled bool, now time.Time) (api.ObsAlertCollectionHealth, api.ObsAlertRetentionHealth, error) {
	now = now.UTC().Truncate(time.Microsecond)
	ctx, cancel := context.WithTimeout(ctx, api.ObsAlertMaintenanceReadBudget)
	defer cancel()
	collection, err := store.OperatorAlertCollectionStatus(ctx)
	if err != nil {
		return api.ObsAlertCollectionHealth{}, api.ObsAlertRetentionHealth{}, fmt.Errorf("collection health: %w", err)
	}
	retention, err := store.OperatorAlertRetentionStatus(ctx, now)
	if err != nil {
		return api.ObsAlertCollectionHealth{}, api.ObsAlertRetentionHealth{}, fmt.Errorf("retention health: %w", err)
	}
	return collectionHealth(collection, enabled, now), retentionHealth(retention, now), nil
}

func collectionHealth(c state.OperatorAlertCollection, enabled bool, now time.Time) api.ObsAlertCollectionHealth {
	out := c.Health(now)
	out.DiagnosticsPath = RuleHealthPath
	switch {
	case !enabled:
		out.CollectionState, out.State = "disabled", "unknown"
		if out.Reason == "" {
			out.Reason = "not_configured"
		}
	case c.LastAttemptAt.After(now.Add(api.ObsHealthClockSkewTolerance)):
		out.CollectionState = "unknown"
	case c.LastAttemptAt.After(c.LastSuccessAt):
		out.CollectionState = "failed"
	case out.Reason == "collection_failed":
		out.CollectionState = "failed"
	case out.Reason == "not_collected":
		out.CollectionState = "never_collected"
	case out.Reason == "observation_stale":
		out.CollectionState = "stale"
	case out.State == "current":
		out.CollectionState = "current"
	default:
		out.CollectionState = "unknown"
	}
	return out
}

func retentionHealth(r state.OperatorAlertRetention, now time.Time) api.ObsAlertRetentionHealth {
	out := api.ObsAlertRetentionHealth{
		State: "current", AsOf: now.UTC(), Cutoff: now.Add(-api.ObsAlertHistoryRetention).UTC(),
		LastAttemptAt: maintenanceTime(r.LastAttemptAt), LastSuccessAt: maintenanceTime(r.LastSuccessAt),
		ExpiredEpisodes: r.ExpiredEpisodes, OldestExpiredAt: maintenanceTime(r.OldestExpiredAt),
	}
	if !r.LastSuccessAt.IsZero() {
		n := r.LastPrunedEpisodes
		out.LastPrunedEpisodes = &n
	}
	switch {
	case r.LastAttemptAt.After(now.Add(api.ObsHealthClockSkewTolerance)):
		out.State, out.Reason = "unknown", "observation_in_future"
	case r.LastAttemptAt.After(r.LastSuccessAt):
		out.State, out.Reason = "failed", "cleanup_failed"
	case r.LastSuccessAt.IsZero():
		out.State, out.Reason = "never_run", "not_cleaned"
	case r.LastSuccessAt.Before(now.Add(-api.ObsAlertRetentionFreshnessMaxAge)):
		out.State, out.Reason = "stale", "observation_stale"
	}
	return out
}

func maintenanceTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	at = at.UTC()
	return &at
}
