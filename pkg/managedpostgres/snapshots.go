package managedpostgres

import (
	"context"
	"time"
)

// SnapshotCaptureRequest identifies one operation-owned checkpoint. Its source
// must be an exact dataset identity and its point must come from the coordinated
// capture owner; adapters must not select a new point or a mutable default.
type SnapshotCaptureRequest struct {
	ResourceID, SourceResourceID, IdempotencyKey string
	PointInTime                                  time.Time
}

// DatabaseSnapshot is observed identity and retention, not proof of completed
// asynchronous creation, restorability, or stage readiness. A nil ExpiresAt
// means the provider explicitly reported no automatic expiry, not missing data.
type DatabaseSnapshot struct {
	ProviderSnapshotID, SourceResourceID string
	PointInTime, CreatedAt               time.Time
	ExpiresAt                            *time.Time
}

type SnapshotDeleteRequest struct {
	ResourceID, ProviderSnapshotID, SourceResourceID string
	PointInTime                                      time.Time
}

// SnapshotProvider is optional. Complete clone capture must persist and verify
// its checkpoint before releasing source writers, and retain ownership until
// copying/compensation no longer needs it. Supporting this interface alone does
// not establish that protocol or change ordinary database provisioning.
type SnapshotProvider interface {
	CaptureSnapshot(context.Context, SnapshotCaptureRequest) (DatabaseSnapshot, error)
	// FindSnapshot recovers an owned intent without creating a new copy. It
	// is also needed for compensation after a lost creation acknowledgement.
	FindSnapshot(context.Context, SnapshotCaptureRequest) (DatabaseSnapshot, error)
	// RetainSnapshot can only change retention on this exact existing copy;
	// it must never create a replacement after an uncertain creation outcome.
	RetainSnapshot(context.Context, SnapshotCaptureRequest, string) (DatabaseSnapshot, error)
	InspectSnapshot(context.Context, string) (DatabaseSnapshot, error)
	DeleteSnapshot(context.Context, SnapshotDeleteRequest) (DeleteResult, error)
}
