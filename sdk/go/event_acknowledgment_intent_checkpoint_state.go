package faas

import (
	"strconv"
	"time"
)

// EventAcknowledgmentIntentConfirmation is live process-local proof metadata.
// Durations use nanoseconds in JSON. It is never part of the saved checkpoint.
type EventAcknowledgmentIntentConfirmation struct {
	Metric          string        `json:"metric"`
	Phase           string        `json:"phase"`
	Reason          string        `json:"reason"`
	FreshSamples    int           `json:"fresh_samples"`
	RequiredSamples int           `json:"required_samples"`
	Elapsed         time.Duration `json:"elapsed_ns"`
	Required        time.Duration `json:"required_ns"`
	Remaining       time.Duration `json:"remaining_ns"`
}

type eventIntentCheckpointProof struct {
	first   time.Time // Includes this process's monotonic clock; never serialized.
	samples int
	mode    string
	resume  bool
}

func (p *eventIntentCheckpointProof) reset() {
	p.first, p.samples, p.mode = time.Time{}, 0, ""
}

func resetEventIntentCheckpointProofs(c *EventAcknowledgmentIntentCheckpoint) []eventIntentCheckpointProof {
	proofs := make([]eventIntentCheckpointProof, len(c.Conditions))
	for i := range c.Conditions {
		c.Conditions[i].State = "unknown"
		proofs[i].resume = c.Conditions[i].IncidentID != ""
	}
	return proofs
}

func eventIntentCheckpointConfirmation(c EventAcknowledgmentIntentCheckpointCondition, p eventIntentCheckpointProof, now time.Time, sustain time.Duration, reason string, blocked bool) EventAcknowledgmentIntentConfirmation {
	phase, elapsed, remaining := c.State, time.Duration(0), time.Duration(0)
	if p.resume {
		phase = "resume_pending"
	}
	if !p.first.IsZero() {
		elapsed = min(sustain, max(0, now.Sub(p.first)))
		remaining = max(0, sustain-elapsed)
		phase = p.mode + "_pending"
		if blocked {
			phase = "notification_blocked"
		}
	}
	return EventAcknowledgmentIntentConfirmation{c.Metric, phase, reason, p.samples, 2, elapsed, sustain, remaining}
}

func eventIntentCheckpointNotification(c *EventAcknowledgmentIntentCheckpoint, condition *EventAcknowledgmentIntentCheckpointCondition, transition string, at time.Time) EventAcknowledgmentIntentNotification {
	c.Sequence++
	if transition == "opened" {
		condition.IncidentID = c.Session + "-" + strconv.FormatInt(c.Sequence, 10)
	}
	a := EventAcknowledgmentIntentNotification{c.Sequence, condition.IncidentID, condition.Metric, transition, at.UTC(), *condition.Value, condition.Threshold}
	c.Pending = append(c.Pending, a)
	return a
}

func applyEventIntentCheckpoint(c *EventAcknowledgmentIntentCheckpoint, condition *EventAcknowledgmentIntentCheckpointCondition, proof *eventIntentCheckpointProof, now time.Time) (string, bool, *EventAcknowledgmentIntentNotification) {
	active, breach := condition.IncidentID != "", *condition.Value >= condition.Threshold
	candidate := breach
	if active {
		candidate = proof.resume || !breach
	}
	if !candidate {
		proof.reset()
		condition.State = "clear"
		if active {
			condition.State = "firing"
			return "threshold_not_cleared", false, nil
		}
		return "threshold_not_breached", false, nil
	}
	mode := "warning"
	if active && !breach {
		mode = "recovery"
	}
	if mode != proof.mode {
		proof.reset()
	}
	proof.mode = mode
	if proof.first.IsZero() {
		proof.first = now
	}
	proof.samples = min(2, proof.samples+1)
	condition.State = "unknown"
	if active && !proof.resume {
		condition.State = "firing"
	}
	if proof.samples < 2 || now.Sub(proof.first) < c.Policy.Sustain {
		return mode + "_confirmation_pending", false, nil
	}
	if !active {
		reserved := 0
		for _, other := range c.Conditions {
			if other.IncidentID != "" {
				reserved++
			}
		}
		if len(c.Pending)+reserved+2 > EventAcknowledgmentIntentOutboxMax {
			return "notification_outbox_full", true, nil
		}
		if c.Sequence > EventAcknowledgmentIntentSequenceMax-int64(reserved)-2 {
			return "notification_sequence_exhausted", true, nil
		}
		alert := eventIntentCheckpointNotification(c, condition, "opened", now)
		condition.State, proof.resume = "firing", false
		proof.reset()
		return "threshold_not_cleared", false, &alert
	}
	if !breach {
		// Opening reserved one outbox/sequence slot for this resolution.
		alert := eventIntentCheckpointNotification(c, condition, "resolved", now)
		condition.IncidentID, condition.State, proof.resume = "", "clear", false
		proof.reset()
		return "threshold_not_breached", false, &alert
	}
	condition.State, proof.resume = "firing", false
	proof.reset()
	return "incident_reconfirmed", false, nil
}

func observeEventIntentCheckpoint(c *EventAcknowledgmentIntentCheckpoint, proofs []eventIntentCheckpointProof, health EventAcknowledgmentIntentHealth, reason string, now time.Time) ([]EventAcknowledgmentIntentNotification, []EventAcknowledgmentIntentConfirmation) {
	alerts := []EventAcknowledgmentIntentNotification{}
	confirmations := make([]EventAcknowledgmentIntentConfirmation, 0, len(c.Conditions))
	for i := range c.Conditions {
		condition, proof := &c.Conditions[i], &proofs[i]
		if reason != "" {
			proof.reset()
			condition.State, proof.resume = "unknown", condition.IncidentID != ""
			confirmations = append(confirmations, eventIntentCheckpointConfirmation(*condition, *proof, now, c.Policy.Sustain, reason, false))
			continue
		}
		value := health.Due
		if condition.Metric == "stalled_recovery" {
			value = health.Stalled
		}
		condition.Value, condition.ObservedAt = &value, cloneEventIntentTime(&health.ObservedAt)
		why, blocked, alert := applyEventIntentCheckpoint(c, condition, proof, now)
		if alert != nil {
			alerts = append(alerts, *alert)
		}
		confirmations = append(confirmations, eventIntentCheckpointConfirmation(*condition, *proof, now, c.Policy.Sustain, why, blocked))
	}
	return alerts, confirmations
}
