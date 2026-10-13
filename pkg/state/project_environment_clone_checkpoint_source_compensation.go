package state

import (
	"context"
	"reflect"
	"time"
)

// Source compensation freezes the original capture and optional release,
// including unknown outcomes. The store authenticates these against
// frozen journals before reservation. Shape validation alone grants no native IO
// or authority to open local configuration, database or object writer holds.
type ProjectEnvironmentCloneCheckpointSourceCompensationPlan struct {
	Capture ProjectEnvironmentCloneCheckpointPlan
	Intent  ProjectEnvironmentCloneResource
	Receipt *ProjectEnvironmentCloneCheckpointReceipt `json:",omitempty"`
	Release *ProjectEnvironmentCloneCheckpointRelease `json:",omitempty"`
}

type ProjectEnvironmentCloneCheckpointSourceCompensation struct {
	Plan                     ProjectEnvironmentCloneCheckpointSourceCompensationPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
	Observation              ProjectEnvironmentCloneCheckpointSourceCompensationObservation
}

// Absence of a native request is insufficient: even a planned request must be
// permanently fenced so a delayed dispatch cannot close the sources again.
type ProjectEnvironmentCloneCheckpointSourceCompensationObservation struct {
	RequestID, PlanSHA256, OwnerToken   string
	CaptureRequestID, CapturePlanSHA256 string
	CapturePermanentlyFenced            bool
	ReleaseRequestID, ReleasePlanSHA256 string `json:",omitempty"`
	ReleasePermanentlyFenced            bool
	Sources                             []ProjectEnvironmentCloneCheckpointCompensatedSource
	CompensatedAt                       time.Time
}

type ProjectEnvironmentCloneCheckpointCompensatedSource struct {
	Kind, ID, SourceResourceID, CaptureIdentity, OwnerToken, TerminalState string
	NoNativeHolds, SourceAccessRestored, SourceUnchanged                   bool
	ReleasedAt                                                             time.Time
}

func ProjectEnvironmentCloneCheckpointSourceCompensationRequestID(operationID string) string {
	return "compensate-sources-" + operationID
}

func ProjectEnvironmentCloneCheckpointSourceCompensationPlanSHA256(p ProjectEnvironmentCloneCheckpointSourceCompensationPlan) string {
	return cloneCheckpointSHA(p)
}

func validateCheckpointSourceCompensationPlan(p ProjectEnvironmentCloneCheckpointSourceCompensationPlan) error {
	c, i := p.Capture, p.Intent
	if c.OperationID == "" || c.AccountID == "" || c.ProjectID == "" || c.SourceEnvironment == "" || !validCloneObjectSHA256(c.SourceRevisionHash) || c.ConfigurationGeneration < 2 || !checkpointObjectRetirementTime(c.ConfigurationHeldAt) || c.ConfigurationHeldAt.After(time.Now()) || len(c.Sources) == 0 {
		return ErrConflict
	}
	if i.Kind != "data_checkpoint" || i.Name != c.SourceEnvironment || i.SourceID != c.OperationID || i.SourceVersion != cloneCheckpointSHA(c) {
		return ErrConflict
	}
	previous := ""
	for _, s := range c.Sources {
		key := s.Kind + "\x00" + s.ID
		if key <= previous || s.ID == "" || s.BackendID == "" || s.BackendFingerprint == "" || s.ProviderResourceID == "" || !validCloneObjectSHA256(s.DefinitionSHA256) {
			return ErrConflict
		}
		switch s.Kind {
		case "managed_postgres":
			if s.DataResourceID == "" || s.AppID != "" || s.Scope != "" {
				return ErrConflict
			}
		case "object_storage":
			if s.AppID == "" || s.Scope == "" || s.DataResourceID != "" {
				return ErrConflict
			}
		default:
			return ErrConflict
		}
		previous = key
	}
	switch i.Status {
	case "planned", "capturing":
		if i.TargetID != "" || i.CapturePoint != "" || p.Receipt != nil || p.Release != nil {
			return ErrConflict
		}
	case "captured":
		if p.Receipt == nil {
			return ErrConflict
		}
		r := p.Receipt
		if r.RequestID != c.OperationID || r.PlanSHA256 != cloneCheckpointSHA(c) || r.CheckpointID == "" || !r.CompleteWriterCoverage || !checkpointObjectRetirementTime(r.CapturedAt) || r.CapturedAt.Before(c.ConfigurationHeldAt) || r.CapturedAt.After(time.Now()) || i.TargetID != r.CheckpointID || i.CapturePoint != r.CapturedAt.Format(time.RFC3339Nano) || len(r.Sources) != len(c.Sources) {
			return ErrConflict
		}
		for n, s := range c.Sources {
			actual := r.Sources[n]
			if actual.Kind != s.Kind || actual.ID != s.ID || actual.CaptureIdentity == "" {
				return ErrConflict
			}
		}
	default:
		return ErrConflict
	}
	if p.Release != nil {
		r := p.Release
		if !reflect.DeepEqual(r.Plan, c) || !reflect.DeepEqual(r.Receipt, *p.Receipt) || !validCloneObjectSHA256(r.AdoptionSHA256) || !checkpointObjectRetirementJournalShape(r.State, "released", r.RequestedAt, r.ObservationSHA256) || r.State != "planned" && r.RequestedAt.Before(p.Receipt.CapturedAt) {
			return ErrConflict
		}
	}
	return nil
}

func validateCheckpointSourceCompensationObservation(in ProjectEnvironmentCloneCheckpointSourceCompensation, o ProjectEnvironmentCloneCheckpointSourceCompensationObservation) error {
	if err := validateCheckpointSourceCompensationPlan(in.Plan); err != nil {
		return err
	}
	p, c := in.Plan, in.Plan.Capture
	if !checkpointObjectRetirementJournalShape(in.State, "compensated", in.RequestedAt, in.ObservationSHA256) || in.State == "planned" || in.RequestedAt.Before(c.ConfigurationHeldAt) || p.Receipt != nil && in.RequestedAt.Before(p.Receipt.CapturedAt) || p.Release != nil && in.RequestedAt.Before(p.Release.RequestedAt) {
		return ErrConflict
	}
	if o.RequestID != ProjectEnvironmentCloneCheckpointSourceCompensationRequestID(c.OperationID) || o.PlanSHA256 != cloneCheckpointSHA(p) || o.OwnerToken != c.OperationID || o.CaptureRequestID != c.OperationID || o.CapturePlanSHA256 != cloneCheckpointSHA(c) || !o.CapturePermanentlyFenced || !checkpointObjectRetirementTime(o.CompensatedAt) || o.CompensatedAt.Before(in.RequestedAt) || o.CompensatedAt.After(time.Now()) || len(o.Sources) != len(c.Sources) {
		return ErrConflict
	}
	if p.Release == nil {
		if o.ReleaseRequestID != "" || o.ReleasePlanSHA256 != "" || o.ReleasePermanentlyFenced {
			return ErrConflict
		}
	} else if o.ReleaseRequestID != ProjectEnvironmentCloneCheckpointReleaseRequestID(c.OperationID) || o.ReleasePlanSHA256 != cloneCheckpointSHA(p.Release.Plan) || !o.ReleasePermanentlyFenced {
		return ErrConflict
	}
	for n, s := range c.Sources {
		actual, resource, identity := o.Sources[n], s.ProviderResourceID, ""
		if s.Kind == "managed_postgres" {
			resource = s.DataResourceID
		}
		if p.Receipt != nil {
			identity = p.Receipt.Sources[n].CaptureIdentity
		}
		if actual.Kind != s.Kind || actual.ID != s.ID || actual.SourceResourceID != resource || actual.CaptureIdentity != identity || actual.OwnerToken != c.OperationID || actual.TerminalState != "released" && actual.TerminalState != "abandoned" || !actual.NoNativeHolds || !actual.SourceAccessRestored || !actual.SourceUnchanged || !checkpointObjectRetirementTime(actual.ReleasedAt) || actual.ReleasedAt.Before(in.RequestedAt) || actual.ReleasedAt.After(o.CompensatedAt) {
			return ErrConflict
		}
	}
	if in.State == "compensated" && in.ObservationSHA256 != cloneCheckpointSHA(o) {
		return ErrConflict
	}
	return nil
}

// Reserve accepts original capture evidence, authenticated against its recorded
// receipt hash. Recovery reads the saved plan; callers must not invent receipts
// for captures with unknown outcomes or renew placement/retention.
type ProjectEnvironmentCloneCheckpointSourceCompensationStore interface {
	ReserveProjectEnvironmentCloneCheckpointSourceCompensation(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointSourceCompensationPlan) (ProjectEnvironmentCloneCheckpointSourceCompensation, error)
	ProjectEnvironmentCloneCheckpointSourceCompensationForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointSourceCompensation, error)
	ClaimProjectEnvironmentCloneCheckpointSourceCompensation(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointSourceCompensation, bool, error)
	FinishProjectEnvironmentCloneCheckpointSourceCompensation(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointSourceCompensationObservation) error
}
