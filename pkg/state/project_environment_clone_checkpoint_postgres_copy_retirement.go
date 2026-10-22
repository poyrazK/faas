package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
)

// Shape validation grants no native IO or catalogue retirement authority. A
// durable store must authenticate these ORIGINAL journals under the operation
// lease before reservation. Legacy deletion receipts cannot supply this proof.
type ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan struct {
	SourceCompensationSHA256 string
	Capture                  ProjectEnvironmentCloneCheckpointPostgresTargetCompensationPlan
	Scope                    copyinventory.Scope
	Reader                   *ProjectEnvironmentClonePostgresCopyReader `json:",omitempty"`
	Target                   *ProjectEnvironmentClonePostgresCopyTarget `json:",omitempty"`
}

type ProjectEnvironmentCloneCheckpointPostgresCopyRetirement struct {
	Plan                     ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan
	State, ObservationSHA256 string
	RequestedAt              time.Time
	Observation              ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation
}

// Absence covers each entire original owner namespace, including unknown native
// outcomes. Closure includes queued reader cleanup operations and every copy
// writer; endpoint/project not-found alone is never sufficient. No provider is
// yet qualified for this contract.
type ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation struct {
	RequestID, PlanSHA256, OwnerToken                                    string
	SourceDataResourceID, ProviderSnapshotID, CaptureResourceID          string
	ReaderOwnerID, ReaderRequestID, ReaderEndpointID                     string
	ReaderPermanentlyFenced, AllReaderOperationsPermanentlyClosed        bool
	NoOwnedReaders                                                       bool
	TargetOwnerID, TargetRequestID, TargetResourceID                     string
	TargetPermanentlyFenced, AllWritersPermanentlyClosed, NoOwnedTargets bool
	NoNativeGrants, SourceUnchanged, SnapshotUnchanged, CaptureUnchanged bool
	RetiredAt                                                            time.Time
}

func ProjectEnvironmentCloneCheckpointPostgresCopyRetirementRequestID(p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) string {
	return "retire-postgres-copy-" + p.Scope.OperationID + "-" + p.Scope.SourceDatabaseID
}

func ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlanSHA256(p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) string {
	return cloneCheckpointSHA(p)
}

func validateCheckpointPostgresCopyRetirementPlan(p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return ErrConflict
	}
	if len(raw) > api.PostgresCheckpointCopyRetirementMaxBytes {
		return ErrQuotaExceeded
	}
	s, c := p.Capture.Snapshot, p.Capture.Restore
	if !validCloneObjectSHA256(p.SourceCompensationSHA256) || validateCheckpointPostgresTargetCompensationPlan(p.Capture) != nil || c.State != "adopted" || p.Capture.Isolation == nil || p.Scope.Validate() != nil || p.Reader == nil && p.Target == nil {
		return ErrConflict
	}
	expected := copyinventory.Scope{PostgresMajor: p.Scope.PostgresMajor, ProjectID: p.Scope.ProjectID, OperationID: c.OperationID, AccountID: c.AccountID, SourceDatabaseID: s.SourceDatabaseID, CaptureDatabaseID: c.AdoptedDatabaseID,
		SourceVersion: s.SourceVersion, BackendID: s.BackendID, BackendFingerprint: s.BackendFingerprint, SourceProviderResourceID: s.SourceProviderResourceID, SourceDataResourceID: s.SourceDataResourceID,
		ProviderSnapshotID: s.ProviderSnapshotID, CaptureProviderResourceID: c.TargetProviderResourceID, CapturePoint: s.CapturePoint, SnapshotCreatedAt: s.SnapshotCreatedAt, CaptureCreatedAt: c.TargetCreatedAt}
	if !p.Scope.Equal(expected) {
		return ErrConflict
	}
	if p.Reader != nil && validateCheckpointCopyRetirementReader(p, *p.Reader) != nil || p.Target != nil && validateCheckpointCopyRetirementTarget(p, *p.Target) != nil {
		return ErrConflict
	}
	if p.Reader != nil && p.Target != nil && (p.Reader.OwnerID == p.Target.TargetDatabaseID || p.Reader.EndpointID != "" && p.Reader.EndpointID == p.Target.ProviderResourceID) {
		return ErrConflict
	}
	return nil
}

func checkpointCopyRetirementOwner(p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan, id string) bool {
	return validCloneCredentialSourceID(id) && id != p.Scope.OperationID && id != p.Scope.SourceDatabaseID && id != p.Scope.CaptureDatabaseID
}

func checkpointCopyRetirementPhysical(p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan, id string) bool {
	return validCloneSnapshotID(id) && id != p.Scope.SourceProviderResourceID && id != p.Scope.SourceDataResourceID && id != p.Scope.ProviderSnapshotID && id != p.Scope.CaptureProviderResourceID
}

func checkpointCopyRetirementTime(at, earliest time.Time) bool {
	return at.IsZero() || checkpointObjectRetirementTime(at) && !at.Before(earliest) && !at.After(time.Now())
}

func validateCheckpointCopyRetirementReader(p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan, r ProjectEnvironmentClonePostgresCopyReader) error {
	if !r.Scope.Equal(p.Scope) || !checkpointCopyRetirementOwner(p, r.OwnerID) || !checkpointCopyRetirementTime(r.RequestStartedAt, p.Scope.CaptureCreatedAt) {
		return ErrConflict
	}
	switch r.State {
	case "reserved":
		if !r.RequestStartedAt.IsZero() || r.EndpointID != "" {
			return ErrConflict
		}
	case "requested", "observed":
		if r.RequestStartedAt.IsZero() || r.State == "requested" && r.EndpointID != "" {
			return ErrConflict
		}
	case "deleting", "retired":
		if r.CleanupRequestedAt.IsZero() {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	if r.State != "deleting" && r.State != "retired" && (!r.CleanupRequestedAt.IsZero() || !r.CleanupDispatchedAt.IsZero()) || !r.CleanupDispatchedAt.IsZero() && (r.CleanupRequestedAt.IsZero() || r.EndpointID == "") {
		return ErrConflict
	}
	if r.EndpointID == "" {
		if !r.EndpointCreatedAt.IsZero() || !r.ObservedAt.IsZero() || r.Available || r.State == "observed" {
			return ErrConflict
		}
	} else if r.RequestStartedAt.IsZero() || !checkpointCopyRetirementPhysical(p, r.EndpointID) || !checkpointObjectRetirementTime(r.EndpointCreatedAt) || !checkpointCopyRetirementTime(r.EndpointCreatedAt, r.RequestStartedAt) || r.ObservedAt.IsZero() || !checkpointCopyRetirementTime(r.ObservedAt, r.EndpointCreatedAt) {
		return ErrConflict
	}
	if !checkpointCopyRetirementTime(r.CleanupRequestedAt, p.Scope.CaptureCreatedAt) || !checkpointCopyRetirementTime(r.CleanupDispatchedAt, r.CleanupRequestedAt) || !checkpointCopyRetirementTime(r.RetiredAt, r.CleanupRequestedAt) || (r.State == "retired") != !r.RetiredAt.IsZero() {
		return ErrConflict
	}
	deleted, err := r.DeletionOperationIDs()
	if err != nil {
		return ErrConflict
	}
	captureDeleted, err := r.CaptureDeletionOperationIDs()
	if err != nil || len(deleted)+len(captureDeleted) > 0 && (r.EndpointID == "" || r.State != "deleting" && r.State != "retired") {
		return ErrConflict
	}
	return nil
}

func validateCheckpointCopyRetirementTarget(p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan, t ProjectEnvironmentClonePostgresCopyTarget) error {
	if t.OperationID != p.Scope.OperationID || t.AccountID != p.Scope.AccountID || t.SourceDatabaseID != p.Scope.SourceDatabaseID || t.CaptureDatabaseID != p.Scope.CaptureDatabaseID || !checkpointCopyRetirementOwner(p, t.TargetDatabaseID) || !checkpointCopyRetirementTime(t.RequestStartedAt, p.Scope.CaptureCreatedAt) {
		return ErrConflict
	}
	switch t.State {
	case "reserved":
		if !t.RequestStartedAt.IsZero() || t.ProviderResourceID != "" {
			return ErrConflict
		}
	case "requested", "preparing", "prepared":
		if t.RequestStartedAt.IsZero() || t.State == "requested" && t.ProviderResourceID != "" {
			return ErrConflict
		}
	case "deleting", "retired":
	default:
		return ErrConflict
	}
	if t.State == "deleting" && (t.RequestStartedAt.IsZero() || t.DeletionStartedAt.IsZero()) || t.State != "deleting" && t.State != "retired" && (!t.DeletionStartedAt.IsZero() || !t.DeletionObservedAt.IsZero()) || !t.DeletionObservedAt.IsZero() && t.DeletionStartedAt.IsZero() {
		return ErrConflict
	}
	if t.ProviderResourceID == "" {
		if !t.ProviderCreatedAt.IsZero() || !t.ObservedAt.IsZero() || !t.PreparedAt.IsZero() || t.State == "preparing" || t.State == "prepared" {
			return ErrConflict
		}
	} else if t.RequestStartedAt.IsZero() || !checkpointCopyRetirementPhysical(p, t.ProviderResourceID) || t.ProviderCreatedAt.IsZero() || !checkpointCopyRetirementTime(t.ProviderCreatedAt, t.RequestStartedAt) || t.ObservedAt.IsZero() || !checkpointCopyRetirementTime(t.ObservedAt, t.ProviderCreatedAt) {
		return ErrConflict
	}
	if !checkpointCopyRetirementTime(t.PreparedAt, t.ProviderCreatedAt) || t.State == "prepared" && t.PreparedAt.IsZero() || !checkpointCopyRetirementTime(t.DeletionStartedAt, p.Scope.CaptureCreatedAt) || !checkpointCopyRetirementTime(t.DeletionObservedAt, t.DeletionStartedAt) || !checkpointCopyRetirementTime(t.RetiredAt, p.Scope.CaptureCreatedAt) || (t.State == "retired") != !t.RetiredAt.IsZero() {
		return ErrConflict
	}
	return nil
}

func validateCheckpointPostgresCopyRetirementObservation(in ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, o ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) error {
	if err := validateCheckpointPostgresCopyRetirementPlan(in.Plan); err != nil {
		return err
	}
	p := in.Plan
	if !checkpointObjectRetirementJournalShape(in.State, "retired", in.RequestedAt, in.ObservationSHA256) || in.State == "planned" || in.RequestedAt.Before(p.Capture.Restore.AdoptedAt) || in.RequestedAt.Before(p.Capture.Isolation.RequestedAt) || o.RequestID != ProjectEnvironmentCloneCheckpointPostgresCopyRetirementRequestID(p) || o.PlanSHA256 != cloneCheckpointSHA(p) || o.OwnerToken != p.Scope.OperationID || o.SourceDataResourceID != p.Scope.SourceDataResourceID || o.ProviderSnapshotID != p.Scope.ProviderSnapshotID || o.CaptureResourceID != p.Scope.CaptureProviderResourceID || !o.NoNativeGrants || !o.SourceUnchanged || !o.SnapshotUnchanged || !o.CaptureUnchanged || !checkpointObjectRetirementTime(o.RetiredAt) || o.RetiredAt.Before(in.RequestedAt) || o.RetiredAt.After(time.Now()) {
		return ErrConflict
	}
	if p.Reader == nil {
		if o.ReaderOwnerID != "" || o.ReaderRequestID != "" || o.ReaderEndpointID != "" || o.ReaderPermanentlyFenced || o.AllReaderOperationsPermanentlyClosed || o.NoOwnedReaders {
			return ErrConflict
		}
	} else {
		r := p.Reader
		if o.ReaderOwnerID != r.OwnerID || o.ReaderRequestID != r.OwnerID || !o.ReaderPermanentlyFenced || !o.AllReaderOperationsPermanentlyClosed || !o.NoOwnedReaders || !checkpointCopyRetirementObservedIdentity(p, r.EndpointID, o.ReaderEndpointID, r.RequestStartedAt) {
			return ErrConflict
		}
		for _, at := range []time.Time{r.RequestStartedAt, r.EndpointCreatedAt, r.ObservedAt, r.CleanupRequestedAt, r.CleanupDispatchedAt, r.RetiredAt} {
			if in.RequestedAt.Before(at) {
				return ErrConflict
			}
		}
	}
	if p.Target == nil {
		if o.TargetOwnerID != "" || o.TargetRequestID != "" || o.TargetResourceID != "" || o.TargetPermanentlyFenced || o.AllWritersPermanentlyClosed || o.NoOwnedTargets {
			return ErrConflict
		}
	} else {
		t := p.Target
		if o.TargetOwnerID != t.TargetDatabaseID || o.TargetRequestID != t.TargetDatabaseID || !o.TargetPermanentlyFenced || !o.AllWritersPermanentlyClosed || !o.NoOwnedTargets || !checkpointCopyRetirementObservedIdentity(p, t.ProviderResourceID, o.TargetResourceID, t.RequestStartedAt) {
			return ErrConflict
		}
		for _, at := range []time.Time{t.RequestStartedAt, t.ProviderCreatedAt, t.ObservedAt, t.PreparedAt, t.DeletionStartedAt, t.DeletionObservedAt, t.RetiredAt} {
			if in.RequestedAt.Before(at) {
				return ErrConflict
			}
		}
	}
	if o.ReaderEndpointID != "" && o.ReaderEndpointID == o.TargetResourceID || in.State == "retired" && in.ObservationSHA256 != cloneCheckpointSHA(o) {
		return ErrConflict
	}
	return nil
}

func checkpointCopyRetirementObservedIdentity(p ProjectEnvironmentCloneCheckpointPostgresCopyRetirementPlan, original, observed string, requested time.Time) bool {
	if original != "" {
		return original == observed
	}
	return observed == "" || !requested.IsZero() && checkpointCopyRetirementPhysical(p, observed)
}

// These methods only authorize permanent retirement under a compensation lease.
// Claim commits once before native IO; requested recovery must observe only.
type ProjectEnvironmentCloneCheckpointPostgresCopyRetirementStore interface {
	ReserveProjectEnvironmentCloneCheckpointPostgresCopyRetirement(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error)
	ProjectEnvironmentCloneCheckpointPostgresCopyRetirementForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, error)
	ClaimProjectEnvironmentCloneCheckpointPostgresCopyRetirement(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneCheckpointPostgresCopyRetirement, bool, error)
	FinishProjectEnvironmentCloneCheckpointPostgresCopyRetirement(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentCloneCheckpointPostgresCopyRetirementObservation) error
}
