package scaleup

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const scaleDecisionEventKind = "scale.decision"

// ScaleDecisionEvent is the bounded, durable explanation for one actionable
// reactive scale-up decision. The app ID is also used as the event subject so
// the payload stays useful to consumers that read events by subject.
type ScaleDecisionEvent struct {
	AppID              string  `json:"app_id"`
	Outcome            string  `json:"outcome"`
	Reason             string  `json:"reason"`
	ObservedRPS        float64 `json:"observed_rps"`
	TargetRPS          int     `json:"target_rps"`
	ObservedCPUPercent float64 `json:"observed_cpu_percent"`
	TargetCPUPercent   int     `json:"target_cpu_percent"`
	CurrentInstances   int     `json:"current_instances"`
	DesiredInstances   int     `json:"desired_instances"`
	CapacityInstances  int     `json:"capacity_instances"`
	HeadroomInstances  int     `json:"headroom_instances"`
	At                 string  `json:"at"`
}

func (t *Trigger) emitScaleDecision(ctx context.Context, stats AppStats, dec Decision, now time.Time) {
	if t == nil || t.events == nil || dec.Outcome == OutcomeNoSignal {
		return
	}

	reason := scaleDecisionReason(stats, dec)
	desired := dec.Desired
	if desired == 0 {
		// The pure decision intentionally leaves Desired empty on the
		// reject-at-cap branch; the current count is the effective desired
		// resident count in that case.
		desired = stats.Concurrency
	}
	fingerprint := fmt.Sprintf("%s:%s:%d:%d:%d:%d:%d", dec.Outcome, reason,
		stats.Concurrency, desired, stats.MaxConcurrency, stats.TargetRPS, stats.TargetCPU)
	if !t.allowScaleDecisionEvent(stats.AppID, fingerprint, now) {
		return
	}

	payload, err := json.Marshal(ScaleDecisionEvent{
		AppID:              stats.AppID,
		Outcome:            string(dec.Outcome),
		Reason:             reason,
		ObservedRPS:        stats.PerInstanceRPS,
		TargetRPS:          stats.TargetRPS,
		ObservedCPUPercent: stats.PerInstanceCPU,
		TargetCPUPercent:   stats.TargetCPU,
		CurrentInstances:   stats.Concurrency,
		DesiredInstances:   desired,
		CapacityInstances:  stats.MaxConcurrency,
		HeadroomInstances:  dec.Headroom,
		At:                 now.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.log.Warn("scaleup: marshal decision event", "app", stats.AppID, "err", err)
		return
	}
	subject := stats.AppID
	if err := t.events.AppendEvent(ctx, "schedd", scaleDecisionEventKind, &subject, payload); err != nil {
		// Event persistence is observability. Never turn a scale decision
		// into a failed scheduler tick because the audit store is degraded.
		t.log.Warn("scaleup: write decision event", "app", stats.AppID, "err", err)
	}
}

func (t *Trigger) allowScaleDecisionEvent(appID, fingerprint string, now time.Time) bool {
	t.eventMu.Lock()
	defer t.eventMu.Unlock()
	previous, ok := t.decisionEvents[appID]
	if ok && previous.fingerprint == fingerprint && now.Sub(previous.at) < time.Duration(api.ScaleDecisionEventMinIntervalSeconds)*time.Second {
		return false
	}
	t.decisionEvents[appID] = decisionEventState{fingerprint: fingerprint, at: now}
	return true
}

func scaleDecisionReason(stats AppStats, dec Decision) string {
	if dec.Outcome == OutcomeRejectAtCap {
		return "capacity_exhausted"
	}
	rpsHot := stats.TargetRPS > 0 && stats.HaveRPS && stats.PerInstanceRPS > float64(stats.TargetRPS)
	cpuHot := stats.TargetCPU > 0 && stats.HaveCPU && stats.PerInstanceCPU > float64(stats.TargetCPU)
	switch {
	case rpsHot && cpuHot:
		return "rps_and_cpu_target_exceeded"
	case rpsHot:
		return "rps_target_exceeded"
	case cpuHot:
		return "cpu_target_exceeded"
	default:
		return "target_exceeded"
	}
}
