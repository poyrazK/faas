package state

import (
	"bytes"
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

func (m *MemStore) CreateAppLogDrain(_ context.Context, in AppLogDrain) (AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createAppLogDrainLocked(in)
}

func (m *MemStore) CreateAppLogDrainIfUnderQuota(_ context.Context, in AppLogDrain, limits api.Limits) (AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.appLogDrains {
		if existing.AppID == in.AppID && existing.TargetURL == in.TargetURL {
			return AppLogDrain{}, ErrConflict
		}
	}
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted {
		return AppLogDrain{}, ErrNotFound
	}
	subject := m.appBillingSubjectLocked(app.AccountID, app.ID)
	appCount, accountCount := 0, 0
	for _, drain := range m.appLogDrains {
		if drain.AppID == in.AppID {
			appCount++
		}
		if m.countsTowardQuotaSubjectLocked(drain.AccountID, drain.AppID, subject) {
			accountCount++
		}
	}
	if appCount >= limits.LogDrainPerApp {
		return AppLogDrain{}, &AppLogDrainQuotaError{Scope: AppLogDrainQuotaScopeApp, Limit: limits.LogDrainPerApp, Observed: appCount}
	}
	if accountCount >= limits.LogDrainPerAccount {
		return AppLogDrain{}, &AppLogDrainQuotaError{Scope: AppLogDrainQuotaScopeAccount, Limit: limits.LogDrainPerAccount, Observed: accountCount}
	}
	return m.createAppLogDrainLocked(in)
}

func (m *MemStore) createAppLogDrainLocked(in AppLogDrain) (AppLogDrain, error) {
	if m.standardManagedControlLocked(in.AppID, appstandards.LogDestinations) {
		return AppLogDrain{}, ErrApplicationStandardManagedControl
	}
	for _, existing := range m.appLogDrains {
		if existing.AppID == in.AppID && existing.TargetURL == in.TargetURL {
			return AppLogDrain{}, ErrConflict
		}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = in.CreatedAt
	}
	m.appLogDrains[in.ID] = cloneAppLogDrain(in)
	return in, nil
}

func (m *MemStore) AppLogDrainByID(_ context.Context, id string) (AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	drain, ok := m.appLogDrains[id]
	if !ok {
		return AppLogDrain{}, ErrNotFound
	}
	return cloneAppLogDrain(drain), nil
}

func (m *MemStore) UpdateAppLogDrain(_ context.Context, id string, p UpdateAppLogDrainParams) (AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	drain, ok := m.appLogDrains[id]
	if !ok {
		return AppLogDrain{}, ErrNotFound
	}
	if m.standardManagedControlLocked(drain.AppID, appstandards.LogDestinations) &&
		((p.Kind != nil && *p.Kind != drain.Kind) || (p.TargetURL != nil && *p.TargetURL != drain.TargetURL) ||
			(p.AuthHeaderSealed != nil && !bytes.Equal(*p.AuthHeaderSealed, drain.AuthHeaderSealed)) || (p.Enabled != nil && *p.Enabled != drain.Enabled)) {
		return AppLogDrain{}, ErrApplicationStandardManagedControl
	}
	if p.Kind != nil {
		drain.Kind = *p.Kind
	}
	if p.TargetURL != nil {
		for otherID, other := range m.appLogDrains {
			if otherID != id && other.AppID == drain.AppID && other.TargetURL == *p.TargetURL {
				return AppLogDrain{}, ErrConflict
			}
		}
		drain.TargetURL = *p.TargetURL
	}
	if p.AuthHeaderSealed != nil {
		drain.AuthHeaderSealed = append([]byte(nil), (*p.AuthHeaderSealed)...)
	}
	if p.Enabled != nil {
		drain.Enabled = *p.Enabled
	}
	drain.UpdatedAt = time.Now()
	m.appLogDrains[id] = cloneAppLogDrain(drain)
	return drain, nil
}

func (m *MemStore) DeleteAppLogDrain(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	drain, ok := m.appLogDrains[id]
	if !ok {
		return ErrNotFound
	}
	if m.standardManagedControlLocked(drain.AppID, appstandards.LogDestinations) {
		return ErrApplicationStandardManagedControl
	}
	delete(m.appLogDrains, id)
	m.eraseStandardLogDrainDeliveriesLocked(id)
	delete(m.appLogDrainHealth, id)
	for key, sample := range m.appLogDrainAnalytics {
		if sample.DrainID == id {
			delete(m.appLogDrainAnalytics, key)
		}
	}
	return nil
}

func (m *MemStore) ListAppLogDrainsForApp(_ context.Context, appID string) ([]AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return listAppLogDrains(func(d AppLogDrain) bool { return d.AppID == appID }, m.appLogDrains), nil
}

func (m *MemStore) ListEnabledAppLogDrains(_ context.Context) ([]AppLogDrain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.deliverableAppLogDrainsLocked()
	for i := range rows {
		rows[i].StandardBinding = m.standardLogBindingLocked(rows[i])
	}
	return rows, nil
}

// deliverableAppLogDrainsLocked is the drains log delivery runs, as both
// PgStore delivery reads select them: enabled, not on a rehearsal-enrolled
// app (ADR-20261019180000), and covered by the app owner's plan.
func (m *MemStore) deliverableAppLogDrainsLocked() []AppLogDrain {
	rows := m.enabledAppLogDrainsLocked(func(AppLogDrain) bool { return true })
	owners := make([]appLogDrainOwnership, len(rows))
	for i, d := range rows {
		owners[i] = m.appLogDrainOwnershipLocked(d)
	}
	return planCoveredAppLogDrains(rows, owners)
}

func (m *MemStore) enabledAppLogDrainsLocked(match func(AppLogDrain) bool) []AppLogDrain {
	return listAppLogDrains(func(d AppLogDrain) bool {
		_, enrolled := m.scenarioActionPolicies[d.AppID]
		return d.Enabled && !enrolled && match(d)
	}, m.appLogDrains)
}

// appLogDrainOwnershipLocked mirrors the owner object of PgStore's delivery
// reads. A drain whose app is gone has no plan, so it is not delivered.
func (m *MemStore) appLogDrainOwnershipLocked(d AppLogDrain) appLogDrainOwnership {
	app, ok := m.apps[d.AppID]
	if !ok {
		return appLogDrainOwnership{}
	}
	owner, _ := m.appResourceAccountLocked(app)
	return appLogDrainOwnership{
		Plan:     string(owner.Plan),
		Subject:  m.appBillingSubjectLocked(app.AccountID, app.ID),
		Counted:  app.Status != AppDeleted,
		Standard: m.standardBoundLogDrainLocked(d),
	}
}

func (m *MemStore) standardBoundLogDrainLocked(d AppLogDrain) bool {
	for _, b := range m.applicationStandardControlBindings {
		if b.Field == appstandards.LogDestinations && sameStandardUUID(b.AppID, d.AppID) && sameStandardUUID(b.PhysicalID, d.ID) {
			return true
		}
	}
	return false
}

func (m *MemStore) ListAppLogDrainsPausedByPlan(_ context.Context, appID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	paused := []string{}
	app, ok := m.apps[appID]
	if !ok {
		return paused, nil
	}
	subject := m.appBillingSubjectLocked(app.AccountID, app.ID)
	ledger := newAppLogDrainPlanLedger()
	for _, d := range m.enabledAppLogDrainsLocked(func(d AppLogDrain) bool {
		other, ok := m.apps[d.AppID]
		return ok && m.appBillingSubjectLocked(other.AccountID, other.ID) == subject
	}) {
		if !ledger.admit(d.AppID, m.appLogDrainOwnershipLocked(d)) && d.AppID == appID {
			paused = append(paused, d.ID)
		}
	}
	return paused, nil
}

func listAppLogDrains(match func(AppLogDrain) bool, rows map[string]AppLogDrain) []AppLogDrain {
	out := make([]AppLogDrain, 0)
	for _, drain := range rows {
		if match(drain) {
			out = append(out, cloneAppLogDrain(drain))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func cloneAppLogDrain(in AppLogDrain) AppLogDrain {
	in.AuthHeaderSealed = append([]byte(nil), in.AuthHeaderSealed...)
	if in.StandardBinding != nil {
		copy := *in.StandardBinding
		in.StandardBinding = &copy
	}
	return in
}
