package state

import (
	"context"
	"time"
)

// This private proof contract grants no deletion or catalogue mutation authority.
// A durable coordinator must authenticate the original journals before saving a
// plan and fence native requests before accepting target absence. In particular,
// provider not-found alone cannot settle a queued restore with an unknown outcome.
type ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan struct {
	Snapshot  ProjectEnvironmentClonePostgresSnapshot
	Restore   ProjectEnvironmentClonePostgresSnapshotRestore
	Isolation *ProjectEnvironmentCloneCheckpointPostgresIsolation `json:",omitempty"`
}

type ProjectEnvironmentCloneCheckpointPostgresTargetCompensation struct {
	Plan                     ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
	Observation              ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation
}

type ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation struct {
	RequestID, PlanSHA256, OwnerID, RestoreRequestID string
	TargetProviderResourceID                         string
	RestorePermanentlyFenced, TargetAbsent           bool
	SourceUnchanged, SnapshotUnchanged               bool
	IsolationRequestID, IsolationPlanSHA256          string `json:",omitempty"`
	IsolationPermanentlyFenced                       bool
	CompensatedAt                                    time.Time
}

func ProjectEnvironmentCloneCheckpointPostgresTargetCompensationRequestID(p ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) string {
	return "compensate-postgres-target-" + p.Restore.OperationID + "-" + p.Restore.TargetOwnerID
}

func ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlanSHA256(p ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) string {
	return cloneCheckpointSHA(p)
}

func validateCheckpointPostgresTargetCompensationPlan(p ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan) error {
	s, r := p.Snapshot, p.Restore
	if s.State != "retained" || s.OperationID == "" || s.SourceDatabaseID == "" || s.SourceVersion == "" || s.BackendID == "" || s.BackendFingerprint == "" || !validCloneSnapshotID(s.ProviderSnapshotID) || !validCloneSnapshotID(s.SourceProviderResourceID) || !validCloneSnapshotID(s.SourceDataResourceID) || !checkpointObjectRetirementTime(s.CapturePoint) || !checkpointObjectRetirementTime(s.SnapshotCreatedAt) || s.SnapshotCreatedAt.Before(s.CapturePoint) || s.SnapshotCreatedAt.After(time.Now()) || r.OperationID != s.OperationID || r.SourceDatabaseID != s.SourceDatabaseID || r.AccountID == "" || r.BackendID != s.BackendID || r.BackendFingerprint != s.BackendFingerprint || r.TargetOwnerID == "" || r.TargetOwnerID == s.SourceDatabaseID {
		return ErrConflict
	}
	switch r.State {
	case "reserved", "requested", "restoring", "restored", "adopted", "deleting", "deleted":
	default:
		return ErrConflict
	}
	if r.State == "reserved" && (!r.RequestStartedAt.IsZero() || r.TargetProviderResourceID != "") || !r.RequestStartedAt.IsZero() && (!checkpointObjectRetirementTime(r.RequestStartedAt) || r.RequestStartedAt.Before(s.SnapshotCreatedAt) || r.RequestStartedAt.After(time.Now())) || (r.State == "requested" || r.State == "restoring" || r.State == "restored" || r.State == "adopted") && r.RequestStartedAt.IsZero() {
		return ErrConflict
	}
	if r.TargetProviderResourceID == "" {
		if !r.TargetCreatedAt.IsZero() || r.AdoptedDatabaseID != "" || p.Isolation != nil || r.State == "restoring" || r.State == "restored" || r.State == "adopted" {
			return ErrConflict
		}
	} else if r.RequestStartedAt.IsZero() || !validCloneSnapshotID(r.TargetProviderResourceID) || r.TargetProviderResourceID == s.SourceDataResourceID || r.TargetProviderResourceID == s.SourceProviderResourceID || !checkpointObjectRetirementTime(r.TargetCreatedAt) || r.TargetCreatedAt.Before(s.SnapshotCreatedAt) || r.TargetCreatedAt.After(time.Now()) {
		return ErrConflict
	}
	if r.AdoptedDatabaseID != "" && (r.AdoptedDatabaseID != r.TargetOwnerID || !checkpointObjectRetirementTime(r.AdoptedAt) || r.AdoptedAt.Before(r.TargetCreatedAt) || r.AdoptedAt.After(time.Now())) || r.State == "adopted" && r.AdoptedDatabaseID == "" {
		return ErrConflict
	}
	if p.Isolation != nil {
		i, ip := p.Isolation, p.Isolation.Plan
		if (i.State == "adopted") != (r.AdoptedDatabaseID != "") || ip.OperationID != r.OperationID || ip.AccountID != r.AccountID || ip.SourceDatabaseID != r.SourceDatabaseID || ip.OwnerID != r.TargetOwnerID || ip.NativeResourceID != r.TargetProviderResourceID || ip.BackendID != s.BackendID || ip.BackendFingerprint != s.BackendFingerprint || ip.CheckpointID == "" || ip.ProviderSnapshotID != s.ProviderSnapshotID || ip.SourceDataResourceID != s.SourceDataResourceID || !ip.CapturePoint.Equal(s.CapturePoint) || !ip.SnapshotCreatedAt.Equal(s.SnapshotCreatedAt) || !ip.TargetCreatedAt.Equal(r.TargetCreatedAt) || !checkpointObjectRetirementJournalShape(i.State, "adopted", i.RequestedAt, i.ObservationSHA256) || i.State != "planned" && i.RequestedAt.Before(r.TargetCreatedAt) {
			return ErrConflict
		}
	}
	return nil
}

func validateCheckpointPostgresTargetCompensationObservation(in ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, o ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) error {
	if err := validateCheckpointPostgresTargetCompensationPlan(in.Plan); err != nil {
		return err
	}
	p, r := in.Plan, in.Plan.Restore
	if !checkpointObjectRetirementJournalShape(in.State, "compensated", in.RequestedAt, in.ObservationSHA256) || in.State == "planned" || in.RequestedAt.Before(p.Snapshot.SnapshotCreatedAt) || in.RequestedAt.Before(r.RequestStartedAt) || in.RequestedAt.Before(r.TargetCreatedAt) || in.RequestedAt.Before(r.AdoptedAt) || p.Isolation != nil && in.RequestedAt.Before(p.Isolation.RequestedAt) || o.RequestID != ProjectEnvironmentCloneCheckpointPostgresTargetCompensationRequestID(p) || o.PlanSHA256 != cloneCheckpointSHA(p) || o.OwnerID != r.TargetOwnerID || o.RestoreRequestID != r.TargetOwnerID || !o.RestorePermanentlyFenced || !o.TargetAbsent || !o.SourceUnchanged || !o.SnapshotUnchanged || !checkpointObjectRetirementTime(o.CompensatedAt) || o.CompensatedAt.Before(in.RequestedAt) || o.CompensatedAt.After(time.Now()) {
		return ErrConflict
	}
	// Unknown restores may have created a target. Its authenticated physical ID
	// belongs in the proof, never in a replacement original restore plan.
	if r.TargetProviderResourceID != "" && o.TargetProviderResourceID != r.TargetProviderResourceID || r.TargetProviderResourceID == "" && o.TargetProviderResourceID != "" && !validCloneSnapshotID(o.TargetProviderResourceID) || r.RequestStartedAt.IsZero() && o.TargetProviderResourceID != "" || o.TargetProviderResourceID == p.Snapshot.SourceDataResourceID || o.TargetProviderResourceID == p.Snapshot.SourceProviderResourceID {
		return ErrConflict
	}
	if p.Isolation == nil {
		if o.IsolationRequestID != "" || o.IsolationPlanSHA256 != "" || o.IsolationPermanentlyFenced {
			return ErrConflict
		}
	} else if o.IsolationRequestID != ProjectEnvironmentCloneCheckpointPostgresIsolationRequestID(p.Isolation.Plan) || o.IsolationPlanSHA256 != cloneCheckpointSHA(p.Isolation.Plan) || !o.IsolationPermanentlyFenced {
		return ErrConflict
	}
	if in.State == "compensated" && in.ObservationSHA256 != cloneCheckpointSHA(o) {
		return ErrConflict
	}
	return nil
}

// Reservation derives the original plan from authenticated frozen journals;
// recovery reads it without source recapture, target discovery or new ownership.
type ProjectEnvironmentCloneCheckpointPostgresTargetCompensationStore interface {
	ReserveProjectEnvironmentCloneCheckpointPostgresTargetCompensation(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error)
	ProjectEnvironmentCloneCheckpointPostgresTargetCompensationForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, error)
	ClaimProjectEnvironmentCloneCheckpointPostgresTargetCompensation(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresTargetCompensation, bool, error)
	FinishProjectEnvironmentCloneCheckpointPostgresTargetCompensation(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresTargetCompensationObservation) error
}
