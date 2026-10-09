package api

import "time"

// PeriodicProfilePolicy opts into advisory checks of live, completed deployments.
type PeriodicProfilePolicy struct {
	IntervalSeconds int `json:"interval_seconds"`
	Confirmations   int `json:"confirmations"`
}

type ProfilePeriodicObservation struct {
	ID              string                  `json:"id"`
	Status          string                  `json:"status"`
	Reason          string                  `json:"reason"`
	CheckedAt       time.Time               `json:"checked_at"`
	Baseline        ProfileQuery            `json:"baseline"`
	Candidate       ProfileQuery            `json:"candidate"`
	RouteCheck      *ProfileRouteRegression `json:"route_check,omitempty"`
	IncidentID      string                  `json:"incident_id,omitempty"`
	Transition      string                  `json:"transition,omitempty"`
	InvestigationID string                  `json:"investigation_id,omitempty"`
	ComparisonURL   string                  `json:"comparison_url,omitempty"`
}

type ProfilePeriodicMonitor struct {
	Active         bool                          `json:"active"`
	ID             string                        `json:"id"`
	AppID          string                        `json:"app_id"`
	DeploymentID   string                        `json:"deployment_id"`
	Scope          string                        `json:"scope"`
	Route          string                        `json:"route"`
	PolicyRevision int64                         `json:"policy_revision"`
	Config         ProfileDeploymentPolicyConfig `json:"config"`
	Baseline       *ProfileQuery                 `json:"baseline,omitempty"`
	Candidate      ProfileQuery                  `json:"candidate"`
	Attempts       int                           `json:"attempts"`
	NextAttemptAt  time.Time                     `json:"next_attempt_at"`
	History        []ProfilePeriodicObservation  `json:"history"`
}

type ListProfilePeriodicMonitorsResponse struct {
	Monitors []ProfilePeriodicMonitor `json:"monitors"`
}
