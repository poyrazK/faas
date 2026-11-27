package state

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/flags"
)

// Subject units, ratio and percentile metrics, bandits and segments in
// MemStore (ADR 20261119173702). Rollout metric arms need request
// telemetry, which MemStore does not keep; tests set them with
// SetRolloutMetricArmsForTest.

// RecordFlagExperimentExposures implements FlagExperimentExtrasStore.
func (m *MemStore) RecordFlagExperimentExposures(_ context.Context, accountID, environmentID string, exposures []FlagExperimentExposure) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fe := m.flagEventsLocked()
	fe.experimentsLocked()
	accepted := 0
	for _, e := range exposures {
		if t, ok := m.platformTenants[e.CustomerID]; !ok || t.AccountID != accountID || t.Status != "active" {
			continue
		}
		var x *FlagExperiment
		for _, c := range fe.experiments {
			if c.EnvironmentID == environmentID && c.Status == flags.ExperimentRunning && c.SubjectUnits() && c.FlagKey == e.Flag && c.RuleID == e.RuleID {
				x = c
			}
		}
		if x == nil {
			continue
		}
		accepted++
		if fe.units[x.ID] == nil {
			fe.units[x.ID] = map[string]*memFlagExperimentUnit{}
		}
		id := SubjectUnitID(e.CustomerID, e.SubjectHash)
		u := fe.units[x.ID][id]
		if u == nil {
			fe.units[x.ID][id] = &memFlagExperimentUnit{variant: e.Variant, firstExposed: e.At.UTC(), customer: e.CustomerID, subjectHash: slices.Clone(e.SubjectHash)}
			continue
		}
		u.crossed = u.crossed || u.variant != e.Variant
		if e.At.Before(u.firstExposed) {
			u.firstExposed = e.At.UTC()
		}
	}
	return accepted, nil
}

// memUnitValue is a unit's metric value: folded plus open days of event.
func (m *MemStore) memUnitValueLocked(x FlagExperiment, customer string, u *memFlagExperimentUnit, event string, folded int64) float64 {
	open := flagExperimentOpenFromDay(x)
	v := float64(folded)
	for k, d := range m.flagEvents.daily {
		if k.environment == x.EnvironmentID && k.name == event && k.customer == customer && !d.Day.Before(open) && !d.LastAt.Before(u.firstExposed) {
			v += float64(d.ValueSum)
		}
	}
	return v
}

// FlagExperimentRatioArms implements FlagExperimentExtrasStore.
func (m *MemStore) FlagExperimentRatioArms(_ context.Context, x FlagExperiment) ([]flags.RatioArm, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	byVariant := map[string]*flags.RatioArm{}
	for customer, u := range m.flagEvents.units[x.ID] {
		if u.crossed {
			continue
		}
		num := m.memUnitValueLocked(x, customer, u, x.MetricEvent, u.valueSum)
		den := m.memUnitValueLocked(x, customer, u, x.DenominatorEvent, u.denSum)
		a := byVariant[u.variant]
		if a == nil {
			a = &flags.RatioArm{Variant: u.variant}
			byVariant[u.variant] = a
		}
		a.Units++
		a.Num += num
		a.Den += den
		a.NumSq += num * num
		a.DenSq += den * den
		a.NumDen += num * den
	}
	out := make([]flags.RatioArm, 0, len(byVariant))
	for _, a := range byVariant {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b flags.RatioArm) int { return compareStrings(a.Variant, b.Variant) })
	return out, nil
}

// FlagExperimentUnitValues implements FlagExperimentExtrasStore.
func (m *MemStore) FlagExperimentUnitValues(_ context.Context, x FlagExperiment, maxPerArm int) (map[string][]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	ids := make([]string, 0, len(m.flagEvents.units[x.ID]))
	for id := range m.flagEvents.units[x.ID] {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := map[string][]float64{}
	for _, id := range ids {
		u := m.flagEvents.units[x.ID][id]
		if u.crossed || len(out[u.variant]) >= maxPerArm {
			continue
		}
		out[u.variant] = append(out[u.variant], m.memUnitValueLocked(x, id, u, x.MetricEvent, u.valueSum))
	}
	for _, v := range out {
		slices.Sort(v)
	}
	return out, nil
}

// SetFlagExperimentBandit implements FlagExperimentExtrasStore.
func (m *MemStore) SetFlagExperimentBandit(_ context.Context, experimentID string, posterior []flags.BanditArm, weights map[string]int, weightedAt *time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.flagEventsLocked().experimentsLocked()[experimentID]
	if x == nil || x.Status != flags.ExperimentRunning {
		return nil
	}
	x.Bandit = slices.Clone(posterior)
	if weightedAt != nil {
		at := weightedAt.UTC()
		x.BanditWeightedAt = &at
		x.Weights = map[string]int{}
		for k, v := range weights {
			x.Weights[k] = v
		}
	}
	return nil
}

// FlagExperimentSegmentArms implements FlagExperimentExtrasStore.
func (m *MemStore) FlagExperimentSegmentArms(_ context.Context, x FlagExperiment, cohortID string) ([]flags.Arm, []flags.Arm, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	members := map[string]bool{}
	for _, c := range m.flagEvents.cohortMembers[cohortID] {
		members[c] = true
	}
	in, out := map[string]*flags.Arm{}, map[string]*flags.Arm{}
	for id, u := range m.flagEvents.units[x.ID] {
		if u.crossed || !memUnitCounted(x, u) {
			continue
		}
		customer := id
		if u.subjectHash != nil {
			customer = u.customer
		}
		group := out
		if members[customer] {
			group = in
		}
		a := group[u.variant]
		if a == nil {
			a = &flags.Arm{Variant: u.variant}
			group[u.variant] = a
		}
		a.Exposed++
		if u.converted {
			a.Converted++
		}
	}
	return sortedArms(in), sortedArms(out), nil
}

func sortedArms(m map[string]*flags.Arm) []flags.Arm {
	out := make([]flags.Arm, 0, len(m))
	for _, a := range m {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b flags.Arm) int { return compareStrings(a.Variant, b.Variant) })
	return out
}

// FeatureFlagRolloutMetricArms implements FeatureFlagRolloutMetricStore
// with the arms a test set.
func (m *MemStore) FeatureFlagRolloutMetricArms(_ context.Context, q RolloutMetricQuery) (flags.Arm, flags.Arm, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	arms := m.flagEventsLocked().rolloutArms[q.FlagKey+"\x00"+q.RuleID]
	return arms[0], arms[1], nil
}

// SetRolloutMetricArmsForTest sets the comparison and treatment arms
// FeatureFlagRolloutMetricArms returns for a flag's rule.
func (m *MemStore) SetRolloutMetricArmsForTest(flagKey, ruleID string, comparison, treatment flags.Arm) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fe := m.flagEventsLocked()
	if fe.rolloutArms == nil {
		fe.rolloutArms = map[string][2]flags.Arm{}
	}
	fe.rolloutArms[flagKey+"\x00"+ruleID] = [2]flags.Arm{comparison, treatment}
}
