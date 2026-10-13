package state

import (
	"context"
	"reflect"
	"time"
)

// Original pinned plans contain no secrets or worker tokens. Field order and
// encoding preserve the original checkpoint proof across worker handoff.
type ProjectEnvironmentCloneCheckpointSource struct {
	Kind, ID, AppID, Scope, BackendID, BackendFingerprint string
	ProviderResourceID, DataResourceID, DefinitionSHA256  string
}

type ProjectEnvironmentCloneCheckpointPlan struct {
	OperationID, AccountID, ProjectID, SourceEnvironment string
	SourceRevisionHash, SourceReleaseSetID               string
	ConfigurationGeneration                              int64
	ConfigurationHeldAt                                  time.Time
	Sources                                              []ProjectEnvironmentCloneCheckpointSource
}

// Release authority survives reopening source configuration. Requested never
// grants another dispatch, including when the original claim reply was lost.
type ProjectEnvironmentCloneCheckpointRelease struct {
	Plan                  ProjectEnvironmentCloneCheckpointPlan
	Receipt               ProjectEnvironmentCloneCheckpointReceipt
	AdoptionSHA256, State string
	RequestedAt           time.Time
	ObservationSHA256     string
}

type ProjectEnvironmentCloneCheckpointSourceRelease struct {
	Kind, ID, CaptureIdentity, OwnerToken, SourceResourceID, TerminalState string
	ReleasedAt                                                             time.Time
}

type ProjectEnvironmentCloneCheckpointReleaseObservation struct {
	RequestID, PlanSHA256, ReceiptSHA256, CheckpointID string
	Sources                                            []ProjectEnvironmentCloneCheckpointSourceRelease
	// Independently checked original capture retention, outside the immutable
	// release digest so a later worker may verify a longer retention horizon.
	RetainedUntil time.Time
}

type ProjectEnvironmentCloneCheckpointReleaseStore interface {
	ProjectEnvironmentCloneCheckpointReleaseForLease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointRelease, error)
	ReserveProjectEnvironmentCloneCheckpointRelease(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointPlan, ProjectEnvironmentCloneCheckpointReceipt) (ProjectEnvironmentCloneCheckpointRelease, error)
	ClaimProjectEnvironmentCloneCheckpointRelease(context.Context, ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointRelease, bool, error)
	FinishProjectEnvironmentCloneCheckpointRelease(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneOperation, error)
}

func ProjectEnvironmentCloneCheckpointReleaseRequestID(operationID string) string {
	return "release-" + operationID
}

func cloneCheckpointReleaseSHA(in ProjectEnvironmentCloneCheckpointReleaseObservation) string {
	in.RetainedUntil = time.Time{}
	return cloneCheckpointSHA(in)
}

func validateCloneCheckpointReleaseObservation(in ProjectEnvironmentCloneCheckpointRelease, observed ProjectEnvironmentCloneCheckpointReleaseObservation, retainUntil time.Time) error {
	if in.State != "requested" && in.State != "released" || in.RequestedAt.IsZero() ||
		observed.RequestID != ProjectEnvironmentCloneCheckpointReleaseRequestID(in.Plan.OperationID) ||
		observed.PlanSHA256 != cloneCheckpointSHA(in.Plan) || observed.ReceiptSHA256 != cloneCheckpointSHA(in.Receipt) ||
		observed.CheckpointID != in.Receipt.CheckpointID || observed.RetainedUntil.Before(retainUntil) ||
		len(observed.Sources) != len(in.Plan.Sources) || len(in.Receipt.Sources) != len(in.Plan.Sources) {
		return ErrConflict
	}
	for i, source := range in.Plan.Sources {
		actual, capture := observed.Sources[i], in.Receipt.Sources[i]
		resource := source.ProviderResourceID
		if source.Kind == "managed_postgres" {
			resource = source.DataResourceID
		}
		if actual.Kind != source.Kind || actual.ID != source.ID || actual.CaptureIdentity != capture.CaptureIdentity ||
			actual.OwnerToken != in.Plan.OperationID || actual.SourceResourceID != resource || actual.TerminalState != "released" ||
			actual.ReleasedAt.Location() != time.UTC || actual.ReleasedAt.Nanosecond()%1000 != 0 ||
			actual.ReleasedAt.Before(in.RequestedAt) || actual.ReleasedAt.Before(in.Receipt.CapturedAt) || actual.ReleasedAt.After(time.Now()) {
			return ErrConflict
		}
	}
	if in.State == "released" && in.ObservationSHA256 != cloneCheckpointReleaseSHA(observed) {
		return ErrConflict
	}
	return nil
}

func validateCloneCheckpointReleasePlan(op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointRelease, views []ProjectEnvironmentCloneBindings) error {
	if op.Status != CloneOperationCapturing {
		return ErrConflict
	}
	return validateCloneCheckpointReleasePlanContents(op, in, views)
}

func validateCloneCheckpointReleasePlanContents(op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointRelease, views []ProjectEnvironmentCloneBindings) error {
	p, r := in.Plan, in.Receipt
	if p.OperationID != op.ID || p.AccountID != op.AccountID || p.ProjectID != op.ProjectID || p.SourceEnvironment != op.SourceEnvironment ||
		p.SourceRevisionHash != op.SourceRevisionHash || p.SourceReleaseSetID != op.SourceReleaseSetID || p.ConfigurationGeneration < 2 ||
		p.ConfigurationHeldAt.IsZero() || p.ConfigurationHeldAt.Location() != time.UTC || p.ConfigurationHeldAt.Nanosecond()%1000 != 0 ||
		r.CapturedAt.Before(p.ConfigurationHeldAt) || r.PlanSHA256 != cloneCheckpointSHA(p) ||
		validateCloneCheckpointAdoptionReceiptContents(op, ProjectEnvironmentCloneCheckpointAdoption{Receipt: r}) != nil {
		return ErrConflict
	}
	expected := map[string]ProjectEnvironmentCloneCheckpointSource{}
	for _, view := range views {
		for _, db := range view.Postgres {
			hash, err := ProjectEnvironmentCloneDatabaseSourceHash(db)
			if err != nil {
				return ErrConflict
			}
			source := ProjectEnvironmentCloneCheckpointSource{Kind: "managed_postgres", ID: db.DatabaseID, BackendID: db.BackendID,
				BackendFingerprint: db.BackendFingerprint, ProviderResourceID: db.ProviderResourceID, DataResourceID: db.DataResourceID, DefinitionSHA256: hash}
			key := source.Kind + "\x00" + source.ID
			if previous, ok := expected[key]; ok && previous != source {
				return ErrConflict
			}
			expected[key] = source
		}
		for _, bucket := range view.Buckets {
			source := ProjectEnvironmentCloneCheckpointSource{Kind: "object_storage", ID: bucket.ID, AppID: view.AppID, Scope: view.SourceScope,
				BackendID: bucket.BackendID, BackendFingerprint: bucket.BackendFingerprint, ProviderResourceID: bucket.PhysicalName, DefinitionSHA256: cloneCheckpointSHA(bucket)}
			key := source.Kind + "\x00" + source.ID
			if _, ok := expected[key]; ok {
				return ErrConflict
			}
			expected[key] = source
		}
	}
	if len(p.Sources) == 0 || len(p.Sources) != len(expected) || len(r.Sources) != len(expected) {
		return ErrConflict
	}
	previous := ""
	for i, source := range p.Sources {
		key := source.Kind + "\x00" + source.ID
		if key <= previous || !reflect.DeepEqual(expected[key], source) || r.Sources[i].Kind != source.Kind || r.Sources[i].ID != source.ID {
			return ErrConflict
		}
		previous = key
	}
	return nil
}
