package platformflags

import (
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// Ramp makes a rollout rule advance itself (ADR 20261021080640). The rule's
// rollout is the current stage; apid's ramp worker moves it to the next stage
// once the definition has been unchanged for IntervalSeconds and the flag's
// health routes stayed under Max5xxBasisPoints over that window with at least
// MinRequests requests. On a breach it pauses and, for OnBreach "rollback",
// also returns to the previous stage. A paused ramp waits for an operator to
// clear Paused.
type Ramp struct {
	Stages            []int  `json:"stages"`
	IntervalSeconds   int    `json:"interval_seconds"`
	Max5xxBasisPoints int    `json:"max_5xx_basis_points"`
	MinRequests       int64  `json:"min_requests"`
	OnBreach          string `json:"on_breach"`
	Paused            bool   `json:"paused,omitempty"`
	PausedReason      string `json:"paused_reason,omitempty"`
}

// On-breach behaviors.
const (
	RampPause    = "pause"
	RampRollback = "rollback"
)

// Ramp step outcomes.
const (
	RampHold     = "hold"
	RampAdvance  = "advance"
	RampPaused   = "pause"
	RampRollBack = "rollback"
	RampComplete = "complete"
)

// maxPausedReason bounds the reason the worker or an operator records.
const maxPausedReason = 500

func cloneRamp(r *Ramp) *Ramp {
	if r == nil {
		return nil
	}
	out := *r
	out.Stages = slices.Clone(r.Stages)
	return &out
}

// validateRamp bounds one rule's ramp. The rule's rollout must be one of the
// stages: it is where the ramp currently stands.
func validateRamp(rule Rule) error {
	r := rule.Ramp
	if r == nil {
		return nil
	}
	if rule.Rollout == nil {
		return fmt.Errorf("a ramp needs a rollout: the rollout is the current stage")
	}
	if n := len(r.Stages); n < api.PlatformFlagsMinRampStages || n > api.PlatformFlagsMaxRampStages {
		return fmt.Errorf("a ramp needs %d to %d stages", api.PlatformFlagsMinRampStages, api.PlatformFlagsMaxRampStages)
	}
	for i, stage := range r.Stages {
		if stage < 1 || stage > 10000 || (i > 0 && stage <= r.Stages[i-1]) {
			return fmt.Errorf("ramp stages must ascend strictly within 1..10000 basis points")
		}
	}
	if !slices.Contains(r.Stages, *rule.Rollout) {
		return fmt.Errorf("rollout %d is not one of the ramp stages", *rule.Rollout)
	}
	if r.IntervalSeconds < api.PlatformFlagsMinRampIntervalSeconds || r.IntervalSeconds > api.PlatformFlagsMaxRampIntervalSeconds {
		return fmt.Errorf("ramp interval_seconds must be %d..%d", api.PlatformFlagsMinRampIntervalSeconds, api.PlatformFlagsMaxRampIntervalSeconds)
	}
	if r.Max5xxBasisPoints < 0 || r.Max5xxBasisPoints > 10000 {
		return fmt.Errorf("ramp max_5xx_basis_points must be 0..10000")
	}
	if r.MinRequests < 0 {
		return fmt.Errorf("ramp min_requests must not be negative")
	}
	if r.OnBreach != RampPause && r.OnBreach != RampRollback {
		return fmt.Errorf("ramp on_breach must be %q or %q", RampPause, RampRollback)
	}
	if len(r.PausedReason) > maxPausedReason {
		return fmt.Errorf("ramp paused_reason must be at most %d characters", maxPausedReason)
	}
	return nil
}

// ValidateRamps refuses a ramp on a flag with no health routes: the worker
// would have nothing to judge it by.
func ValidateRamps(spec Spec, def Definition) error {
	for _, rule := range def.Rules {
		if rule.Ramp != nil && len(spec.HealthRoutes) == 0 {
			return fmt.Errorf("rule %q has a ramp, but %s lists no health routes to judge it by", rule.ID, spec.Key)
		}
	}
	return nil
}

// RampHealth is what the worker measured over a ramp's interval. Known is
// false when it could not measure, for example without Prometheus.
type RampHealth struct {
	Known    bool
	Requests int64
	Failures int64
}

// RampStep decides one ramp's next move. It returns the rule to store, the
// outcome and a reason fit for the change reason and the audit log. Hold
// and Complete leave the rule as it is.
func RampStep(rule Rule, health RampHealth) (Rule, string, string) {
	r := rule.Ramp
	if r == nil || rule.Rollout == nil || r.Paused {
		return rule, RampHold, "ramp is paused or absent"
	}
	at := slices.Index(r.Stages, *rule.Rollout)
	if at < 0 {
		return rule, RampHold, "rollout is not a ramp stage"
	}
	if !health.Known {
		return rule, RampHold, "no health data"
	}
	if health.Requests < r.MinRequests || health.Requests == 0 {
		return rule, RampHold, fmt.Sprintf("%d requests, below the %d needed", health.Requests, r.MinRequests)
	}
	rate := int(health.Failures * 10000 / health.Requests)
	next := rule
	next.Ramp = cloneRamp(r)
	if rate > r.Max5xxBasisPoints {
		reason := fmt.Sprintf("5xx rate %d bp over %d requests exceeds %d bp at stage %d bp", rate, health.Requests, r.Max5xxBasisPoints, *rule.Rollout)
		next.Ramp.Paused, next.Ramp.PausedReason = true, reason
		if r.OnBreach == RampRollback && at > 0 {
			previous := r.Stages[at-1]
			next.Rollout = &previous
			return next, RampRollBack, reason + fmt.Sprintf("; rolled back to %d bp", previous)
		}
		return next, RampPaused, reason
	}
	if at == len(r.Stages)-1 {
		return rule, RampComplete, "ramp is at its last stage"
	}
	stage := r.Stages[at+1]
	next.Rollout = &stage
	return next, RampAdvance, fmt.Sprintf("advanced to %d bp: 5xx rate %d bp over %d requests", stage, rate, health.Requests)
}
