package state

import (
	"context"
	"time"
)

// AlertEpisode is one firing period of an alert rule (ADR-20261022193512).
// SetAlertRuleState opens it when the rule turns firing and resolves it when
// the rule turns ok. AppID, RuleName, Metric and RuleState are the rule's,
// read with the episode.
type AlertEpisode struct {
	ID         string
	RuleID     string
	AccountID  string
	AppID      string // empty for an account-wide rule
	RuleName   string
	Metric     AlertMetric
	RuleState  AlertState
	OpenedAt   time.Time
	ResolvedAt *time.Time
}

// RouteMonitorIncidentSummary is a route monitor incident without its saved
// reports and evidence.
type RouteMonitorIncidentSummary struct {
	ID           string
	AppID        string
	DeploymentID string
	Status       string
	OpenedAt     time.Time
	ClosedAt     *time.Time
}

// IncidentSourceStore reads what the unified incident list projects.
type IncidentSourceStore interface {
	// ListAlertEpisodes returns the account's open episodes plus those
	// resolved at or after resolvedSince, newest opened first, at most
	// limit. A non-empty appID keeps that app's rules only.
	ListAlertEpisodes(ctx context.Context, accountID, appID string, resolvedSince time.Time, limit int) ([]AlertEpisode, error)
	// AlertEpisodeByID returns ErrNotFound for another account's episode.
	AlertEpisodeByID(ctx context.Context, accountID, id string) (AlertEpisode, error)
	// ListRouteMonitorIncidentSummaries returns the account's open route
	// monitor incidents plus those closed at or after closedSince, newest
	// opened first, at most limit. A non-empty appID keeps that app only.
	ListRouteMonitorIncidentSummaries(ctx context.Context, accountID, appID string, closedSince time.Time, limit int) ([]RouteMonitorIncidentSummary, error)
	// RouteMonitorIncidentSummaryByID returns ErrNotFound for another
	// account's incident.
	RouteMonitorIncidentSummaryByID(ctx context.Context, accountID, id string) (RouteMonitorIncidentSummary, error)
}
