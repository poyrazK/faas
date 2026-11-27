package api

import "time"

const (
	APISandboxReceiverInvitePrefix  = "sbx_winv_"
	APISandboxReceiverOutcomePrefix = "sbx_wres_"
)

type APISandboxReceiverInviteRequest struct {
	ScenarioRevision string `json:"scenario_revision"`
}

type APISandboxReceiverInvite struct {
	RunID            string    `json:"run_id"`
	ScenarioRevision string    `json:"scenario_revision"`
	Status           string    `json:"status"`
	ExpiresAt        time.Time `json:"expires_at"`
	Token            string    `json:"token,omitempty"`
}

type APISandboxReceiverProof struct {
	Proof     string    `json:"proof"`
	ExpiresAt time.Time `json:"expires_at"`
}

type APISandboxReceiverURL struct {
	URL string `json:"url"`
}

type APISandboxReceiverCredentials struct {
	SigningSecret string    `json:"signing_secret"`
	OutcomeToken  string    `json:"outcome_token"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type APISandboxReceiverOutcome struct {
	DeliveryID   string `json:"delivery_id"`
	AppliedCount *int   `json:"applied_count"`
}
