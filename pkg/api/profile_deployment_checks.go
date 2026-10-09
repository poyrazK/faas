package api

import (
	"encoding/json"
	"errors"
	"time"
)

type ProfileDeploymentPolicyConfig struct {
	CanaryGate             *ProfileCanaryGatePolicy `json:"canary_gate,omitempty"`
	Periodic               *PeriodicProfilePolicy   `json:"periodic,omitempty"`
	NotifyRouteRegressions bool                     `json:"notify_route_regressions,omitempty"`
	Enabled                bool                     `json:"enabled"`
	Runtime                string                   `json:"runtime"`
	WindowSeconds          int                      `json:"window_seconds"`
	WarmupSeconds          int                      `json:"warmup_seconds"`
	Options                ProfileRegressionOptions `json:"options"`
}

func (c *ProfileDeploymentPolicyConfig) UnmarshalJSON(body []byte) error {
	type config ProfileDeploymentPolicyConfig
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	for _, name := range []string{"enabled", "runtime", "window_seconds", "warmup_seconds", "options"} {
		if value, ok := fields[name]; !ok || string(value) == "null" {
			return errors.New("automatic profiling policy requires every capture setting and threshold")
		}
	}
	var out config
	if err := json.Unmarshal(body, &out); err != nil {
		return err
	}
	*c = ProfileDeploymentPolicyConfig(out)
	return nil
}

type ProfileDeploymentPolicy struct {
	AppID     string                        `json:"app_id"`
	Revision  int64                         `json:"revision"`
	Config    ProfileDeploymentPolicyConfig `json:"config"`
	UpdatedAt *time.Time                    `json:"updated_at,omitempty"`
}

type SaveProfileDeploymentPolicyRequest struct {
	ExpectedRevision *int64                        `json:"expected_revision"`
	Config           ProfileDeploymentPolicyConfig `json:"config"`
}

// A deployment receipt pins the comparison windows and policy used by the
// background worker. Sample data lives only in the profiling backend.
type ProfileDeploymentCheck struct {
	DeploymentID    string                        `json:"deployment_id"`
	AppID           string                        `json:"app_id"`
	Scope           string                        `json:"scope"`
	PolicyRevision  int64                         `json:"policy_revision"`
	Config          ProfileDeploymentPolicyConfig `json:"config"`
	Baseline        *ProfileQuery                 `json:"baseline,omitempty"`
	Candidate       ProfileQuery                  `json:"candidate"`
	Status          string                        `json:"status"`
	Reason          string                        `json:"reason"`
	Attempts        int                           `json:"attempts"`
	NextAttemptAt   *time.Time                    `json:"next_attempt_at,omitempty"`
	CompletedAt     *time.Time                    `json:"completed_at,omitempty"`
	InvestigationID string                        `json:"investigation_id,omitempty"`
	ComparisonURL   string                        `json:"comparison_url,omitempty"`
	CreatedAt       time.Time                     `json:"created_at"`
}

type ListProfileDeploymentChecksResponse struct {
	Checks []ProfileDeploymentCheck `json:"checks"`
}

// ProfileCanaryHistoryPage is a bounded newest-first page of retained
// report-only stage assessments for one deployment.
type ProfileCanaryHistoryPage struct {
	AppID        string                `json:"app_id"`
	DeploymentID string                `json:"deployment_id"`
	Entries      []CanaryProfileSignal `json:"entries"`
	NextCursor   string                `json:"next_cursor,omitempty"`
}
