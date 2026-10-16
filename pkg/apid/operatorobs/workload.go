package operatorobs

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const AlertWorkloadSummaryPath = "/v1/admin/obs/incidents/platform_alert/summary"

func AlertWorkloadSummary(ctx context.Context, reader state.OperatorAlertWorkloadReader, enabled bool, now time.Time) (api.ObsAlertWorkloadSummary, error) {
	out := api.ObsAlertWorkloadSummary{AsOf: now.UTC().Truncate(time.Microsecond), Scope: "retained_platform_alert_episodes", AgeBasis: "episode_started_at", Groups: []api.ObsAlertWorkloadGroup{}}
	if now.IsZero() {
		return out, state.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsAlertWorkloadBudget)
	defer cancel()
	snap, err := reader.OperatorAlertWorkload(ctx, state.OperatorAlertWorkloadFilter{Now: out.AsOf, CollectionEnabled: enabled})
	if err != nil {
		return out, err
	}
	out.Collection = collectionHealth(snap.Collection, enabled, out.AsOf)
	out.Retention = retentionHealth(snap.Retention, out.AsOf)
	if snap.Collection.LastSuccessAt.IsZero() {
		return out, nil
	}
	if len(snap.Buckets) > api.ObsAlertWorkloadGroupMax {
		return out, fmt.Errorf("invalid alert workload cardinality")
	}
	out.Counts = &api.ObsAlertWorkloadCounts{}
	for _, b := range snap.Buckets {
		if b.Count < 1 || ValidateReference("platform_alert", b.OldestID) != nil || b.OldestStartedAt.IsZero() || b.Severity != "critical" && b.Severity != "error" && b.Severity != "warning" {
			return out, fmt.Errorf("invalid alert workload bucket")
		}
		switch b.Status {
		case "firing":
			out.Counts.Firing += b.Count
		case "unknown":
			out.Counts.Unknown += b.Count
		case "resolved":
			out.Counts.Resolved += b.Count
		default:
			return out, fmt.Errorf("invalid alert workload status")
		}
		out.Counts.Total += b.Count
		out.Groups = append(out.Groups, api.ObsAlertWorkloadGroup{Status: b.Status, Severity: b.Severity, Acknowledged: b.Acknowledged, Owned: b.Owned, Snoozed: b.Snoozed, Count: b.Count})
		if b.Status == "resolved" || b.Snoozed {
			continue
		}
		if !b.Acknowledged || !b.Owned {
			out.Counts.NeedsAttention += b.Count
		}
		if !b.Acknowledged {
			out.OldestUnacknowledged = olderWorkload(out.OldestUnacknowledged, b, out.AsOf)
		}
		if !b.Owned {
			out.OldestUnowned = olderWorkload(out.OldestUnowned, b, out.AsOf)
		}
	}
	return out, nil
}

func olderWorkload(current *api.ObsAlertWorkloadOldest, b state.OperatorAlertWorkloadBucket, now time.Time) *api.ObsAlertWorkloadOldest {
	id := "platform_alert/" + b.OldestID
	if current != nil && (current.StartedAt.Before(b.OldestStartedAt) || current.StartedAt.Equal(b.OldestStartedAt) && current.IncidentID < id) {
		return current
	}
	age := int64(now.Sub(b.OldestStartedAt) / time.Second)
	if age < 0 {
		age = 0
	}
	return &api.ObsAlertWorkloadOldest{IncidentID: id, Status: b.Status, Severity: b.Severity, StartedAt: b.OldestStartedAt, AgeSeconds: age, EvidencePath: EvidencePath("platform_alert", b.OldestID)}
}
