package state

import (
	"context"
	"strings"
	"time"
)

// Snapshot receipts are private worker state. Retained means independently
// observed ownership and no automatic expiry. It is not proof of asynchronous
// completion, restorability, or application/database/object consistency.
type ProjectEnvironmentClonePostgresSnapshot struct {
	OperationID, SourceDatabaseID, SourceVersion                   string
	BackendID, BackendFingerprint                                  string
	SourceProviderResourceID, SourceDataResourceID                 string
	ProviderSnapshotID, State                                      string
	CapturePoint, SnapshotCreatedAt, ObservedAt, CleanupObservedAt time.Time
	RequestStartedAt                                               time.Time
}

type ProjectEnvironmentClonePostgresSnapshotObservation struct {
	ProviderSnapshotID, SourceDataResourceID string
	CapturePoint, CreatedAt                  time.Time
	ExpiresAt                                *time.Time
}

type ProjectEnvironmentClonePostgresSnapshotStore interface {
	ReserveProjectEnvironmentClonePostgresSnapshot(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresSnapshot, error)
	ClaimProjectEnvironmentClonePostgresSnapshotRequest(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresSnapshot, bool, error)
	ProjectEnvironmentClonePostgresSnapshotForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresSnapshot, error)
	RecordProjectEnvironmentClonePostgresSnapshot(context.Context, ProjectEnvironmentCloneLease, string, ProjectEnvironmentClonePostgresSnapshotObservation) (ProjectEnvironmentClonePostgresSnapshot, error)
	BeginProjectEnvironmentClonePostgresSnapshotCleanup(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresSnapshot, error)
	FinishProjectEnvironmentClonePostgresSnapshotCleanup(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresSnapshot, error)
}

func ProjectEnvironmentClonePostgresSnapshotOwner(operationID, sourceID string) string {
	return "environment-clone-" + operationID + "-" + sourceID
}

func validCloneSnapshotID(id string) bool {
	return id != "" && len(id) <= 255 && !strings.ContainsRune(id, '\x00')
}

func validateCloneSnapshotObservation(receipt ProjectEnvironmentClonePostgresSnapshot, observed ProjectEnvironmentClonePostgresSnapshotObservation) error {
	if !validCloneSnapshotID(observed.ProviderSnapshotID) || observed.SourceDataResourceID != receipt.SourceDataResourceID ||
		!observed.CapturePoint.Equal(receipt.CapturePoint) || observed.CreatedAt.IsZero() || observed.CreatedAt.Before(receipt.CapturePoint) ||
		observed.CreatedAt.Nanosecond()%1000 != 0 || receipt.State != "deleting" && observed.ExpiresAt != nil ||
		receipt.ProviderSnapshotID != "" && (receipt.ProviderSnapshotID != observed.ProviderSnapshotID || !receipt.SnapshotCreatedAt.Equal(observed.CreatedAt)) {
		return ErrConflict
	}
	return nil
}

var _ ProjectEnvironmentClonePostgresSnapshotStore = (*PgStore)(nil)
