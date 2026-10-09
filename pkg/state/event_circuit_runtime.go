package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Runtime is operational state, separate from acceptance-time retry policy.
type eventCircuitRecord struct {
	AccountID, AppID, SubscriptionID string
	Policy                           api.EventCircuitBreakerPolicy
	Runtime                          eventCircuitRuntime
}
type eventCircuitRuntime struct {
	EvaluatedAt       time.Time `json:"evaluated_at"`
	State             string    `json:"state"`
	Reason            string    `json:"reason"`
	ChangedAt         time.Time `json:"changed_at"`
	BaselineAt        time.Time `json:"baseline_at"`
	CooldownUntil     time.Time `json:"cooldown_until"`
	ProbeUntil        time.Time `json:"probe_until"`
	ProbeSince        time.Time `json:"probe_since"`
	ProbeToken        string    `json:"probe_token"`
	ProbeOutboxID     int64     `json:"probe_outbox_id"`
	NextProbeAt       time.Time `json:"next_probe_at"`
	SuccessfulProbes  int       `json:"successful_probes"`
	RecoveryUntil     time.Time `json:"recovery_until"`
	RecoveryWindow    time.Time `json:"recovery_window"`
	RecoveryCount     int       `json:"recovery_count"`
	HistoryIncomplete bool      `json:"history_incomplete"`
}
type eventCircuitObservation struct {
	Successes, Failures int64
	Incomplete, Sampled bool
	Probe               *PublishedEventRecipientProgress
}

func newEventCircuitRuntime(now time.Time, reason string) eventCircuitRuntime {
	return eventCircuitRuntime{State: "closed", Reason: reason, ChangedAt: now, BaselineAt: now}
}
func circuitOpen(r *eventCircuitRecord, now time.Time, reason string) {
	r.Runtime = eventCircuitRuntime{State: "open", Reason: reason, ChangedAt: now, BaselineAt: now, CooldownUntil: now.Add(time.Duration(r.Policy.CooldownSeconds) * time.Second)}
}
func circuitSince(r *eventCircuitRecord, now time.Time) time.Time {
	since := now.Add(-time.Duration(r.Policy.WindowSeconds) * time.Second)
	if r.Runtime.State == "draining" || r.Runtime.BaselineAt.After(since) {
		since = r.Runtime.BaselineAt
	}
	return since
}
func circuitAdvance(r *eventCircuitRecord, o eventCircuitObservation, now time.Time) {
	s := &r.Runtime
	if o.Sampled {
		s.HistoryIncomplete = o.Incomplete
	}
	if s.State == "half_open" && s.ProbeToken != "" {
		p := o.Probe
		if p != nil && !p.UpdatedAt.Before(s.ProbeSince) {
			if circuitOutcomeFailed(*p) {
				circuitOpen(r, now, "probe_failed")
				return
			}
			if p.State == PublishedEventRecipientEnqueued {
				s.SuccessfulProbes++
			}
			if p.State != "" {
				s.ProbeToken = ""
				s.NextProbeAt = now.Add(api.EventCircuitNeutralProbeDelay)
			}
			if s.SuccessfulProbes >= r.Policy.ProbeSuccesses {
				s.State = "draining"
				s.Reason = "probes_succeeded"
				s.ChangedAt = now
				s.BaselineAt = now
				s.RecoveryUntil = now.Add(time.Duration(r.Policy.RecoverySeconds) * time.Second)
				s.RecoveryWindow = now
				s.RecoveryCount = 0
			}
		} else if !s.ProbeUntil.After(now) {
			circuitOpen(r, now, "probe_timeout")
			return
		}
	}
	switch s.State {
	case "open":
		if !s.CooldownUntil.After(now) {
			s.State = "half_open"
			s.Reason = "cooldown_elapsed"
			s.ChangedAt = now
			s.NextProbeAt = now
		}
	case "closed":
		if o.Sampled && !o.Incomplete && o.Successes+o.Failures >= r.Policy.MinSamples && 100*float64(o.Failures)/float64(o.Successes+o.Failures) >= r.Policy.FailureThresholdPct {
			circuitOpen(r, now, "failure_threshold")
		}
	case "draining":
		if o.Failures > 0 {
			circuitOpen(r, now, "recovery_failed")
			return
		}
		if o.Sampled && !o.Incomplete && !s.RecoveryUntil.After(now) {
			*s = newEventCircuitRuntime(now, "recovery_complete")
		}
	}
}
func circuitRecoveryRate(r *eventCircuitRecord, now time.Time) int {
	if r == nil || r.Runtime.State != "draining" {
		return 0
	}
	step := min(7, max(0, int(now.Sub(r.Runtime.ChangedAt)/api.EventCircuitRampInterval)))
	return min(r.Policy.RecoveryMaxRate, 1<<step)
}
func circuitWaitingReason(r *eventCircuitRecord, now time.Time) (string, time.Time) {
	if r == nil {
		return "", time.Time{}
	}
	switch r.Runtime.State {
	case "open":
		if r.Runtime.CooldownUntil.After(now) {
			return "circuit_open", r.Runtime.CooldownUntil
		}
	case "half_open":
		if r.Runtime.ProbeToken != "" && r.Runtime.ProbeUntil.After(now) {
			return "circuit_probe_wait", r.Runtime.ProbeUntil
		}
		if r.Runtime.NextProbeAt.After(now) {
			return "circuit_probe_wait", r.Runtime.NextProbeAt
		}
	case "draining":
		if r.Runtime.RecoveryWindow.Add(time.Second).After(now) && r.Runtime.RecoveryCount >= circuitRecoveryRate(r, now) {
			return "circuit_recovery_rate_limited", r.Runtime.RecoveryWindow.Add(time.Second)
		}
	}
	return "", time.Time{}
}
func circuitReserve(r *eventCircuitRecord, work *PublishedEventRecipientWork, now time.Time) {
	if r.Runtime.State == "half_open" {
		r.Runtime.ProbeToken = work.ClaimToken
		r.Runtime.ProbeOutboxID = work.OutboxID
		r.Runtime.ProbeSince = now
		r.Runtime.ProbeUntil = work.LeaseUntil
	}
	if r.Runtime.State == "draining" {
		if !r.Runtime.RecoveryWindow.Add(time.Second).After(now) {
			r.Runtime.RecoveryWindow = now
			r.Runtime.RecoveryCount = 0
		}
		r.Runtime.RecoveryCount++
	}
}
func circuitAdmissionReason(r *eventCircuitRecord, claim PublishedEventRoutingClaim, now time.Time) (string, time.Time) {
	if r == nil {
		return "", time.Time{}
	}
	if r.Runtime.State == "open" {
		return "circuit_open", maxCircuitTime(r.Runtime.CooldownUntil, now.Add(api.EventCircuitNeutralProbeDelay))
	}
	if r.Runtime.State == "half_open" && r.Runtime.ProbeToken != claim.ClaimToken {
		return "circuit_probe_wait", maxCircuitTime(r.Runtime.ProbeUntil, now.Add(api.EventCircuitNeutralProbeDelay))
	}
	return "", time.Time{}
}
func maxCircuitTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func circuitResponse(sub string, r *eventCircuitRecord, paused bool, now time.Time) api.EventCircuitBreakerResponse {
	out := api.EventCircuitBreakerResponse{SubscriptionID: canonicalMemUUID(sub), State: "disabled", ManualPaused: paused}
	if r == nil {
		return out
	}
	policy := r.Policy
	out.Enabled = true
	out.Policy = &policy
	out.State = r.Runtime.State
	out.Reason = r.Runtime.Reason
	at := r.Runtime.ChangedAt
	out.ChangedAt = &at
	if r.Runtime.State == "open" {
		at := r.Runtime.CooldownUntil
		out.CooldownUntil = &at
	}
	out.ProbeInFlight = r.Runtime.ProbeToken != "" && r.Runtime.ProbeUntil.After(now)
	out.SuccessfulProbes = r.Runtime.SuccessfulProbes
	out.RecoveryRatePerSecond = circuitRecoveryRate(r, now)
	out.HistoryIncomplete = r.Runtime.HistoryIncomplete
	return out
}
