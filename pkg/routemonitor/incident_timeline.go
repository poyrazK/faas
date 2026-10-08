package routemonitor

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// IncidentTimelineSignal identifies a signal which changed from non-violated
// to violated between adjacent observations.
type IncidentTimelineSignal struct {
	RouteIndex int
	Signal     string
}

// IncidentTimelineEntry projects an evaluation into bounded route and
// customer-impact counts. Identity details and request evidence stay in their
// existing, separately authorized projections.
func IncidentTimelineEntry(r api.RouteMonitorReport) api.RouteMonitorIncidentTimelineEntry {
	entry := api.RouteMonitorIncidentTimelineEntry{
		CheckedAt: r.CheckedAt,
		Coverage:  r.Coverage,
		Status:    r.Status,
		Reason:    r.Reason,
		Routes:    make([]api.RouteMonitorIncidentTimelineRoute, 0, len(r.Routes)),
	}
	if r.Customers != nil {
		entry.CustomerImpact = incidentCustomerImpact(r.CustomerGroupBy, r.Customers.Coverage, r.Customers.ObservedCustomers, r.Customers.ViolatedCustomers, r.Customers.UnknownCustomers)
	}
	for index, finding := range r.Routes {
		route := api.RouteMonitorIncidentTimelineRoute{
			RouteIndex:    index,
			Status:        finding.Status,
			ErrorStatus:   finding.ErrorStatus,
			LatencyStatus: finding.LatencyStatus,
		}
		if r.Customers != nil && index < len(r.Customers.Routes) {
			cohortRoute := r.Customers.Routes[index]
			route.CustomerImpact = incidentCustomerImpact(r.CustomerGroupBy, r.Customers.Coverage, cohortRoute.ObservedCustomers, cohortRoute.ViolatedCustomers, cohortRoute.UnknownCustomers)
		}
		entry.Routes = append(entry.Routes, route)
	}
	return entry
}

// IncidentTimelineEscalation reports newly violated error or latency signals
// between adjacent observations. It intentionally describes counts rather
// than route labels or customer identities for webhook use.
func IncidentTimelineEscalation(previous, current api.RouteMonitorIncidentTimelineEntry) *api.RouteMonitorWebhookEscalation {
	if previous.CheckedAt.IsZero() || !current.CheckedAt.After(previous.CheckedAt) {
		return nil
	}
	signals := NewlyViolatedIncidentTimelineSignals(previous, current)
	if len(signals) == 0 {
		return nil
	}
	routes := map[int]struct{}{}
	for _, signal := range signals {
		routes[signal.RouteIndex] = struct{}{}
	}
	return &api.RouteMonitorWebhookEscalation{
		PreviousCheckedAt:    previous.CheckedAt,
		NewlyViolatedRoutes:  len(routes),
		NewlyViolatedSignals: len(signals),
	}
}

// NewlyViolatedIncidentTimelineSignals returns transitions in stable route
// index order, with error signals before latency signals for each route.
func NewlyViolatedIncidentTimelineSignals(previous, current api.RouteMonitorIncidentTimelineEntry) []IncidentTimelineSignal {
	if previous.CheckedAt.IsZero() || !current.CheckedAt.After(previous.CheckedAt) {
		return nil
	}
	previousRoutes := make(map[int]api.RouteMonitorIncidentTimelineRoute, len(previous.Routes))
	for _, route := range previous.Routes {
		previousRoutes[route.RouteIndex] = route
	}
	currentRoutes := slices.Clone(current.Routes)
	slices.SortFunc(currentRoutes, func(a, b api.RouteMonitorIncidentTimelineRoute) int {
		return a.RouteIndex - b.RouteIndex
	})
	signals := make([]IncidentTimelineSignal, 0, len(currentRoutes)*2)
	for _, route := range currentRoutes {
		prior, exists := previousRoutes[route.RouteIndex]
		if route.ErrorStatus == "violated" && (!exists || prior.ErrorStatus != "violated") {
			signals = append(signals, IncidentTimelineSignal{RouteIndex: route.RouteIndex, Signal: "errors"})
		}
		if route.LatencyStatus == "violated" && (!exists || prior.LatencyStatus != "violated") {
			signals = append(signals, IncidentTimelineSignal{RouteIndex: route.RouteIndex, Signal: "latency"})
		}
	}
	return signals
}

func incidentCustomerImpact(groupBy, coverage string, observed, violated, unknown int64) *api.RouteMonitorCustomerImpact {
	return &api.RouteMonitorCustomerImpact{
		GroupBy:           groupBy,
		Coverage:          coverage,
		ObservedCustomers: observed,
		ViolatedCustomers: violated,
		UnknownCustomers:  unknown,
	}
}

// EnsureIncidentTimeline gives incidents written before timeline support an
// opening baseline when they are read or next updated.
func EnsureIncidentTimeline(i *api.RouteMonitorIncident) {
	if i != nil && len(i.Timeline) == 0 {
		i.Timeline = []api.RouteMonitorIncidentTimelineEntry{IncidentTimelineEntry(i.OpeningReport)}
	}
}

// AppendIncidentTimeline adds one confirmed monitor evaluation, retaining the
// immutable opening baseline plus the newest bounded observations.
func AppendIncidentTimeline(i *api.RouteMonitorIncident, r api.RouteMonitorReport) bool {
	if i == nil {
		return false
	}
	EnsureIncidentTimeline(i)
	entry := IncidentTimelineEntry(r)
	last := i.Timeline[len(i.Timeline)-1]
	if !entry.CheckedAt.After(last.CheckedAt) {
		// A duplicate or stale evaluation must not reorder the incident history.
		return false
	}
	if len(i.Timeline) >= api.RouteMonitorIncidentTimelineMaxEntries {
		keepRecent := api.RouteMonitorIncidentTimelineMaxEntries - 2
		start := len(i.Timeline) - keepRecent
		if start < 1 {
			start = 1
		}
		kept := make([]api.RouteMonitorIncidentTimelineEntry, 1, api.RouteMonitorIncidentTimelineMaxEntries)
		kept[0] = i.Timeline[0]
		kept = append(kept, i.Timeline[start:]...)
		i.Timeline = kept
		i.TimelineTruncated = true
	}
	i.Timeline = append(i.Timeline, entry)
	return true
}

// AppendIncidentEscalation keeps the newest bounded escalation details.
func AppendIncidentEscalation(i *api.RouteMonitorIncident, e api.RouteMonitorIncidentEscalation) bool {
	if i == nil || e.CheckedAt.IsZero() || len(i.Escalations) > 0 && !e.CheckedAt.After(i.Escalations[len(i.Escalations)-1].CheckedAt) {
		return false
	}
	if len(i.Escalations) >= api.RouteMonitorIncidentEscalationMaxEntries {
		keep := api.RouteMonitorIncidentEscalationMaxEntries - 1
		start := len(i.Escalations) - keep
		if start < 1 {
			start = 1
		}
		i.Escalations = append([]api.RouteMonitorIncidentEscalation(nil), i.Escalations[start:]...)
		i.EscalationsTruncated = true
	}
	i.Escalations = append(i.Escalations, e)
	return true
}

func validTimelineTime(at, opened time.Time, closed *time.Time) bool {
	if at.Before(opened) {
		return false
	}
	return closed == nil || !at.After(*closed)
}
