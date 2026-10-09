package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memPeriodicMonitor struct {
	Monitor               api.ProfilePeriodicMonitor
	AccountID, Token      string
	LeaseUntil, UpdatedAt time.Time
}

func (m *MemStore) periodicEligibleLocked(row *memPeriodicMonitor) bool {
	mon := row.Monitor
	p := m.profileDeploymentPolicies[mon.AppID]
	app := m.apps[mon.AppID]
	acct := m.accounts[row.AccountID]
	d := m.deployments[mon.DeploymentID]
	if app.AccountID != row.AccountID || app.Status == AppDeleted || !acct.Active() || !api.MustLimitsFor(acct.Plan).Profiling.Enabled || d.Status != DeployLive || !profileCheckDeploymentSucceeded(d) || !p.Config.Enabled || p.Config.Periodic == nil || p.Revision != mon.PolicyRevision {
		return false
	}
	for _, route := range p.Config.Options.Routes {
		if route == mon.Route {
			return true
		}
	}
	return false
}
func (m *MemStore) DiscoverProfilePeriodicMonitors(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.profilePeriodicMonitors == nil {
		m.profilePeriodicMonitors = map[string]*memPeriodicMonitor{}
	}
	pruned := 0
	for id, row := range m.profilePeriodicMonitors {
		if row.UpdatedAt.Before(now.Add(-api.ProfileAutoReceiptRetention)) && !m.periodicEligibleLocked(row) {
			delete(m.profilePeriodicMonitors, id)
			pruned++
			if pruned >= api.ProfileAutoBatchSize {
				break
			}
		}
	}
	ids := make([]string, 0, len(m.deployments))
	for id := range m.deployments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	count := 0
	for _, id := range ids {
		d := m.deployments[id]
		p := m.profileDeploymentPolicies[d.AppID]
		if p.Config.Periodic == nil {
			continue
		}
		for _, route := range p.Config.Options.Routes {
			probe := &memPeriodicMonitor{AccountID: m.apps[d.AppID].AccountID, Monitor: api.ProfilePeriodicMonitor{AppID: d.AppID, DeploymentID: d.ID, Route: route, PolicyRevision: p.Revision}}
			if !m.periodicEligibleLocked(probe) {
				continue
			}
			exists := false
			for _, row := range m.profilePeriodicMonitors {
				mon := row.Monitor
				if mon.DeploymentID == d.ID && mon.PolicyRevision == p.Revision && mon.Route == route {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			probe.Monitor = newPeriodicMonitor(d, p, route, now)
			probe.UpdatedAt = now
			body, err := encodePeriodicMonitor(probe.Monitor)
			if err != nil {
				return err
			}
			_ = json.Unmarshal(body, &probe.Monitor)
			m.profilePeriodicMonitors[probe.Monitor.ID] = probe
			count++
			if count >= api.ProfileAutoBatchSize {
				return nil
			}
		}
	}
	return nil
}
func (m *MemStore) ClaimProfilePeriodicMonitor(_ context.Context, now time.Time) (ProfilePeriodicWork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *memPeriodicMonitor
	for _, row := range m.profilePeriodicMonitors {
		if !m.periodicEligibleLocked(row) || row.Monitor.NextAttemptAt.After(now) || row.LeaseUntil.After(now) {
			continue
		}
		if selected == nil || row.Monitor.NextAttemptAt.Before(selected.Monitor.NextAttemptAt) || row.Monitor.NextAttemptAt.Equal(selected.Monitor.NextAttemptAt) && row.Monitor.ID < selected.Monitor.ID {
			selected = row
		}
	}
	if selected == nil {
		return ProfilePeriodicWork{}, ErrNotFound
	}
	if selected.Monitor.Attempts <= api.ProfileAutoMaxAttempts {
		selected.Monitor.Attempts++
	}
	selected.Token = uuid.NewString()
	selected.LeaseUntil = now.Add(api.ProfileAutoLeaseDuration)
	body, _ := encodePeriodicMonitor(selected.Monitor)
	var mon api.ProfilePeriodicMonitor
	_ = json.Unmarshal(body, &mon)
	return ProfilePeriodicWork{Monitor: mon, AccountID: selected.AccountID, Token: selected.Token}, nil
}
func (m *MemStore) FinishProfilePeriodicMonitor(_ context.Context, work ProfilePeriodicWork, result ProfilePeriodicResult, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.profilePeriodicMonitors[work.Monitor.ID]
	if row == nil || row.AccountID != work.AccountID || row.Token != work.Token || !row.LeaseUntil.After(now) || !m.periodicEligibleLocked(row) {
		return ErrProfileCheckLease
	}
	mon := row.Monitor
	if err := validatePeriodicResult(mon, result); err != nil {
		return err
	}
	var plans []profileAlertPlan
	o := periodicObservation(mon, result.Assessment, m.apps[mon.AppID].Slug, work.AccountID)
	terminal := !result.Retry || mon.Attempts >= api.ProfileAutoMaxAttempts
	if terminal && mon.Baseline != nil {
		var err error
		plans, err = m.prepareProfileAlertsLocked(o)
		if err != nil {
			return err
		}
	}
	next := advancePeriodicMonitor(mon, result, plans, "", now)
	if _, err := encodePeriodicMonitor(next); err != nil {
		return err
	}
	if profileAlertHasEvent(plans) {
		saved := &memProfileDeploymentCheck{accountID: work.AccountID, check: PeriodicProfileCheck(mon)}
		if err := m.saveAutoProfileInvestigationLocked(saved, result.Assessment, now); err != nil {
			return err
		}
		next.History[0].InvestigationID = saved.check.InvestigationID
		profileAlertInvestigation(plans, o.Slug, saved.check.InvestigationID)
	}
	m.publishProfileAlertsLocked(o, plans)
	row.Monitor, row.Token, row.LeaseUntil, row.UpdatedAt = next, "", time.Time{}, now
	return nil
}
func (m *MemStore) ListProfilePeriodicMonitors(_ context.Context, account, app string) ([]api.ProfilePeriodicMonitor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(account, app) {
		return nil, ErrNotFound
	}
	var rows []*memPeriodicMonitor
	for _, row := range m.profilePeriodicMonitors {
		if row.AccountID == account && row.Monitor.AppID == app {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UpdatedAt.Equal(rows[j].UpdatedAt) {
			return rows[i].Monitor.ID < rows[j].Monitor.ID
		}
		return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
	})
	out := []api.ProfilePeriodicMonitor{}
	for _, row := range rows {
		if len(out) >= api.ProfilePeriodicMaxRows {
			break
		}
		body, _ := encodePeriodicMonitor(row.Monitor)
		var mon api.ProfilePeriodicMonitor
		_ = json.Unmarshal(body, &mon)
		mon.Active = m.periodicEligibleLocked(row)
		out = append(out, trimPeriodicHistory(mon, time.Now()))
	}
	return out, nil
}
