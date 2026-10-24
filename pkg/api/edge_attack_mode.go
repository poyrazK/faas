package api

import (
	"fmt"
	"time"
)

// Attack mode (ADR-20261021220456) is one switch per app that challenges
// every request (ADR-831 amendment 7) for a bounded time. Browsers solve the
// challenge once and continue; verified crawlers (ADR-973) and requests with
// a verified API key, consumer key or JWT pass straight through; anything
// else gets 403 challenge_required. It is carried by one app-wide challenge
// rule the switch owns.

// EdgeRuleManagedByAttackMode is EdgeRuleResponse.ManagedBy on the app's
// attack-mode rule.
const EdgeRuleManagedByAttackMode = "attack_mode"

// EdgeAttackModeRuleName names the attack-mode rule in rule listings.
const EdgeAttackModeRuleName = "Attack mode"

// EnableEdgeAttackModeRequest is the body of
// PUT /v1/apps/{slug}/edge-protection/attack-mode.
type EnableEdgeAttackModeRequest struct {
	// DurationSeconds is how long attack mode stays on from now. Zero
	// applies EdgeAttackModeDefaultSeconds. Switching it on while it is
	// already on restarts the clock with this duration.
	DurationSeconds int `json:"duration_seconds,omitempty"`
}

// Validate applies the default and bounds, mutating the receiver.
func (r *EnableEdgeAttackModeRequest) Validate() *Problem {
	if r.DurationSeconds == 0 {
		r.DurationSeconds = EdgeAttackModeDefaultSeconds
	}
	if r.DurationSeconds < MinEdgeAttackModeSeconds || r.DurationSeconds > MaxEdgeAttackModeSeconds {
		return ErrValidation(fmt.Sprintf(
			"duration_seconds must be in %d..%d (got %d); attack mode always ends on its own",
			MinEdgeAttackModeSeconds, MaxEdgeAttackModeSeconds, r.DurationSeconds))
	}
	return nil
}

// EdgeAttackModeStatus answers GET, PUT and DELETE on
// /v1/apps/{slug}/edge-protection/attack-mode, and is the attack_mode
// section of the edge-protection summary.
type EdgeAttackModeStatus struct {
	// Active reports that attack mode is on now.
	Active bool `json:"active"`
	// RuleID is the app-wide challenge rule that carries attack mode. It
	// stays set after attack mode ran out, until it is switched on again
	// or off.
	RuleID string `json:"rule_id,omitempty"`
	// StartedAt is when attack mode was switched on; extending it keeps
	// this time.
	StartedAt *time.Time `json:"started_at,omitempty"`
	// ExpiresAt is when attack mode ends, or ended.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Difficulty and PassTTLSeconds are the challenge attack mode serves.
	Difficulty     int `json:"difficulty,omitempty"`
	PassTTLSeconds int `json:"pass_ttl_seconds,omitempty"`
}
