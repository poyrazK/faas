package state

import (
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/flags"
)

// Funnel and retention conversions and CUPED values in MemStore
// (ADR 20261119102741).

// memUnitCounted reports whether a unit counts in results: a funnel or
// retention unit only once its deadline is at or before the cursor.
func memUnitCounted(x FlagExperiment, u *memFlagExperimentUnit) bool {
	h := x.MetricDefinition.Horizon(x.MetricKind)
	return h == 0 || !u.firstExposed.Add(h).After(x.TelemetryCursor)
}

// FlagExperimentConversionCandidates implements FlagExperimentAnalyticsStore.
func (m *MemStore) FlagExperimentConversionCandidates(_ context.Context, x FlagExperiment, afterCustomer string, now, notBefore time.Time, limit int) ([]FlagExperimentUnitRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	start, horizon := x.MetricDefinition.StartOffset(x.MetricKind), conversionHorizon(x)
	var out []FlagExperimentUnitRef
	for id, u := range m.flagEvents.units[x.ID] {
		if u.crossed || u.converted || id <= afterCustomer ||
			u.firstExposed.Add(start).After(now) || !u.firstExposed.Add(horizon).After(notBefore) {
			continue
		}
		out = append(out, memUnitRef(id, u))
	}
	slices.SortFunc(out, func(a, b FlagExperimentUnitRef) int { return compareStrings(a.CustomerID, b.CustomerID) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// MarkFlagExperimentUnitConversions implements FlagExperimentAnalyticsStore.
func (m *MemStore) MarkFlagExperimentUnitConversions(_ context.Context, experimentID string, conversions map[string]time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	for id, at := range conversions {
		if u := m.flagEvents.units[experimentID][id]; u != nil && !u.converted && !at.Before(u.firstExposed) {
			u.converted, u.convertedAt = true, at.UTC()
		}
	}
	return nil
}

// FlagExperimentUnitsWithoutPreValue implements FlagExperimentAnalyticsStore.
func (m *MemStore) FlagExperimentUnitsWithoutPreValue(_ context.Context, experimentID string, limit int) ([]FlagExperimentUnitRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	var out []FlagExperimentUnitRef
	for id, u := range m.flagEvents.units[experimentID] {
		if u.preValue == nil {
			out = append(out, memUnitRef(id, u))
		}
	}
	slices.SortFunc(out, func(a, b FlagExperimentUnitRef) int { return compareStrings(a.CustomerID, b.CustomerID) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SetFlagExperimentPreValues implements FlagExperimentAnalyticsStore.
func (m *MemStore) SetFlagExperimentPreValues(_ context.Context, experimentID string, values map[string]float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	for id, v := range values {
		if u := m.flagEvents.units[experimentID][id]; u != nil && u.preValue == nil {
			v := v
			u.preValue = &v
		}
	}
	return nil
}

// FlagExperimentCupedArms implements FlagExperimentAnalyticsStore. Mean
// metrics use each unit's folded value without winsorizing.
func (m *MemStore) FlagExperimentCupedArms(_ context.Context, x FlagExperiment, _ int64) ([]flags.CupedArm, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.flagEventsLocked().experimentsLocked()
	byVariant := map[string]*flags.CupedArm{}
	var pending int64
	for _, u := range m.flagEvents.units[x.ID] {
		if u.crossed || !memUnitCounted(x, u) {
			continue
		}
		a := byVariant[u.variant]
		if a == nil {
			a = &flags.CupedArm{Variant: u.variant}
			byVariant[u.variant] = a
		}
		var y float64
		switch {
		case x.MetricKind == flags.ExperimentMetricMean:
			y = float64(u.valueSum)
		case u.converted:
			y = 1
		}
		a.N++
		a.SumY += y
		a.SumY2 += y * y
		if u.preValue == nil {
			pending++
			continue
		}
		xv := *u.preValue
		a.SumX += xv
		a.SumX2 += xv * xv
		a.SumXY += xv * y
	}
	arms := make([]flags.CupedArm, 0, len(byVariant))
	for _, a := range byVariant {
		arms = append(arms, *a)
	}
	slices.SortFunc(arms, func(a, b flags.CupedArm) int { return compareStrings(a.Variant, b.Variant) })
	return arms, pending, nil
}

func memUnitRef(id string, u *memFlagExperimentUnit) FlagExperimentUnitRef {
	ref := FlagExperimentUnitRef{CustomerID: id, FirstExposedAt: u.firstExposed, Customer: id}
	if u.subjectHash != nil {
		ref.Customer, ref.SubjectHash = u.customer, u.subjectHash
	}
	return ref
}
