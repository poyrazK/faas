package state

import (
	"github.com/onebox-faas/faas/pkg/flags"

	"context"
	"time"
)

// memFlagEvents mirrors flag_event_usage, flag_event_names and
// flag_conversion_daily for MemStore (ADR 20261115163113).
type memFlagEvents struct {
	usage map[string][2]int64        // account|month -> accepted, dropped
	names map[string]map[string]bool // environment -> names
	daily map[flagConversionKeyMem]*FlagConversionDay
	// experiments by ID, and units by experiment ID then customer.
	experiments map[string]*FlagExperiment
	units       map[string]map[string]*memFlagExperimentUnit
	// latency histograms by experiment, variant and bucket.
	latency map[string]map[string]map[int]int64
	// analytics events and property keys by environment (ADR 20261117084505).
	analytics    []memAnalyticsEvent
	propertyKeys map[string]map[string]bool
	// cohorts by ID and their members (ADR 20261119102741 §3).
	cohorts       map[string]*AnalyticsCohort
	cohortMembers map[string][]string
	// rolloutArms are test-set metric-driven rollout arms by flag NUL rule.
	rolloutArms map[string][2]flags.Arm
}

type flagConversionKeyMem struct {
	environment, name, customer string
	day                         time.Time
}

// FlagConversionDay is one folded flag_conversion_daily row.
type FlagConversionDay struct {
	AccountID, EnvironmentID, EventName, CustomerID string
	Day                                             time.Time
	Count, ValueSum                                 int64
	FirstAt, LastAt                                 time.Time
}

func (m *MemStore) flagEventsLocked() *memFlagEvents {
	if m.flagEvents == nil {
		m.flagEvents = &memFlagEvents{usage: map[string][2]int64{}, names: map[string]map[string]bool{}, daily: map[flagConversionKeyMem]*FlagConversionDay{}}
	}
	return m.flagEvents
}

// IngestFlagEvents implements FlagEventStore.
func (m *MemStore) IngestFlagEvents(_ context.Context, in FlagEventIngest) (FlagEventIngestResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fe := m.flagEventsLocked()
	var res FlagEventIngestResult
	kept := make([]FlagConversionEvent, 0, len(in.Events))
	for _, e := range in.Events {
		t, ok := m.platformTenants[e.CustomerID]
		if !ok || t.AccountID != in.Scope.AccountID || t.Status != "active" {
			res.DroppedUnknownCustomer++
			continue
		}
		kept = append(kept, e)
	}
	names := fe.names[in.Scope.EnvironmentID]
	if names == nil {
		names = map[string]bool{}
		fe.names[in.Scope.EnvironmentID] = names
	}
	named := kept[:0:0]
	for _, e := range kept {
		if !names[e.Name] && len(names) < in.MaxNames {
			names[e.Name] = true
		}
		if names[e.Name] {
			named = append(named, e)
			continue
		}
		res.DroppedNameLimit++
	}
	now := in.Now.UTC()
	usageKey := in.Scope.AccountID + "|" + time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	usage := fe.usage[usageKey]
	granted := int64(len(named))
	if left := in.Quota - usage[0]; granted > left {
		granted = max(left, 0)
	}
	res.DroppedQuota = len(named) - int(granted)
	usage[0] += granted
	usage[1] += int64(res.DroppedQuota)
	fe.usage[usageKey] = usage
	res.MonthAccepted, res.MonthDropped = usage[0], usage[1]
	res.Accepted = int(granted)
	for _, e := range named[:granted] {
		at := e.At.UTC()
		k := flagConversionKeyMem{environment: in.Scope.EnvironmentID, name: e.Name, customer: e.CustomerID, day: time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)}
		row := fe.daily[k]
		if row == nil {
			row = &FlagConversionDay{AccountID: in.Scope.AccountID, EnvironmentID: k.environment, EventName: k.name, CustomerID: k.customer, Day: k.day, FirstAt: at, LastAt: at}
			fe.daily[k] = row
		}
		row.Count++
		row.ValueSum += e.Value
		if at.Before(row.FirstAt) {
			row.FirstAt = at
		}
		if at.After(row.LastAt) {
			row.LastAt = at
		}
	}
	if in.Analytics {
		m.memAddAnalyticsLocked(in, named[:granted])
	}
	return res, nil
}

// SweepFlagConversions implements FlagEventStore.
func (m *MemStore) SweepFlagConversions(_ context.Context, now time.Time, retentionDays func(plan string) int, _ int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fe := m.flagEventsLocked()
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	var deleted int64
	for k, row := range fe.daily {
		acct, ok := m.accounts[row.AccountID]
		if !ok {
			continue
		}
		if row.Day.Before(today.AddDate(0, 0, -retentionDays(string(acct.Plan)))) {
			delete(fe.daily, k)
			deleted++
		}
	}
	return deleted, nil
}
