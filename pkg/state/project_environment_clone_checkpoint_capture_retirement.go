package state

import (
	"context"
	"time"
)

type ProjectEnvironmentCloneCheckpointCaptureRetirementStore interface {
	ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCaptureRetirement, error)
	ProjectEnvironmentCloneCheckpointCaptureRetirementForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCaptureRetirement, error)
	ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointCaptureRetirement, bool, error)
	FinishProjectEnvironmentCloneCheckpointCaptureRetirement(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) error
}

// Capture retirement covers all retained native artifacts owned by the original
// common capture, including artifacts whose creation never returned a receipt.
// A store must authenticate source compensation and all borrower retirements
// before reserving this plan. Shape validation grants no native IO authority.
type ProjectEnvironmentCloneCheckpointCaptureRetirementPlan struct {
	SourceCompensation ProjectEnvironmentCloneCheckpointSourceCompensation
}

type ProjectEnvironmentCloneCheckpointCaptureRetirement struct {
	Plan                     ProjectEnvironmentCloneCheckpointCaptureRetirementPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
	Observation              ProjectEnvironmentCloneCheckpointCaptureRetirementObservation
}

type ProjectEnvironmentCloneCheckpointCaptureRetirementObservation struct {
	RequestID, PlanSHA256, OwnerToken   string
	CaptureRequestID, CapturePlanSHA256 string
	CapturePermanentlyFenced            bool
	Sources                             []ProjectEnvironmentCloneCheckpointRetiredCapture
	RetiredAt                           time.Time
}

// NoOwnedArtifacts covers the entire original capture owner namespace, not just
// the known receipt identity. Provider not-found for one snapshot is insufficient.
// The observer must preserve source data and access and prove no borrowers remain.
type ProjectEnvironmentCloneCheckpointRetiredCapture struct {
	Kind, ID, SourceResourceID, CaptureIdentity, OwnerToken                    string
	NoOwnedArtifacts, NoNativeBorrowers, SourceUnchanged, SourceAccessRestored bool
}

func ProjectEnvironmentCloneCheckpointCaptureRetirementRequestID(operationID string) string {
	return "retire-capture-" + operationID
}

func ProjectEnvironmentCloneCheckpointCaptureRetirementPlanSHA256(p ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) string {
	return cloneCheckpointSHA(p)
}

func validateCheckpointCaptureRetirementPlan(p ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) error {
	s := p.SourceCompensation
	if s.State != "compensated" || validateCheckpointSourceCompensationObservation(s, s.Observation) != nil {
		return ErrConflict
	}
	return nil
}

func validateCheckpointCaptureRetirementObservation(in ProjectEnvironmentCloneCheckpointCaptureRetirement, o ProjectEnvironmentCloneCheckpointCaptureRetirementObservation) error {
	if validateCheckpointCaptureRetirementPlan(in.Plan) != nil {
		return ErrConflict
	}
	s := in.Plan.SourceCompensation
	c := s.Plan.Capture
	if !checkpointObjectRetirementJournalShape(in.State, "retired", in.RequestedAt, in.ObservationSHA256) || in.State == "planned" || in.RequestedAt.Before(s.Observation.CompensatedAt) || o.RequestID != ProjectEnvironmentCloneCheckpointCaptureRetirementRequestID(c.OperationID) || o.PlanSHA256 != cloneCheckpointSHA(in.Plan) || o.OwnerToken != c.OperationID || o.CaptureRequestID != c.OperationID || o.CapturePlanSHA256 != cloneCheckpointSHA(c) || !o.CapturePermanentlyFenced || !checkpointObjectRetirementTime(o.RetiredAt) || o.RetiredAt.Before(in.RequestedAt) || o.RetiredAt.After(time.Now()) || len(o.Sources) != len(c.Sources) {
		return ErrConflict
	}
	for n, source := range c.Sources {
		actual, physical, identity := o.Sources[n], source.ProviderResourceID, ""
		if source.Kind == "managed_postgres" {
			physical = source.DataResourceID
		}
		if s.Plan.Receipt != nil {
			identity = s.Plan.Receipt.Sources[n].CaptureIdentity
		}
		if actual.Kind != source.Kind || actual.ID != source.ID || actual.SourceResourceID != physical || actual.CaptureIdentity != identity || actual.OwnerToken != c.OperationID || !actual.NoOwnedArtifacts || !actual.NoNativeBorrowers || !actual.SourceUnchanged || !actual.SourceAccessRestored {
			return ErrConflict
		}
	}
	if in.State == "retired" && in.ObservationSHA256 != cloneCheckpointSHA(o) {
		return ErrConflict
	}
	return nil
}
