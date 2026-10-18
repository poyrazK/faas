package state

import (
	"context"
	"time"
)

// apid periodically reconciles installed enrollments outside active reviewed
// operations. The same enrollment lease fences materialization and observation.
type ApplicationStandardAutomaticObservationStore interface {
	ClaimApplicationStandardObservation(context.Context, string) (ApplicationStandardEnrollmentClaim, error)
	ObserveApplicationStandardEnrollment(context.Context, ApplicationStandardEnrollmentClaim) (ApplicationStandardEnrollment, error)
	ReleaseApplicationStandardEnrollmentWorker(context.Context, ApplicationStandardEnrollmentClaim) error
}

type standardAutomaticObservationCheck struct {
	revision int64
	at       time.Time
}

func standardObservationInstalled(e ApplicationStandardEnrollment) bool {
	return e.DesiredRevision > 0 && e.PersistedRevision == e.DesiredRevision && (e.State == "persisted" || e.State == "observed")
}

func standardObservedEnrollment(e ApplicationStandardEnrollment, q standardApplicationQualification, now time.Time) ApplicationStandardEnrollment {
	t := ApplicationStandardOperationTarget{}
	standardObservedTarget(&t, q, now)
	revision := int64(0)
	if t.State == "observed" {
		revision = e.DesiredRevision
	}
	if e.State != t.State || e.ErrorCode != t.ErrorCode || e.ObservedRevision != revision {
		e.State, e.ErrorCode, e.ObservedRevision, e.UpdatedAt = t.State, t.ErrorCode, revision, now
	}
	return e
}
