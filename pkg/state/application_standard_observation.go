package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Only the owning apid worker may advance a reviewed operation. Qualification
// is read again under the same locks as the observation and wave checkpoint.
type ApplicationStandardObservationStore interface {
	ObserveApplicationStandardOperation(context.Context, ApplicationStandardWorkerClaim) (ApplicationStandardOperation, error)
}

type standardApplicationQualification struct {
	reason string
	until  time.Time
}

func (q *standardApplicationQualification) restrict(until time.Time) {
	if q.until.IsZero() || until.Before(q.until) {
		q.until = until
	}
}

func standardObservationEnrollment(r ApplicationStandardConsumerRoster, revision int64) string {
	if !r.EnrollmentCurrent || revision < 1 || r.DesiredRevision != revision || r.PersistedRevision != revision {
		return "enrollment_pending"
	}
	if len(r.Nodes) == 0 {
		return "consumer_roster_empty"
	}
	return ""
}

func standardObservationLoggingNode(n ApplicationStandardConsumerNode, inventory ApplicationStandardLogInventory, rows []ApplicationStandardLogInventoryObservation, now time.Time) standardApplicationQualification {
	if !n.LoggingRequired || n.LoggingStoppedAt != nil {
		return standardApplicationQualification{}
	}
	if !n.Present || !n.Active || !n.HeartbeatFresh || n.Role == "control-plane" || n.LoggingSession == nil {
		return standardApplicationQualification{reason: "logging_consumer_unavailable"}
	}
	for _, row := range rows {
		if row.ApplicationStandardLogConsumerSession == *n.LoggingSession && sameStandardLogInventory(row.ApplicationStandardLogInventory, inventory) {
			until := row.ObservedAt.Add(api.ApplicationStandardLogInventoryFreshness)
			if !row.ObservedAt.After(now) && until.After(now) {
				return standardApplicationQualification{until: until}
			}
		}
	}
	return standardApplicationQualification{reason: "logging_inventory_pending"}
}

func standardObservationProvider(n ApplicationStandardConsumerNode, b ApplicationStandardLogDrainBinding, rows []ApplicationStandardLogHealthObservation, now time.Time) standardApplicationQualification {
	for _, row := range rows {
		if row.ApplicationStandardLogDrainBinding != b || n.LoggingSession == nil || row.ApplicationStandardLogConsumerSession != *n.LoggingSession {
			continue
		}
		until := row.ObservedAt.Add(api.ApplicationStandardLogHealthFreshness)
		eventUntil := row.EventAt.Add(api.ApplicationStandardLogHealthFreshness)
		if eventUntil.Before(until) {
			until = eventUntil
		}
		if row.EventAt.IsZero() || row.EventAt.After(now) || row.ObservedAt.After(now) || !until.After(now) {
			break
		}
		if row.Status == "healthy" && row.Reason == "delivered" {
			return standardApplicationQualification{until: until}
		}
		if row.Status == "degraded" {
			return standardApplicationQualification{reason: "logging_provider_degraded"}
		}
	}
	return standardApplicationQualification{reason: "logging_provider_pending"}
}

func standardObservationEgress(target ApplicationStandardEgressTarget, rows []ApplicationStandardEgressObservation, now time.Time) standardApplicationQualification {
	for _, row := range rows {
		until := row.ObservedAt.Add(api.ApplicationStandardEgressFreshness)
		if sameStandardEgress(target, row.Target) && !row.ObservedAt.After(now) && until.After(now) && row.Receipt.Check(target.Identity, target.Policy) == nil {
			return standardApplicationQualification{until: until}
		}
	}
	return standardApplicationQualification{reason: "egress_observation_pending"}
}

func standardObservedTarget(t *ApplicationStandardOperationTarget, q standardApplicationQualification, now time.Time) {
	t.State, t.ErrorCode, t.UpdatedAt = "persisted", q.reason, now
	if q.reason == "" && !q.until.IsZero() && q.until.After(now) {
		t.State = "observed"
	} else if t.ErrorCode == "" {
		t.ErrorCode = "consumer_evidence_expired"
	}
}
