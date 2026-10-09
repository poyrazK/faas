package state

import (
	"context"
	"reflect"
	"time"
)

// Retirement pins the original creation and optional copy journals, including
// requests with unknown outcomes. These records must be authenticated against
// the frozen operation before reservation; their presence is not authority to
// delete a bucket. No credentials, worker tokens or refreshed inventories belong
// in this immutable plan. No native provider is yet qualified for this protocol.
type ProjectEnvironmentCloneCheckpointObjectRetirementPlan struct {
	Creation         ProjectEnvironmentCloneCheckpointObjectTarget
	SourceResourceID string
	Copy             *ProjectEnvironmentCloneCheckpointObjectCopy `json:",omitempty"`
}

type ProjectEnvironmentCloneCheckpointObjectRetirement struct {
	Plan                     ProjectEnvironmentCloneCheckpointObjectRetirementPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
	Observation              ProjectEnvironmentCloneCheckpointObjectRetirementObservation
}

type ProjectEnvironmentCloneCheckpointObjectRetirementStore interface {
	ReleaseProjectEnvironmentCloneCheckpointUnusedObjectReservation(context.Context, ProjectEnvironmentCloneLease, string) error
	ReserveProjectEnvironmentCloneCheckpointObjectRetirement(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointObjectRetirement, error)
	ClaimProjectEnvironmentCloneCheckpointObjectRetirement(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointObjectRetirement, bool, error)
	FinishProjectEnvironmentCloneCheckpointObjectRetirement(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointObjectRetirementObservation) error
}

// Absence alone is insufficient: a qualified native observer must attest that
// the original create/copy requests and every writer are permanently fenced,
// even if they were queued before compensation or have never returned a reply.
// These are provider observations, not assertions derived from local withdrawal.
type ProjectEnvironmentCloneCheckpointObjectRetirementObservation struct {
	RequestID, PlanSHA256, OwnerToken, NativeResourceID string
	SourceResourceID                                    string
	CreationRequestID, CreationPlanSHA256               string
	CopyRequestID, CopyPlanSHA256                       string `json:",omitempty"`
	CreationPermanentlyFenced, CopyPermanentlyFenced    bool
	AllWritersPermanentlyClosed, TargetAbsent           bool
	NoNativeGrants, SourceUnchanged                     bool
	RetiredAt                                           time.Time
}

func ProjectEnvironmentCloneCheckpointObjectRetirementRequestID(p ProjectEnvironmentCloneCheckpointObjectRetirementPlan) string {
	return "retire-" + ProjectEnvironmentCloneCheckpointObjectTargetRequestID(p.Creation.Plan)
}

func ProjectEnvironmentCloneCheckpointObjectRetirementPlanSHA256(p ProjectEnvironmentCloneCheckpointObjectRetirementPlan) string {
	return cloneCheckpointSHA(p)
}

// Shape validation complements, but cannot replace, frozen-journal ownership
// authentication. Cleanup deliberately has no retention or copy-deadline check:
// expired source retention must not prevent retiring an owned target.
func validateCheckpointObjectRetirementPlan(p ProjectEnvironmentCloneCheckpointObjectRetirementPlan) error {
	c := p.Creation
	t := c.Plan
	for _, id := range []string{t.OperationID, t.AccountID, t.AppID, t.SourceBucketID, t.TargetBucketID, t.BackendID, t.BackendFingerprint, t.Region, t.PhysicalName, t.CheckpointID} {
		if id == "" {
			return ErrConflict
		}
	}
	if t.SourceBucketID == t.TargetBucketID || p.SourceResourceID == "" || p.SourceResourceID == t.PhysicalName || !validCloneObjectSHA256(t.ManifestSHA256) || !checkpointObjectRetirementJournalShape(c.State, "ready", c.RequestedAt, c.ObservationSHA256) {
		return ErrConflict
	}
	if p.Copy == nil {
		return nil
	}
	copy := p.Copy
	i := copy.Plan.Inventory
	if c.State != "ready" || !reflect.DeepEqual(i.Target, t) || i.CreationSHA256 != c.ObservationSHA256 || i.SourceResourceID != p.SourceResourceID || i.CopySHA256 != "" || !checkpointObjectRetirementTime(copy.Plan.WriteUntil) || !checkpointObjectRetirementJournalShape(copy.State, "copied", copy.RequestedAt, copy.ObservationSHA256) {
		return ErrConflict
	}
	return nil
}

func checkpointObjectRetirementTime(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t.Nanosecond()%1000 == 0
}

func checkpointObjectRetirementJournalShape(s, terminal string, requested time.Time, digest string) bool {
	switch s {
	case "planned":
		return requested.IsZero() && digest == ""
	case "requested":
		return checkpointObjectRetirementTime(requested) && !requested.After(time.Now()) && digest == ""
	case terminal:
		return checkpointObjectRetirementTime(requested) && !requested.After(time.Now()) && validCloneObjectSHA256(digest)
	default:
		return false
	}
}

func validateCheckpointObjectRetirementObservation(in ProjectEnvironmentCloneCheckpointObjectRetirement, o ProjectEnvironmentCloneCheckpointObjectRetirementObservation) error {
	if err := validateCheckpointObjectRetirementPlan(in.Plan); err != nil {
		return err
	}
	p := in.Plan
	c := p.Creation.Plan
	if in.RequestedAt.Before(p.Creation.RequestedAt) || p.Copy != nil && in.RequestedAt.Before(p.Copy.RequestedAt) || o.SourceResourceID != p.SourceResourceID {
		return ErrConflict
	}
	if !checkpointObjectRetirementJournalShape(in.State, "retired", in.RequestedAt, in.ObservationSHA256) || in.State == "planned" || o.RequestID != ProjectEnvironmentCloneCheckpointObjectRetirementRequestID(p) || o.PlanSHA256 != cloneCheckpointSHA(p) || o.OwnerToken != c.OperationID || o.NativeResourceID != c.PhysicalName || o.CreationRequestID != ProjectEnvironmentCloneCheckpointObjectTargetRequestID(c) || o.CreationPlanSHA256 != cloneCheckpointSHA(c) || !o.CreationPermanentlyFenced || !o.AllWritersPermanentlyClosed || !o.TargetAbsent || !o.NoNativeGrants || !o.SourceUnchanged || !checkpointObjectRetirementTime(o.RetiredAt) || o.RetiredAt.Before(in.RequestedAt) || o.RetiredAt.After(time.Now()) {
		return ErrConflict
	}
	if p.Copy == nil {
		if o.CopyRequestID != "" || o.CopyPlanSHA256 != "" || o.CopyPermanentlyFenced {
			return ErrConflict
		}
	} else if o.CopyRequestID != ProjectEnvironmentCloneCheckpointObjectCopyRequestID(p.Copy.Plan) || o.CopyPlanSHA256 != cloneCheckpointSHA(p.Copy.Plan) || !o.CopyPermanentlyFenced {
		return ErrConflict
	}
	if in.State == "retired" && in.ObservationSHA256 != cloneCheckpointSHA(o) {
		return ErrConflict
	}
	return nil
}
