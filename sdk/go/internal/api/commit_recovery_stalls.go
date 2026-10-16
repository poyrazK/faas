package api

import (
	"reflect"
	"strings"
	"time"
)

// CommitRecoveryConsumerStall reports sampled routing behavior, never delivery settlement.
type CommitRecoveryConsumerStall struct {
	SubscriptionID string                            `json:"subscription_id"`
	Counts         map[string]int                    `json:"counts"`
	Blockers       []CommitRecoveryPersistentBlocker `json:"blockers"`
}

// CommitRecoveryPersistentBlocker measures this watch's confirmed sampling span.
type CommitRecoveryPersistentBlocker struct {
	Query           EventRecipientBlockerQuery `json:"query"`
	WaitingReason   string                     `json:"waiting_reason"`
	State           string                     `json:"state"`
	FirstObservedAt time.Time                  `json:"first_observed_at"`
	LastObservedAt  time.Time                  `json:"last_observed_at"`
	Samples         int                        `json:"samples"`
	MonitoredMS     int64                      `json:"monitored_ms"`
}

type commitRecoveryStallSample struct {
	observation        *CommitRecoveryDiagnosticObservation
	first              time.Time
	started, last      time.Time
	samples            int
	advancing, changed bool
}

type commitRecoveryStallTracker struct {
	samples map[int]*commitRecoveryStallSample
}

func (t *commitRecoveryStallTracker) observe(out CommitRecoveryStatusResult, maxAge, threshold time.Duration) {
	if threshold == 0 {
		return
	}
	if t.samples == nil {
		t.samples = map[int]*commitRecoveryStallSample{}
	}
	now, wall := time.Now(), time.Now().UTC()
	for i, row := range out.Items {
		previous := t.samples[i]
		current := diagnosticObservation(row.Diagnostics, row)
		if current == nil || diagnosticFreshness(row, wall, maxAge) != "fresh" {
			delete(t.samples, i)
			continue
		}
		if previous != nil && (previous.observation.ObservedAt.After(wall) || wall.Sub(previous.observation.ObservedAt) > maxAge) {
			delete(t.samples, i)
			previous = nil
		}
		if row.Diagnostics.Retained {
			continue
		}
		sample := &commitRecoveryStallSample{observation: current, first: current.ObservedAt, started: now, last: now, samples: 1}
		if previous != nil && current.ObservedAt.After(previous.observation.ObservedAt) {
			if reflect.DeepEqual(diagnosticRoutingFields(current), diagnosticRoutingFields(previous.observation)) {
				sample.first, sample.started, sample.samples = previous.first, previous.started, previous.samples+1
			} else {
				sample.changed = true
				sample.advancing = (previous.observation.RoutingState == "pending" && current.RoutingState == "processing") || (previous.observation.Status == "available" && current.Status == "not_waiting" && current.RoutingState == "enqueued")
			}
		} else if previous != nil {
			delete(t.samples, i)
			continue
		}
		t.samples[i] = sample
	}
}

func persistentRoutingBlocker(reason string) bool {
	switch reason {
	case "subscription_paused", "subscription_rate_limited", "circuit_open", "circuit_probe_wait", "circuit_recovery_rate_limited", "ordering_blocked", "capacity_consumer", "capacity_app", "capacity_account", "retry_backoff":
		return true
	default:
		return false
	}
}

func (t *commitRecoveryStallTracker) summarize(out CommitRecoveryStatusResult, maxAge, threshold time.Duration) []CommitRecoveryConsumerStall {
	if threshold == 0 || out.DiagnosticsProgress == nil {
		return nil
	}
	groups := map[string]*CommitRecoveryConsumerStall{}
	result := make([]CommitRecoveryConsumerStall, 0, len(out.DiagnosticsProgress.Consumers))
	for _, consumer := range out.DiagnosticsProgress.Consumers {
		group := &CommitRecoveryConsumerStall{SubscriptionID: consumer.SubscriptionID, Counts: map[string]int{"insufficient_evidence": 0, "advancing": 0, "routing_changed": 0, "not_blocked": 0, "observing": 0, "stalled": 0}, Blockers: []CommitRecoveryPersistentBlocker{}}
		groups[consumer.SubscriptionID] = group
	}
	now := time.Now().UTC()
	for i, row := range out.Items {
		if !diagnosticTarget(row) {
			continue
		}
		group := groups[strings.ToLower(row.Attempt.SubscriptionID)]
		sample := t.samples[i]
		if sample == nil || diagnosticFreshness(row, now, maxAge) != "fresh" || !row.Diagnostics.ObservedAt.Equal(sample.observation.ObservedAt) {
			delete(t.samples, i)
			group.Counts["insufficient_evidence"]++
			continue
		}
		if sample.changed {
			group.Counts["routing_changed"]++
		}
		if sample.advancing {
			group.Counts["advancing"]++
		}
		observation := sample.observation
		if observation.Routing == nil || !persistentRoutingBlocker(observation.Routing.WaitingReason) {
			group.Counts["not_blocked"]++
			continue
		}
		state := "observing"
		duration := sample.last.Sub(sample.started)
		if sample.samples >= 2 && duration >= threshold {
			state = "stalled"
		}
		group.Counts[state]++
		group.Blockers = append(group.Blockers, CommitRecoveryPersistentBlocker{Query: observation.Query, WaitingReason: observation.Routing.WaitingReason, State: state, FirstObservedAt: sample.first, LastObservedAt: observation.ObservedAt, Samples: sample.samples, MonitoredMS: duration.Milliseconds()})
	}
	for _, consumer := range out.DiagnosticsProgress.Consumers {
		result = append(result, *groups[consumer.SubscriptionID])
	}
	return result
}
