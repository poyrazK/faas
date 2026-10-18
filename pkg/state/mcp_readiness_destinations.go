package state

import (
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Private, durable ownership for each destination. The event's legacy fields
// remain an aggregate projection for existing clients and suppression checks.
type readinessDestinationDelivery struct {
	Provider       string     `json:"provider,omitempty"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	AvailableAt    time.Time  `json:"available_at"`
	LeaseToken     string     `json:"lease_token,omitempty"`
	LeaseUntil     time.Time  `json:"lease_until,omitempty"`
	DeliveryID     string     `json:"delivery_id,omitempty"`
	LastAttemptAt  *time.Time `json:"last_attempt_at,omitempty"`
	LastStatusCode int        `json:"last_status_code,omitempty"`
}
type readinessDestinationCandidate struct {
	Event           readinessIncidentEventRecord
	Route           readinessIncidentRouting
	Destination     string
	Head            bool
	WaitingPrimary  bool
	ReceiverBlocked bool
}

func readinessDestinationTerminal(s string) bool {
	return readinessNotificationTerminal(s) || s == "superseded"
}
func prepareReadinessDestinations(e *readinessIncidentEventRecord, route readinessIncidentRouting) {
	if e.Destinations == nil {
		e.Destinations = map[string]readinessDestinationDelivery{}
	}
	names := []string{"primary"}
	if route.Activated {
		names = []string{"fallback"}
		if e.Kind == "alert.resolved" && route.PrimaryInformed {
			names = append(names, "primary")
		}
	}
	for _, name := range names {
		target := route.Primary
		if name == "fallback" {
			target = route.Fallback
		}
		if existing, ok := e.Destinations[name]; ok {
			existing.Provider = readinessProvider(target)
			e.Destinations[name] = existing
			continue
		}
		d := readinessDestinationDelivery{Provider: readinessProvider(target), Status: "pending", AvailableAt: e.At}
		delivered := e.Routing.PrimaryDelivered
		d.Attempts = e.Routing.PrimaryAttempts
		if name == "fallback" {
			delivered, d.Attempts = e.Routing.FallbackDelivered, e.Routing.FallbackAttempts
		}
		if delivered {
			d.Status = "delivered"
		}
		// Hydrate legacy claims without losing their fences or retry deadlines.
		last := e.Routing.LastDestination
		if last == "" {
			last = "primary"
			if name == "primary" {
				d.Attempts = e.Attempts
			}
		}
		if last == name && e.Attempts > 0 {
			d.AvailableAt, d.LeaseToken, d.LeaseUntil = e.AvailableAt, e.LeaseToken, e.LeaseUntil
			d.DeliveryID, d.LastAttemptAt, d.LastStatusCode = e.DeliveryID, e.LastAttemptAt, e.LastStatusCode
			if e.Status == "failed" && !delivered {
				d.Status = "failed"
			}
		}
		e.Destinations[name] = d
	}
	if route.Activated && e.Kind == "alert.fired" {
		if d, ok := e.Destinations["primary"]; ok && !readinessDestinationTerminal(d.Status) {
			d.Status, d.LeaseToken, d.LeaseUntil = "superseded", "", time.Time{}
			e.Destinations["primary"] = d
		}
	}
}

// A potential future fallback reserves its FIFO position before failover, so
// a later recovery can never overtake a failure still retrying at primary.
func readinessDestinationPlan(rule AlertRule, policy MCPReadinessFallbackConfig, events []readinessIncidentEventRecord, incidents map[string]readinessIncident) []readinessDestinationCandidate {
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	routes := map[string]readinessIncidentRouting{}
	seen := map[string]bool{}
	seenURLs := map[string]bool{}
	primaryBlocked := false
	out := []readinessDestinationCandidate{}
	for _, original := range events {
		if readinessNotificationTerminal(original.Status) {
			continue
		}
		e := cloneReadiness(original)
		route, ok := routes[e.IncidentID]
		if !ok {
			route = incidents[e.IncidentID].Routing
		}
		initializeReadinessRoute(rule, policy, &route, e, primaryBlocked)
		routes[e.IncidentID] = route
		prepareReadinessDestinations(&e, route)
		for _, dest := range []string{"fallback", "primary"} {
			d, ok := e.Destinations[dest]
			if !ok || readinessDestinationTerminal(d.Status) {
				continue
			}
			target := route.Primary
			if target.URL == "" {
				target.URL = rule.WebhookURL
			}
			if dest == "fallback" {
				target = route.Fallback
			}
			url := readinessReceiverIdentity(target)
			out = append(out, readinessDestinationCandidate{Event: e, Route: route, Destination: dest, Head: !seen[dest], ReceiverBlocked: seenURLs[url]})
			seen[dest], seenURLs[url] = true, true
		}
		if !route.Activated && route.AfterFailures > 0 {
			out = append(out, readinessDestinationCandidate{Event: e, Route: route, Destination: "fallback", Head: !seen["fallback"], WaitingPrimary: true})
			seen["fallback"] = true
			seenURLs[readinessReceiverIdentity(route.Fallback)] = true
		}
		if d, ok := e.Destinations["primary"]; ok && e.Kind == "alert.resolved" && route.Activated && route.PrimaryInformed && !readinessDestinationTerminal(d.Status) {
			primaryBlocked = true
		}
	}
	return out
}
func readyReadinessDestination(c readinessDestinationCandidate, now time.Time) bool {
	d := c.Event.Destinations[c.Destination]
	return c.Head && !c.WaitingPrimary && !c.ReceiverBlocked && !now.Before(c.Event.At) && !now.Before(d.AvailableAt) && (d.LeaseToken == "" || !now.Before(d.LeaseUntil))
}
func projectReadinessDestinations(e *readinessIncidentEventRecord) {
	if len(e.Destinations) == 0 {
		return
	}
	e.LeaseToken, e.LeaseUntil = "", time.Time{}
	e.AvailableAt = time.Time{}
	pending, failed, abandoned := false, false, false
	for _, name := range []string{"primary", "fallback"} {
		d, ok := e.Destinations[name]
		if !ok {
			continue
		}
		if d.Status == "abandoned" {
			abandoned = true
		}
		if readinessDestinationTerminal(d.Status) {
			continue
		}
		if d.Status == "failed" {
			failed = true
		} else {
			pending = true
		}
		if e.AvailableAt.IsZero() || d.AvailableAt.Before(e.AvailableAt) {
			e.AvailableAt = d.AvailableAt
		}
		if d.LeaseToken != "" && (e.LeaseToken == "" || d.LeaseUntil.After(e.LeaseUntil)) {
			e.LeaseToken, e.LeaseUntil = d.LeaseToken, d.LeaseUntil
		}
	}
	switch {
	case pending:
		e.Status = "pending"
	case failed:
		e.Status = "failed"
	case abandoned:
		e.Status = "abandoned"
	default:
		e.Status = "delivered"
	}
}
func claimReadinessDestination(rule AlertRule, policy MCPReadinessFallbackConfig, c *readinessDestinationCandidate, now time.Time) MCPReadinessDeliveryClaim {
	e, route := &c.Event, &c.Route
	d := e.Destinations[c.Destination]
	if d.LeaseToken != "" && c.Destination == "primary" && e.Kind == "alert.fired" {
		route.PrimaryInformed = true
	}
	dispatch := selectReadinessDestination(rule, policy, route, e, c.Destination)
	e.Attempts++
	e.Version++
	d.Attempts++
	d.Status = "pending"
	d.LeaseToken = uuid.NewString()
	d.LeaseUntil = now.Add(MCPReadinessDeliveryLease)
	at := now
	d.LastAttemptAt = &at
	e.LastAttemptAt = &at
	delivery := readinessRouteDelivery(rule, *e, now)
	d.DeliveryID, e.DeliveryID = delivery.ID, delivery.ID
	e.Destinations[c.Destination] = d
	projectReadinessDestinations(e)
	return MCPReadinessDeliveryClaim{Provider: d.Provider, EventID: e.ID, Token: d.LeaseToken, LeaseUntil: d.LeaseUntil, Delivery: delivery, Rule: dispatch}
}
func readinessDestinationForToken(e readinessIncidentEventRecord, token string, now time.Time) (string, readinessDestinationDelivery, error) {
	for _, name := range []string{"primary", "fallback"} {
		d := e.Destinations[name]
		if token != "" && d.LeaseToken == token && now.Before(d.LeaseUntil) && !readinessDestinationTerminal(d.Status) && !readinessNotificationTerminal(e.Status) {
			return name, d, nil
		}
	}
	return "", readinessDestinationDelivery{}, ErrConflict
}
func finishReadinessDestination(route *readinessIncidentRouting, e *readinessIncidentEventRecord, name string, delivery AlertDelivery, now time.Time, cooldown int) {
	d := e.Destinations[name]
	d.LeaseToken, d.LeaseUntil = "", time.Time{}
	d.LastStatusCode = delivery.LastStatusCode
	d.Status = "failed"
	d.AvailableAt = now.Add(time.Duration(cooldown) * time.Minute)
	if delivery.Status == AlertDeliveryDelivered {
		d.Status = "delivered"
	}
	e.Destinations[name] = d
	if name == "primary" {
		if delivery.Status == AlertDeliveryDelivered {
			e.Routing.PrimaryDelivered = true
			if e.Kind == "alert.fired" {
				route.PrimaryInformed = true
			}
		} else {
			e.Routing.PrimaryFailures++
			if !route.Activated && route.AfterFailures > 0 && e.Routing.PrimaryFailures >= route.AfterFailures {
				route.Activated = true
			}
		}
	} else if delivery.Status == AlertDeliveryDelivered {
		e.Routing.FallbackDelivered = true
	}
	// Clear aggregate legacy ownership before hydrating a newly required target.
	// Otherwise a just-finished lease could be copied into that target.
	e.LeaseToken, e.LeaseUntil = "", time.Time{}
	prepareReadinessDestinations(e, *route)
	projectReadinessDestinations(e)
	e.LastStatusCode = delivery.LastStatusCode
	e.Version++
}
func readinessDestinationViews(e readinessIncidentEventRecord, now time.Time) []api.MCPReadinessDestinationDelivery {
	out := []api.MCPReadinessDestinationDelivery{}
	for _, name := range []string{"primary", "fallback"} {
		d, ok := e.Destinations[name]
		if !ok {
			continue
		}
		v := api.MCPReadinessDestinationDelivery{Provider: d.Provider, Destination: name, Status: d.Status, AttemptCount: d.Attempts, LastStatusCode: d.LastStatusCode, LastDeliveryID: d.DeliveryID}
		if !readinessDestinationTerminal(d.Status) && !readinessNotificationTerminal(e.Status) {
			at := d.AvailableAt
			v.NextAttemptAt = &at
			if d.LeaseToken != "" && now.Before(d.LeaseUntil) {
				until := d.LeaseUntil
				v.Status = "delivering"
				v.LeaseUntil = &until
				v.NextAttemptAt = &until
			}
		} else if !readinessDestinationTerminal(d.Status) && readinessNotificationTerminal(e.Status) {
			v.Status = e.Status
		}
		if d.Provider == "pagerduty" {
			v.ProviderStatus = v.Status
			switch v.Status {
			case "delivered":
				v.ProviderStatus = "accepted"
			case "failed":
				v.ProviderStatus = "retrying"
			}
		}
		out = append(out, v)
	}
	return out
}
