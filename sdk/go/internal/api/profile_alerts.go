package api

import "time"

// ProfileRouteAlertPayload is advisory evidence delivered through app webhooks.
// ApplicationFrames are application-level hotspots, not proof of route causality.
type ProfileRouteAlertPayload struct {
	ComparisonURL        string                 `json:"comparison_url,omitempty"`
	Version              int                    `json:"version"`
	AppID                string                 `json:"app_id"`
	DeploymentID         string                 `json:"deployment_id"`
	BaselineDeploymentID string                 `json:"baseline_deployment_id"`
	PolicyRevision       int64                  `json:"policy_revision"`
	IncidentID           string                 `json:"incident_id"`
	Status               string                 `json:"status"`
	Source               string                 `json:"source"`
	CheckedAt            time.Time              `json:"checked_at"`
	Baseline             ProfileQuery           `json:"baseline"`
	Candidate            ProfileQuery           `json:"candidate"`
	RouteCheck           ProfileRouteRegression `json:"route_check"`
	ApplicationFrames    []ProfileCallPathFrame `json:"application_frames,omitempty"`
	EvidencePath         string                 `json:"evidence_path"`
	InvestigationPath    string                 `json:"investigation_path,omitempty"`
}
