package operatorobs

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// AlertIncidents uses retained episodes after the first successful collection.
// Before that, preserve the live-only compatibility view without writing on GET.
func AlertIncidents(ctx context.Context, store state.OperatorAlertHistoryReader, client *promql.Client, f state.OperatorAlertFilter) ([]api.ObsIncident, []string, error) {
	page, err := store.OperatorAlertEpisodes(ctx, f)
	if err != nil {
		return nil, nil, err
	}
	missing := []string{}
	if page.Collection.LastSuccessAt.IsZero() {
		if f.Acknowledgment != "" || f.Ownership != "" || f.Snooze != "" {
			return []api.ObsIncident{}, []string{"platform_alert_history"}, nil
		}
		live, unavailable := FiringAlerts(ctx, client)
		unavailable = append(unavailable, "platform_alert_history")
		out := []api.ObsIncident{}
		for _, item := range live {
			if (f.Status == "" || f.Status == "unknown") && (f.Severity == "" || item.Severity == f.Severity) {
				item.Status = "unknown"
				out = append(out, item)
			}
		}
		return out, unavailable, nil
	}
	unknown := page.Collection.Health(f.Now).State != "current"
	out := make([]api.ObsIncident, 0, len(page.Episodes))
	for _, episode := range page.Episodes {
		item := retainedIncident(episode, page.Collection, f.Now)
		out = append(out, item)
		unknown = unknown || item.Status == "unknown"
	}
	if unknown {
		missing = append(missing, "platform_alert_history")
	}
	return out, missing, nil
}

func retainedIncident(e state.OperatorAlertEpisode, c state.OperatorAlertCollection, now time.Time) api.ObsIncident {
	labels := map[string]string{"alertname": e.Name, "component": e.Component, "severity": e.Severity, "account_id": e.AccountID, "app_id": e.AppID, "node_id": e.NodeID}
	item := projectAlert(promql.VectorSample{Labels: labels, Value: float64(e.StartedAt.UnixMilli()) / 1000})
	item.ID = "platform_alert/" + e.ID
	triage := e.Triage.Projection(now)
	item.Triage = &triage
	item.DedupeKey = "platform_alert/" + e.SeriesID
	item.Status = e.Status(c, now)
	item.ObservedAt = e.ObservedAt()
	if book := safeRunbookURL(e.RunbookURL); book != "" {
		item.RunbookURL = book
	}
	item.EvidencePath = EvidencePath("platform_alert", e.ID)
	observation := "current"
	if item.Status == "unknown" {
		observation = "unknown"
	}
	if item.Status == "resolved" {
		observation = "historical"
	}
	item.AlertEpisode = &api.ObsAlertEpisode{SeriesID: e.SeriesID, StartedAt: e.StartedAt, FirstSeenAt: e.FirstSeenAt, LastSeenAt: e.LastSeenAt, LastEvaluatedAt: e.LastEvaluatedAt, ResolvedAt: e.ResolvedAt, ObservationState: observation}
	return item
}

func retainedAlertEvidence(ctx context.Context, store Store, id string, out api.ObsIncidentEvidenceResponse) (api.ObsIncidentEvidenceResponse, bool, error) {
	_, ok := store.(state.OperatorAlertHistoryReader)
	if !ok {
		return out, false, nil
	}
	e, collection, err := retainedAlertSnapshot(ctx, store, id, &out)
	if errors.Is(err, state.ErrNotFound) {
		return out, !collection.LastSuccessAt.IsZero(), err
	}
	if err != nil {
		return out, true, err
	}
	out.Incident = retainedIncident(e, collection, out.GeneratedAt)
	out.Since = e.StartedAt
	if out.Incident.Status == "unknown" {
		out.UnavailableSources = append(out.UnavailableSources, "platform_alert_history")
	}
	if e.ResolvedAt != nil {
		out.History = append(out.History, api.ObsIncidentEvidenceEntry{ID: e.ID + ":resolved", Source: "platform_alert", Kind: "platform_alert.resolved", Status: "resolved", At: *e.ResolvedAt})
	}
	out.History = append(out.History, api.ObsIncidentEvidenceEntry{ID: e.ID + ":firing", Source: "platform_alert", Kind: "platform_alert.firing", Status: "firing", At: e.FirstSeenAt})
	sort.SliceStable(out.History, func(i, j int) bool { return out.History[i].At.After(out.History[j].At) })
	if len(out.History) > out.Limit {
		out.Truncated = true
		out.History = out.History[:out.Limit]
	}
	return out, true, nil
}
