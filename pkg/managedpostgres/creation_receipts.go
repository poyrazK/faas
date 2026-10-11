package managedpostgres

import (
	"context"
	"errors"
	"regexp"
	"time"
)

// CreationAcknowledgement establishes custody of an operation-created resource,
// never its restored contents, retention, readiness, or requested configuration.
// Adapters obtain it from a successful creation response, not a name lookup.
var backendFingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type CreationAcknowledgement struct {
	ProviderResourceID, SourceResourceID string
	CreatedAt                            time.Time
}

func (a CreationAcknowledgement) Validate() error {
	if !validOpaqueID(a.ProviderResourceID) || !validOpaqueID(a.SourceResourceID) || a.ProviderResourceID == a.SourceResourceID ||
		a.CreatedAt.IsZero() || a.CreatedAt.Nanosecond()%1000 != 0 || a.CreatedAt.After(time.Now()) {
		return ErrInvalid
	}
	return nil
}

type CreationRecorder func(context.Context, CreationAcknowledgement) error

// Started is durable evidence that the exact owned resource was independently
// visible before cleanup. Absence before that point can be delayed creation.
type CreationCleanup struct {
	Started       bool
	RecordStarted func(context.Context) error
}

// Receipt providers checkpoint a successful POST before polling correctness.
// A known acknowledgement pins recovery to its exact ID and forbids creation.
// Legacy discovery without an acknowledgement still needs full lineage proof.
type RestoreCreationProvider interface {
	RestoreWithCreationReceipt(context.Context, RestoreRequest, *CreationAcknowledgement, CreationRecorder) (ObservedDatabase, error)
	DeleteRestoreCreation(context.Context, RestoreRequest, CreationAcknowledgement, CreationCleanup) (DeleteResult, error)
}

type SnapshotCreationProvider interface {
	CaptureSnapshotWithCreationReceipt(context.Context, SnapshotCaptureRequest, *CreationAcknowledgement, CreationRecorder) (DatabaseSnapshot, error)
	ObserveSnapshotCreation(context.Context, SnapshotCaptureRequest, CreationAcknowledgement) (DatabaseSnapshot, error)
	DeleteSnapshotCreation(context.Context, SnapshotCaptureRequest, CreationAcknowledgement, CreationCleanup) (DeleteResult, error)
}

// CreationReceipt is private, immutable custody evidence. It deliberately lives
// outside the verified lifecycle and snapshot receipts used to publish data.
type CreationReceipt struct {
	Kind, ResourceID, AccountID, BackendID, BackendFingerprint string
	DatabaseID                                                 string // restore target; empty for operation-owned checkpoints
	Generation                                                 int64
	PointInTime                                                time.Time
	Acknowledgement                                            CreationAcknowledgement
	CleanupStartedAt                                           time.Time
}

// Database receipts require a current lifecycle lease. Snapshot receipts can
// only append evidence to an already dispatched durable clone intent; they do
// not advance that intent, release writers, or grant publication authority.
type CreationReceiptStore interface {
	RecordCreationReceipt(context.Context, CreationReceipt, string) error
	GetCreationReceipt(context.Context, string, string, string) (CreationReceipt, error)
	RecordCreationCleanup(context.Context, CreationReceipt, string) error
}

func (r CreationReceipt) Validate() error {
	if (r.Kind != "restore" && r.Kind != "snapshot") || !validOpaqueID(r.ResourceID) || !validOpaqueID(r.AccountID) ||
		!validOpaqueID(r.BackendID) || !backendFingerprintPattern.MatchString(r.BackendFingerprint) || r.Generation < 1 ||
		r.PointInTime.IsZero() || r.PointInTime.After(r.Acknowledgement.CreatedAt) || r.Acknowledgement.Validate() != nil ||
		r.Kind == "restore" && r.DatabaseID != r.ResourceID || r.Kind == "snapshot" && (r.DatabaseID != "" || r.Generation != 1) {
		return ErrInvalid
	}
	return nil
}

func sameCreationReceipt(a, b CreationReceipt) bool {
	return a.Kind == b.Kind && a.ResourceID == b.ResourceID && a.AccountID == b.AccountID && a.DatabaseID == b.DatabaseID &&
		a.BackendID == b.BackendID && a.BackendFingerprint == b.BackendFingerprint && a.Generation == b.Generation &&
		a.PointInTime.Equal(b.PointInTime) && a.Acknowledgement.ProviderResourceID == b.Acknowledgement.ProviderResourceID &&
		a.Acknowledgement.SourceResourceID == b.Acknowledgement.SourceResourceID && a.Acknowledgement.CreatedAt.Equal(b.Acknowledgement.CreatedAt)
}

func restoreCreationReceipt(d Database, a CreationAcknowledgement) CreationReceipt {
	return CreationReceipt{Kind: "restore", ResourceID: d.ID, DatabaseID: d.ID, AccountID: d.AccountID, BackendID: d.BackendID,
		BackendFingerprint: d.BackendFingerprint, Generation: d.DesiredGeneration, PointInTime: d.RestorePointInTime, Acknowledgement: a}
}

func (s *Service) restoreCreation(ctx context.Context, d Database) (*CreationAcknowledgement, error) {
	receipts, ok := s.store.(CreationReceiptStore)
	if !ok {
		return nil, ErrUnsupported
	}
	r, err := receipts.GetCreationReceipt(ctx, "restore", d.BackendID, d.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !sameCreationReceipt(r, restoreCreationReceipt(d, r.Acknowledgement)) || r.Validate() != nil || r.Acknowledgement.SourceResourceID != d.RestoreSourceResourceID {
		return nil, ErrConflict
	}
	return &r.Acknowledgement, nil
}

func (s *Service) reconcileRestoreCreation(ctx context.Context, p RestoreCreationProvider, d Database, request RestoreRequest) (ObservedDatabase, error) {
	expected, err := s.restoreCreation(ctx, d)
	if err != nil {
		return ObservedDatabase{}, err
	}
	return p.RestoreWithCreationReceipt(ctx, request, expected, func(ctx context.Context, a CreationAcknowledgement) error {
		if a.SourceResourceID != d.RestoreSourceResourceID {
			return ErrConflict
		}
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
		defer cancel()
		return s.store.(CreationReceiptStore).RecordCreationReceipt(writeCtx, restoreCreationReceipt(d, a), d.LeaseToken)
	})
}

func (s *Service) deleteRestoreCreation(ctx, providerCtx context.Context, p RestoreCreationProvider, d Database, accepted CreationAcknowledgement) (Database, error) {
	cleanup, err := s.creationCleanup(providerCtx, "restore", d.BackendID, d.ID, d.LeaseToken)
	if err != nil {
		return Database{}, s.releaseProviderError(ctx, d, StateDeleting, err)
	}
	result, err := p.DeleteRestoreCreation(providerCtx, RestoreRequest{ResourceID: d.ID, SourceResourceID: d.RestoreSourceResourceID,
		Spec: d.Spec, PointInTime: d.RestorePointInTime, IdempotencyKey: "delete-" + d.ID}, accepted, cleanup)
	if err != nil {
		return Database{}, s.releaseProviderError(ctx, d, StateDeleting, err)
	}
	if !result.Done {
		if err := s.release(ctx, d.ID, d.LeaseToken, StateDeleting, "", s.pollInterval); err != nil {
			return Database{}, err
		}
		return s.store.Get(ctx, d.AccountID, d.ID)
	}
	// Retain the accounting identity only after this owned cleanup has completed.
	if err := s.recordProviderResource(ctx, d.ID, d.LeaseToken, accepted.ProviderResourceID); err != nil {
		return Database{}, s.releaseProviderError(ctx, d, StateDeleting, err)
	}
	return s.finishDelete(ctx, d.ID, d.LeaseToken)
}

func snapshotCreationReceipt(account string, d RestoreSourceDefinition, r SnapshotCaptureRequest, a CreationAcknowledgement) CreationReceipt {
	return CreationReceipt{Kind: "snapshot", ResourceID: r.ResourceID, AccountID: account, BackendID: d.BackendID,
		BackendFingerprint: d.BackendFingerprint, Generation: 1, PointInTime: r.PointInTime, Acknowledgement: a}
}

// SnapshotCreationAcknowledgement retrieves custody only; it cannot satisfy a
// retained snapshot or release a coordinated capture's writer barrier.
func (s *Service) SnapshotCreationAcknowledgement(ctx context.Context, d RestoreSourceDefinition, request SnapshotCaptureRequest) (*CreationAcknowledgement, error) {
	if s == nil || request.SourceResourceID != d.DataResourceID {
		return nil, ErrInvalid
	}
	if _, err := s.snapshotProvider(d); err != nil {
		return nil, err
	}
	receipts, ok := s.store.(CreationReceiptStore)
	if !ok {
		return nil, nil
	} // legacy adapters retain their full-proof recovery
	r, err := receipts.GetCreationReceipt(ctx, "snapshot", d.BackendID, request.ResourceID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.Validate() != nil || !sameCreationReceipt(r, snapshotCreationReceipt(r.AccountID, d, request, r.Acknowledgement)) || r.Acknowledgement.SourceResourceID != request.SourceResourceID {
		return nil, ErrConflict
	}
	return &r.Acknowledgement, nil
}

func (s *Service) captureSnapshotCreation(ctx context.Context, account string, d RestoreSourceDefinition, request SnapshotCaptureRequest, p SnapshotCreationProvider) (DatabaseSnapshot, error) {
	receipts, ok := s.store.(CreationReceiptStore)
	if !ok {
		return DatabaseSnapshot{}, ErrUnsupported
	}
	expected, err := s.SnapshotCreationAcknowledgement(ctx, d, request)
	if err != nil {
		return DatabaseSnapshot{}, err
	}
	if expected != nil {
		receipt, err := receipts.GetCreationReceipt(ctx, "snapshot", d.BackendID, request.ResourceID)
		if err != nil {
			return DatabaseSnapshot{}, err
		}
		if receipt.AccountID != account {
			return DatabaseSnapshot{}, ErrConflict
		}
	}
	return p.CaptureSnapshotWithCreationReceipt(ctx, request, expected, func(ctx context.Context, a CreationAcknowledgement) error {
		if a.SourceResourceID != request.SourceResourceID {
			return ErrConflict
		}
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
		defer cancel()
		return receipts.RecordCreationReceipt(writeCtx, snapshotCreationReceipt(account, d, request, a), "")
	})
}

func (s *Service) creationCleanup(ctx context.Context, kind, backend, resource, lease string) (CreationCleanup, error) {
	store, ok := s.store.(CreationReceiptStore)
	if !ok {
		return CreationCleanup{}, ErrUnsupported
	}
	receipt, err := store.GetCreationReceipt(ctx, kind, backend, resource)
	if err != nil {
		return CreationCleanup{}, err
	}
	return CreationCleanup{Started: !receipt.CleanupStartedAt.IsZero(), RecordStarted: func(ctx context.Context) error {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
		defer cancel()
		return store.RecordCreationCleanup(writeCtx, receipt, lease)
	}}, nil
}
