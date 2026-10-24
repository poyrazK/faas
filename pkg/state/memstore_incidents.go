package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// recordAlertEpisodeLocked mirrors setAlertRuleStateSQL for a rule whose
// state just changed. Episodes whose rule is gone are skipped on read, the
// MemStore's stand-in for ON DELETE CASCADE.
func (m *MemStore) recordAlertEpisodeLocked(r AlertRule, at time.Time) {
	if m.alertEpisodes == nil {
		m.alertEpisodes = map[string]AlertEpisode{}
	}
	var open *AlertEpisode
	var resolved []AlertEpisode
	for _, ep := range m.alertEpisodes {
		if ep.RuleID != r.ID {
			continue
		}
		if ep.ResolvedAt == nil {
			ep := ep
			open = &ep
		} else {
			resolved = append(resolved, ep)
		}
	}
	switch r.State {
	case AlertStateFiring:
		if open != nil {
			return
		}
		sortEpisodesNewestFirst(resolved)
		for _, ep := range resolved[min(len(resolved), alertEpisodeKeep):] {
			delete(m.alertEpisodes, ep.ID)
		}
		id := uuid.NewString()
		m.alertEpisodes[id] = AlertEpisode{ID: id, RuleID: r.ID, AccountID: r.AccountID, OpenedAt: at}
	case AlertStateOk:
		if open == nil {
			return
		}
		end := at
		if end.Before(open.OpenedAt) {
			end = open.OpenedAt
		}
		open.ResolvedAt = &end
		m.alertEpisodes[open.ID] = *open
	}
}

func sortEpisodesNewestFirst(eps []AlertEpisode) {
	sort.Slice(eps, func(i, j int) bool {
		if !eps[i].OpenedAt.Equal(eps[j].OpenedAt) {
			return eps[i].OpenedAt.After(eps[j].OpenedAt)
		}
		return eps[i].ID > eps[j].ID
	})
}

// withRuleLocked fills the rule columns, or reports the rule gone.
func (m *MemStore) withRuleLocked(ep AlertEpisode) (AlertEpisode, bool) {
	r, ok := m.alertRules[ep.RuleID]
	if !ok {
		return ep, false
	}
	ep.AppID, ep.RuleName, ep.Metric, ep.RuleState = r.AppID, r.Name, r.Metric, r.State
	if ep.ResolvedAt != nil {
		at := *ep.ResolvedAt
		ep.ResolvedAt = &at
	}
	return ep, true
}

func (m *MemStore) ListAlertEpisodes(_ context.Context, accountID, appID string, resolvedSince time.Time, limit int) ([]AlertEpisode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []AlertEpisode{}
	for _, ep := range m.alertEpisodes {
		if ep.AccountID != accountID || (ep.ResolvedAt != nil && ep.ResolvedAt.Before(resolvedSince)) {
			continue
		}
		ep, ok := m.withRuleLocked(ep)
		if !ok || (appID != "" && ep.AppID != appID) {
			continue
		}
		out = append(out, ep)
	}
	sortEpisodesNewestFirst(out)
	return out[:min(len(out), max(limit, 0))], nil
}

func (m *MemStore) AlertEpisodeByID(_ context.Context, accountID, id string) (AlertEpisode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ep, ok := m.alertEpisodes[id]
	if !ok || ep.AccountID != accountID {
		return AlertEpisode{}, ErrNotFound
	}
	if ep, ok = m.withRuleLocked(ep); !ok {
		return AlertEpisode{}, ErrNotFound
	}
	return ep, nil
}

func (m *MemStore) ListRouteMonitorIncidentSummaries(_ context.Context, accountID, appID string, closedSince time.Time, limit int) ([]RouteMonitorIncidentSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RouteMonitorIncidentSummary{}
	for id, incidents := range m.routeMonitorIncidents {
		if app, ok := m.apps[id]; !ok || app.AccountID != accountID || (appID != "" && id != appID) {
			continue
		}
		for _, inc := range incidents {
			if inc.Status != "open" && (inc.ClosedAt == nil || inc.ClosedAt.Before(closedSince)) {
				continue
			}
			out = append(out, routeMonitorIncidentSummary(inc))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.After(out[j].OpenedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out[:min(len(out), max(limit, 0))], nil
}

func (m *MemStore) RouteMonitorIncidentSummaryByID(_ context.Context, accountID, id string) (RouteMonitorIncidentSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for appID, incidents := range m.routeMonitorIncidents {
		if app, ok := m.apps[appID]; !ok || app.AccountID != accountID {
			continue
		}
		for _, inc := range incidents {
			if inc.ID == id {
				return routeMonitorIncidentSummary(inc), nil
			}
		}
	}
	return RouteMonitorIncidentSummary{}, ErrNotFound
}

func routeMonitorIncidentSummary(inc api.RouteMonitorIncident) RouteMonitorIncidentSummary {
	out := RouteMonitorIncidentSummary{ID: inc.ID, AppID: inc.AppID, DeploymentID: inc.DeploymentID, Status: inc.Status, OpenedAt: inc.OpenedAt.UTC()}
	if inc.ClosedAt != nil {
		at := inc.ClosedAt.UTC()
		out.ClosedAt = &at
	}
	return out
}
