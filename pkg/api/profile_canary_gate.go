package api

import "time"

// Presence enables a stage-specific gate; absence preserves advisory checks.
type ProfileCanaryGatePolicy struct {
	Confirmations  int    `json:"confirmations"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	OnTimeout      string `json:"on_timeout"`
	AutoRollback   bool   `json:"auto_rollback"`
}

type ProfileGateRouteStreak struct {
	Route  string `json:"route"`
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type ProfileCanaryGateState struct {
	Policy        ProfileCanaryGatePolicy  `json:"policy"`
	Status        string                   `json:"status"`
	Reason        string                   `json:"reason"`
	Deadline      time.Time                `json:"deadline"`
	Windows       int                      `json:"windows"`
	LastWindowEnd *time.Time               `json:"last_window_end,omitempty"`
	Streaks       []ProfileGateRouteStreak `json:"streaks"`
	NextCandidate *ProfileQuery            `json:"next_candidate,omitempty"`
}

type ProfileGateOverride struct {
	ExpectedPolicyRevision int64  `json:"expected_policy_revision"`
	Reason                 string `json:"reason"`
}

type ProfileCanaryGateDecision struct {
	Status              string               `json:"status"`
	Reason              string               `json:"reason"`
	PolicyRevision      int64                `json:"policy_revision"`
	CanaryStep          int                  `json:"canary_step"`
	CanaryStepStartedAt *time.Time           `json:"canary_step_started_at,omitempty"`
	Deadline            *time.Time           `json:"deadline,omitempty"`
	OnTimeout           string               `json:"on_timeout,omitempty"`
	AutoRollback        bool                 `json:"auto_rollback"`
	StableDeploymentID  string               `json:"stable_deployment_id,omitempty"`
	Signal              *CanaryProfileSignal `json:"signal,omitempty"`
}
