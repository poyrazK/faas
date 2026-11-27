package state

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Analytics cohorts in MemStore (ADR 20261119102741 §3).

func (fe *memFlagEvents) cohortsLocked() map[string]*AnalyticsCohort {
	if fe.cohorts == nil {
		fe.cohorts = map[string]*AnalyticsCohort{}
		fe.cohortMembers = map[string][]string{}
	}
	return fe.cohorts
}

func (m *MemStore) memCohort(environmentID, key string) *AnalyticsCohort {
	for _, c := range m.flagEventsLocked().cohortsLocked() {
		if c.EnvironmentID == environmentID && c.Key == key {
			return c
		}
	}
	return nil
}

func cloneAnalyticsCohort(c *AnalyticsCohort) AnalyticsCohort {
	out := *c
	out.Definition.Where = maps.Clone(c.Definition.Where)
	out.Definition.Steps = slices.Clone(c.Definition.Steps)
	if c.RefreshedAt != nil {
		t := *c.RefreshedAt
		out.RefreshedAt = &t
	}
	return out
}

// CreateAnalyticsCohort implements AnalyticsCohortStore.
func (m *MemStore) CreateAnalyticsCohort(_ context.Context, c AnalyticsCohort, maxCohorts int) (AnalyticsCohort, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.memCohort(c.EnvironmentID, c.Key) != nil {
		return AnalyticsCohort{}, ErrConflict
	}
	n := 0
	for _, x := range m.flagEventsLocked().cohortsLocked() {
		if x.EnvironmentID == c.EnvironmentID {
			n++
		}
	}
	if n >= maxCohorts {
		return AnalyticsCohort{}, ErrAnalyticsCohortLimit
	}
	c.ID = uuid.NewString()
	now := time.Now().UTC()
	c.CreatedAt, c.RefreshRequestedAt, c.Members, c.Truncated, c.RefreshedAt, c.RefreshError = now, now, 0, false, nil, ""
	stored := cloneAnalyticsCohort(&c)
	m.flagEvents.cohorts[c.ID] = &stored
	return cloneAnalyticsCohort(&stored), nil
}

// GetAnalyticsCohort implements AnalyticsCohortStore.
func (m *MemStore) GetAnalyticsCohort(_ context.Context, environmentID, key string) (AnalyticsCohort, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.memCohort(environmentID, key)
	if c == nil {
		return AnalyticsCohort{}, ErrNotFound
	}
	return cloneAnalyticsCohort(c), nil
}

// ListAnalyticsCohorts implements AnalyticsCohortStore.
func (m *MemStore) ListAnalyticsCohorts(_ context.Context, environmentID string) ([]AnalyticsCohort, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AnalyticsCohort
	for _, c := range m.flagEventsLocked().cohortsLocked() {
		if c.EnvironmentID == environmentID {
			out = append(out, cloneAnalyticsCohort(c))
		}
	}
	slices.SortFunc(out, func(a, b AnalyticsCohort) int { return compareStrings(a.Key, b.Key) })
	return out, nil
}

// DeleteAnalyticsCohort implements AnalyticsCohortStore.
func (m *MemStore) DeleteAnalyticsCohort(_ context.Context, environmentID, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.memCohort(environmentID, key)
	if c == nil {
		return false, nil
	}
	delete(m.flagEvents.cohorts, c.ID)
	delete(m.flagEvents.cohortMembers, c.ID)
	return true, nil
}

// RequestAnalyticsCohortRefresh implements AnalyticsCohortStore.
func (m *MemStore) RequestAnalyticsCohortRefresh(_ context.Context, environmentID, key string, now time.Time) (AnalyticsCohort, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.memCohort(environmentID, key)
	if c == nil {
		return AnalyticsCohort{}, ErrNotFound
	}
	c.RefreshRequestedAt = now.UTC()
	return cloneAnalyticsCohort(c), nil
}

// DueAnalyticsCohorts implements AnalyticsCohortStore. MemStore has no
// accounts table here, so Plan is left empty for the caller to resolve.
func (m *MemStore) DueAnalyticsCohorts(_ context.Context, staleBefore time.Time, limit int) ([]AnalyticsCohort, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AnalyticsCohort
	for _, c := range m.flagEventsLocked().cohortsLocked() {
		if c.RefreshedAt == nil || c.RefreshRequestedAt.After(*c.RefreshedAt) || c.RefreshedAt.Before(staleBefore) {
			x := cloneAnalyticsCohort(c)
			if acct, ok := m.accounts[c.AccountID]; ok {
				x.Plan = string(acct.Plan)
			}
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b AnalyticsCohort) int { return a.RefreshRequestedAt.Compare(b.RefreshRequestedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ReplaceAnalyticsCohortMembers implements AnalyticsCohortStore.
func (m *MemStore) ReplaceAnalyticsCohortMembers(_ context.Context, c AnalyticsCohort, customers []string, truncated bool, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := m.flagEventsLocked().cohortsLocked()[c.ID]
	if stored == nil {
		return ErrNotFound
	}
	members := slices.Clone(customers)
	slices.Sort(members)
	members = slices.Compact(members)
	m.flagEvents.cohortMembers[c.ID] = members
	t := now.UTC()
	if c.RefreshRequestedAt.After(t) {
		t = c.RefreshRequestedAt.UTC()
	}
	stored.Members, stored.Truncated, stored.RefreshedAt, stored.RefreshError = len(members), truncated, &t, ""
	return nil
}

// FailAnalyticsCohortRefresh implements AnalyticsCohortStore.
func (m *MemStore) FailAnalyticsCohortRefresh(_ context.Context, cohortID string, now time.Time, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.flagEventsLocked().cohortsLocked()[cohortID]; c != nil {
		t := now.UTC()
		if c.RefreshRequestedAt.After(t) {
			t = c.RefreshRequestedAt
		}
		c.RefreshedAt, c.RefreshError = &t, msg
	}
	return nil
}

// AnalyticsCohortMembersForTest returns a cohort's members.
func (m *MemStore) AnalyticsCohortMembersForTest(cohortID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.flagEventsLocked().cohortMembers[cohortID])
}

// AnalyticsCohortDid implements AnalyticsCohortStore.
func (m *MemStore) AnalyticsCohortDid(_ context.Context, match AnalyticsMatch, minCount, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[string]int{}
	for _, e := range m.flagEventsLocked().analytics {
		if e.environmentID == match.EnvironmentID && e.name == match.Name && e.hasProperties(match.Properties) && !e.at.Before(match.From) {
			counts[e.customerID]++
		}
	}
	var out []string
	for id, n := range counts {
		if n >= minCount {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AnalyticsCohortDidNot implements AnalyticsCohortStore.
func (m *MemStore) AnalyticsCohortDidNot(_ context.Context, match AnalyticsMatch, activeSince time.Time, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	active, did := map[string]bool{}, map[string]bool{}
	for _, e := range m.flagEventsLocked().analytics {
		if e.environmentID != match.EnvironmentID {
			continue
		}
		if !e.at.Before(activeSince) {
			active[e.customerID] = true
		}
		if e.name == match.Name && e.hasProperties(match.Properties) && !e.at.Before(match.From) {
			did[e.customerID] = true
		}
	}
	var out []string
	for id := range active {
		if !did[id] {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AnalyticsCustomerTotals implements AnalyticsCohortStore.
func (m *MemStore) AnalyticsCustomerTotals(_ context.Context, match AnalyticsMatch, customers []string) (map[string]AnalyticsTotal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]bool{}
	for _, id := range customers {
		want[id] = true
	}
	out := map[string]AnalyticsTotal{}
	for _, e := range m.flagEventsLocked().analytics {
		if want[e.customerID] && e.matches(match, false) {
			t := out[e.customerID]
			t.Events++
			t.Value += float64(e.value)
			out[e.customerID] = t
		}
	}
	return out, nil
}
