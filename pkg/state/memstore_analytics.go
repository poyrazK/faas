package state

import (
	"bytes"
	"context"
	"maps"
	"slices"
	"time"
)

// MemStore keeps product analytics events in memory for apid tests
// (ADR 20261117084505). It has no request telemetry, so
// AnalyticsRequestsByFlag reports errMemStoreRequestTelemetry.

type memAnalyticsEvent struct {
	accountID, environmentID, customerID string
	subjectHash                          []byte
	name                                 string
	at                                   time.Time
	value                                int64
	properties, flags                    map[string]string
}

func (e memAnalyticsEvent) unit(kind string) (AnalyticsUnitRef, bool) {
	if kind == AnalyticsUnitSubject {
		return AnalyticsUnitRef{SubjectHash: e.subjectHash}, e.subjectHash != nil
	}
	return AnalyticsUnitRef{CustomerID: e.customerID}, true
}

func (e memAnalyticsEvent) hasProperties(want map[string]string) bool {
	for k, v := range want {
		if e.properties[k] != v {
			return false
		}
	}
	return true
}

func (e memAnalyticsEvent) matches(m AnalyticsMatch, ignoreRange bool) bool {
	return e.environmentID == m.EnvironmentID && e.name == m.Name && e.hasProperties(m.Properties) &&
		(ignoreRange || (!e.at.Before(m.From) && e.at.Before(m.Until)))
}

func (m *MemStore) memAddAnalyticsLocked(in FlagEventIngest, events []FlagConversionEvent) {
	fe := m.flagEventsLocked()
	if fe.propertyKeys == nil {
		fe.propertyKeys = map[string]map[string]bool{}
	}
	keys := fe.propertyKeys[in.Scope.EnvironmentID]
	if keys == nil {
		keys = map[string]bool{}
		fe.propertyKeys[in.Scope.EnvironmentID] = keys
	}
	for _, e := range events {
		props := map[string]string{}
		names := slices.Sorted(maps.Keys(e.Properties))
		for _, k := range names {
			if !keys[k] && len(keys) < in.MaxPropertyKeys {
				keys[k] = true
			}
			if keys[k] {
				props[k] = e.Properties[k]
			}
		}
		var subject []byte
		if e.Subject != "" {
			subject = AnalyticsSubjectHash(in.Scope.EnvironmentID, e.Subject)
		}
		flags := maps.Clone(e.Flags)
		if flags == nil {
			flags = map[string]string{}
		}
		fe.analytics = append(fe.analytics, memAnalyticsEvent{accountID: in.Scope.AccountID, environmentID: in.Scope.EnvironmentID,
			customerID: e.CustomerID, subjectHash: subject, name: e.Name, at: e.At.UTC(), value: e.Value, properties: props, flags: flags})
	}
}

// AnalyticsEventSeries implements AnalyticsStore.
func (m *MemStore) AnalyticsEventSeries(_ context.Context, q AnalyticsSeriesQuery) ([]AnalyticsSeriesRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type key struct {
		bucket    time.Time
		breakdown string
	}
	rows := map[key]*AnalyticsSeriesRow{}
	units := map[key]map[string]bool{}
	for _, e := range m.flagEventsLocked().analytics {
		u, ok := e.unit(q.Unit)
		if !ok || !e.matches(q.AnalyticsMatch, false) {
			continue
		}
		start := q.From.Add(e.at.Sub(q.From).Truncate(q.Bucket)).UTC()
		breakdown := ""
		switch q.ByKind {
		case "property":
			breakdown = valueOrNone(e.properties, q.ByKey)
		case "flag":
			breakdown = valueOrNone(e.flags, q.ByKey)
		}
		k := key{start, breakdown}
		if rows[k] == nil {
			rows[k] = &AnalyticsSeriesRow{BucketStart: start, Breakdown: breakdown}
			units[k] = map[string]bool{}
		}
		rows[k].Events++
		rows[k].ValueSum += e.value
		units[k][u.Key()] = true
	}
	out := make([]AnalyticsSeriesRow, 0, len(rows))
	for k, r := range rows {
		r.Units = int64(len(units[k]))
		out = append(out, *r)
	}
	slices.SortFunc(out, func(a, b AnalyticsSeriesRow) int {
		if c := a.BucketStart.Compare(b.BucketStart); c != 0 {
			return c
		}
		return compareStrings(a.Breakdown, b.Breakdown)
	})
	return out, nil
}

func valueOrNone(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return "(none)"
}

// AnalyticsEntries implements AnalyticsStore.
func (m *MemStore) AnalyticsEntries(_ context.Context, match AnalyticsMatch, unit string, maxUnits int) ([]AnalyticsEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	first := map[string]AnalyticsEntry{}
	for _, e := range m.flagEventsLocked().analytics {
		u, ok := e.unit(unit)
		if !ok || !e.matches(match, false) {
			continue
		}
		if cur, ok := first[u.Key()]; !ok || e.at.Before(cur.At) {
			first[u.Key()] = AnalyticsEntry{Unit: u, At: e.at, Properties: maps.Clone(e.properties), Flags: maps.Clone(e.flags)}
		}
	}
	out := slices.SortedFunc(maps.Values(first), func(a, b AnalyticsEntry) int { return compareStrings(a.Unit.Key(), b.Unit.Key()) })
	if len(out) > maxUnits {
		out = out[:maxUnits]
	}
	return out, nil
}

// AnalyticsNextSteps implements AnalyticsStore.
func (m *MemStore) AnalyticsNextSteps(_ context.Context, match AnalyticsMatch, unit string, steps []AnalyticsStep) (map[string]time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	byKey := map[string]AnalyticsStep{}
	for _, st := range steps {
		byKey[st.Unit.Key()] = st
	}
	out := map[string]time.Time{}
	for _, e := range m.flagEventsLocked().analytics {
		u, ok := e.unit(unit)
		if !ok || !e.matches(match, true) {
			continue
		}
		st, ok := byKey[u.Key()]
		if !ok || !e.at.After(st.After) || e.at.After(st.Deadline) {
			continue
		}
		if cur, ok := out[u.Key()]; !ok || e.at.Before(cur) {
			out[u.Key()] = e.at
		}
	}
	return out, nil
}

// AnalyticsUnitFlags implements AnalyticsStore.
func (m *MemStore) AnalyticsUnitFlags(_ context.Context, environmentID, unit, flag string, units []AnalyticsUnitRef, from, until time.Time) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]bool{}
	for _, u := range units {
		want[u.Key()] = true
	}
	out := map[string]string{}
	earliest := map[string]time.Time{}
	for _, e := range m.flagEventsLocked().analytics {
		u, ok := e.unit(unit)
		v, has := e.flags[flag]
		if !ok || !has || e.environmentID != environmentID || !want[u.Key()] || e.at.Before(from) || !e.at.Before(until) {
			continue
		}
		if cur, ok := earliest[u.Key()]; !ok || e.at.Before(cur) {
			earliest[u.Key()], out[u.Key()] = e.at, v
		}
	}
	return out, nil
}

// AnalyticsReturns implements AnalyticsStore.
func (m *MemStore) AnalyticsReturns(_ context.Context, match AnalyticsMatch, unit string, units []AnalyticsCohortUnit, period time.Duration, periods int) ([]AnalyticsReturnRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type key struct {
		cohort    time.Time
		breakdown string
		period    int
	}
	seen := map[key]map[string]bool{}
	for _, cu := range units {
		for _, e := range m.flagEventsLocked().analytics {
			u, ok := e.unit(unit)
			if !ok || u.Key() != cu.Unit.Key() || !e.matches(match, true) || !e.at.After(cu.FirstAt) {
				continue
			}
			if unit == AnalyticsUnitSubject && !bytes.Equal(u.SubjectHash, cu.Unit.SubjectHash) {
				continue
			}
			k := int(e.at.Sub(cu.Cohort) / period)
			if k < 1 || k > periods {
				continue
			}
			kk := key{cu.Cohort, cu.Breakdown, k}
			if seen[kk] == nil {
				seen[kk] = map[string]bool{}
			}
			seen[kk][cu.Unit.Key()] = true
		}
	}
	out := make([]AnalyticsReturnRow, 0, len(seen))
	for k, set := range seen {
		out = append(out, AnalyticsReturnRow{Cohort: k.cohort, Breakdown: k.breakdown, Period: k.period, Units: int64(len(set))})
	}
	slices.SortFunc(out, func(a, b AnalyticsReturnRow) int {
		if c := a.Cohort.Compare(b.Cohort); c != 0 {
			return c
		}
		if c := compareStrings(a.Breakdown, b.Breakdown); c != 0 {
			return c
		}
		return a.Period - b.Period
	})
	return out, nil
}

// AnalyticsRequestsByFlag reads request telemetry, which MemStore does not
// keep (ADR-127): like the other telemetry readers it returns
// errMemStoreRequestTelemetry, and the query is tested against Postgres.
func (m *MemStore) AnalyticsRequestsByFlag(context.Context, AnalyticsRequestsQuery) ([]AnalyticsRequestRow, error) {
	return nil, errMemStoreRequestTelemetry
}

// SweepAnalyticsEvents implements AnalyticsStore.
func (m *MemStore) SweepAnalyticsEvents(_ context.Context, now time.Time, retentionDays map[string]int, _, _ int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fe := m.flagEventsLocked()
	kept := fe.analytics[:0]
	var deleted int64
	for _, e := range fe.analytics {
		acct, ok := m.accounts[e.accountID]
		days, known := retentionDays[string(acct.Plan)]
		if !ok || !known || e.at.Before(now.UTC().AddDate(0, 0, -days)) {
			deleted++
			continue
		}
		kept = append(kept, e)
	}
	fe.analytics = kept
	return deleted, nil
}
