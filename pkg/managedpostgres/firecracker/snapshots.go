package firecracker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Snapshots for environment clones (ADR-1027, ADR-590). A native snapshot
// is schedd's hold on the source's WAL archive at the capture point, taken
// once the archive reaches it: nothing is copied, and it never expires
// until it is deleted. Restoring it is a point-in-time restore of the
// source into a new database, which the hold keeps possible outside the
// source's restore window. The snapshot id names the source database and
// the hold, which derives from the owning resource id, so a capture whose
// reply was lost is found again under the same id.

var (
	_ managedpostgres.SnapshotProvider                = (*Provider)(nil)
	_ managedpostgres.SnapshotRestoreProvider         = (*Provider)(nil)
	_ managedpostgres.SnapshotRestoreDeletionProvider = (*Provider)(nil)
)

// snapshotName is the hold name of the snapshot resourceID owns.
func snapshotName(resourceID string) string {
	sum := sha256.Sum256([]byte("gregale-native-snapshot\x00" + resourceID))
	return hex.EncodeToString(sum[:16])
}

func snapshotID(databaseID, name string) string { return databaseID + "/" + name }

// ownedSnapshot checks a capture request and returns its source database
// and hold name.
func ownedSnapshot(req managedpostgres.SnapshotCaptureRequest) (string, string, error) {
	source, err := uuid.Parse(req.SourceResourceID)
	if err != nil || source.String() != req.SourceResourceID || req.ResourceID == "" || len(req.ResourceID) > 255 ||
		req.PointInTime.IsZero() || req.PointInTime.Nanosecond()%1000 != 0 {
		return "", "", managedpostgres.ErrInvalid
	}
	return req.SourceResourceID, snapshotName(req.ResourceID), nil
}

// ownedSnapshotID checks that id is the snapshot req owns.
func ownedSnapshotID(req managedpostgres.SnapshotCaptureRequest, id string) (string, string, error) {
	source, name, err := ownedSnapshot(req)
	if err != nil {
		return "", "", err
	}
	if id != snapshotID(source, name) {
		return "", "", managedpostgres.ErrConflict
	}
	return source, name, nil
}

// CaptureSnapshot holds the source's archive at the request's point.
// Schedd first makes the archive reach the point (the source commits past
// it), so the snapshot is restorable when this returns.
func (p *Provider) CaptureSnapshot(ctx context.Context, req managedpostgres.SnapshotCaptureRequest) (managedpostgres.DatabaseSnapshot, error) {
	source, name, err := ownedSnapshot(req)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	if req.IdempotencyKey == "" || !req.PointInTime.Before(time.Now()) {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrInvalid
	}
	snap, err := p.scheduler.CaptureDatabaseSnapshot(ctx, &scheddpb.CaptureDatabaseSnapshotRequest{
		DatabaseId: source, Name: name, PointInTime: timestamppb.New(req.PointInTime),
	})
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, providerError(err)
	}
	return observedSnapshot(snap, source, name, req.PointInTime)
}

// FindSnapshot returns the snapshot the request owns, never creating one.
func (p *Provider) FindSnapshot(ctx context.Context, req managedpostgres.SnapshotCaptureRequest) (managedpostgres.DatabaseSnapshot, error) {
	source, name, err := ownedSnapshot(req)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	return p.findSnapshot(ctx, source, name, req.PointInTime)
}

// RetainSnapshot returns the snapshot as it is: a native snapshot is
// already held until it is deleted.
func (p *Provider) RetainSnapshot(ctx context.Context, req managedpostgres.SnapshotCaptureRequest, id string) (managedpostgres.DatabaseSnapshot, error) {
	source, name, err := ownedSnapshotID(req, id)
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, err
	}
	return p.findSnapshot(ctx, source, name, req.PointInTime)
}

// InspectSnapshot reports a snapshot by id.
func (p *Provider) InspectSnapshot(ctx context.Context, id string) (managedpostgres.DatabaseSnapshot, error) {
	source, name, ok := strings.Cut(id, "/")
	if _, err := uuid.Parse(source); err != nil || !ok || snapshotID(source, name) != id || len(name) != 32 {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrInvalid
	}
	return p.findSnapshot(ctx, source, name, time.Time{})
}

// DeleteSnapshot releases the snapshot's hold; a snapshot already gone,
// or whose source was deleted with its archive, is done.
func (p *Provider) DeleteSnapshot(ctx context.Context, req managedpostgres.SnapshotDeleteRequest) (managedpostgres.DeleteResult, error) {
	capture := managedpostgres.SnapshotCaptureRequest{ResourceID: req.ResourceID, SourceResourceID: req.SourceResourceID, PointInTime: req.PointInTime}
	source, name, err := ownedSnapshotID(capture, req.ProviderSnapshotID)
	if err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	_, err = p.findSnapshot(ctx, source, name, req.PointInTime)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		return managedpostgres.DeleteResult{Done: true}, nil
	}
	if err != nil {
		return managedpostgres.DeleteResult{}, err
	}
	if _, err := p.scheduler.DeleteDatabaseSnapshot(ctx, &scheddpb.DatabaseSnapshotRef{DatabaseId: source, Name: name}); err != nil {
		return managedpostgres.DeleteResult{}, providerError(err)
	}
	return managedpostgres.DeleteResult{Done: true}, nil
}

// findSnapshot reads a snapshot; a zero point accepts any.
func (p *Provider) findSnapshot(ctx context.Context, source, name string, point time.Time) (managedpostgres.DatabaseSnapshot, error) {
	snap, err := p.scheduler.GetDatabaseSnapshot(ctx, &scheddpb.DatabaseSnapshotRef{DatabaseId: source, Name: name})
	if err != nil {
		return managedpostgres.DatabaseSnapshot{}, providerError(err)
	}
	return observedSnapshot(snap, source, name, point)
}

// observedSnapshot checks schedd's snapshot against the one expected. Its
// times keep microseconds, as the clone receipts do.
func observedSnapshot(snap *scheddpb.DatabaseSnapshot, source, name string, point time.Time) (managedpostgres.DatabaseSnapshot, error) {
	if snap.GetDatabaseId() != source || snap.GetName() != name || snap.GetPointInTime() == nil || snap.GetCreatedAt() == nil {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	at, created := snap.GetPointInTime().AsTime().Truncate(time.Microsecond), snap.GetCreatedAt().AsTime().Truncate(time.Microsecond)
	if !point.IsZero() && !at.Equal(point) || created.Before(at) {
		return managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	return managedpostgres.DatabaseSnapshot{ProviderSnapshotID: snapshotID(source, name), SourceResourceID: source, PointInTime: at, CreatedAt: created}, nil
}

// RestoreSnapshot restores the snapshot into the database the request's
// resource id names, with the source's spec. Schedd restores it in the
// background; the observation reports Restored once the database has
// recovered and taken its first base backup.
func (p *Provider) RestoreSnapshot(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, req managedpostgres.SnapshotRestoreRequest) (managedpostgres.SnapshotRestoreObservation, error) {
	target, snap, err := p.snapshotRestore(ctx, definition, req)
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	if !p.Capabilities().PointInTimeRestore {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrUnsupported
	}
	if err := p.supports(definition.Spec); err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	_, name, _ := strings.Cut(req.ProviderSnapshotID, "/")
	rt, err := p.scheduler.RestoreDatabase(ctx, &scheddpb.RestoreDatabaseRequest{
		Database: p.provisionRequest(target, definition.Spec), SourceDatabaseId: snap.SourceResourceID,
		PointInTime: timestamppb.New(snap.PointInTime), Snapshot: name,
	})
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, providerError(err)
	}
	return restoreObservation(rt, snap)
}

// FindSnapshotRestore reports the restore the request owns, never
// starting one.
func (p *Provider) FindSnapshotRestore(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, req managedpostgres.SnapshotRestoreRequest) (managedpostgres.SnapshotRestoreObservation, error) {
	target, snap, err := p.snapshotRestore(ctx, definition, req)
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, err
	}
	rt, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: target})
	if err != nil {
		return managedpostgres.SnapshotRestoreObservation{}, providerError(err)
	}
	return restoreObservation(rt, snap)
}

// snapshotRestore checks a restore request and returns its target
// database and the snapshot as schedd holds it.
func (p *Provider) snapshotRestore(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, req managedpostgres.SnapshotRestoreRequest) (string, managedpostgres.DatabaseSnapshot, error) {
	if err := req.Validate(); err != nil || req.Snapshot.SourceResourceID != definition.DataResourceID {
		return "", managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrInvalid
	}
	target := resourceUUID(req.ResourceID)
	if target == req.Snapshot.SourceResourceID || req.ExpectedTargetResourceID != "" && req.ExpectedTargetResourceID != target {
		return "", managedpostgres.DatabaseSnapshot{}, managedpostgres.ErrConflict
	}
	source, name, err := ownedSnapshotID(req.Snapshot, req.ProviderSnapshotID)
	if err != nil {
		return "", managedpostgres.DatabaseSnapshot{}, err
	}
	snap, err := p.findSnapshot(ctx, source, name, req.Snapshot.PointInTime)
	if errors.Is(err, managedpostgres.ErrNotFound) {
		// Released before its restore was observed: nothing proves the fork.
		err = managedpostgres.ErrConflict
	}
	return target, snap, err
}

// restoreObservation reports a restored database against its snapshot: its
// recorded lineage must be the snapshot's source and point. A failed
// restore never completes.
func restoreObservation(rt *scheddpb.DatabaseRuntime, snap managedpostgres.DatabaseSnapshot) (managedpostgres.SnapshotRestoreObservation, error) {
	if rt.GetRestoreSourceId() != snap.SourceResourceID || rt.GetRestoreTarget() == nil || !rt.GetRestoreTarget().AsTime().Equal(snap.PointInTime) ||
		rt.GetCreatedAt() == nil || rt.GetState() == "failed" || rt.GetState() == "deleting" {
		return managedpostgres.SnapshotRestoreObservation{}, managedpostgres.ErrConflict
	}
	return managedpostgres.SnapshotRestoreObservation{
		ProviderResourceID: rt.GetDatabaseId(), ProviderSnapshotID: snap.ProviderSnapshotID, SourceDataResourceID: snap.SourceResourceID,
		PointInTime: snap.PointInTime, SnapshotCreatedAt: snap.CreatedAt, TargetCreatedAt: rt.GetCreatedAt().AsTime().Truncate(time.Microsecond),
		Restored: rt.GetState() != "provisioning" && rt.GetBootstrapped(),
	}, nil
}

// restoreDeletionOperation names the one operation deleting a restored
// database: schedd's delete of it, which finishes before it answers.
func restoreDeletionOperation(target string) string { return "schedd-delete/" + target }

// DeleteSnapshotRestore deletes a restored database the clone no longer
// needs, after checking it is the one the receipts recorded.
func (p *Provider) DeleteSnapshotRestore(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, req managedpostgres.SnapshotRestoreDeletionRequest) (managedpostgres.SnapshotRestoreDeletionObservation, error) {
	return p.snapshotRestoreDeletion(ctx, definition, req, true)
}

// ObserveSnapshotRestoreDeletion reports whether the restored database is
// gone, deleting nothing.
func (p *Provider) ObserveSnapshotRestoreDeletion(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, req managedpostgres.SnapshotRestoreDeletionRequest) (managedpostgres.SnapshotRestoreDeletionObservation, error) {
	return p.snapshotRestoreDeletion(ctx, definition, req, false)
}

func (p *Provider) snapshotRestoreDeletion(ctx context.Context, definition managedpostgres.RestoreSourceDefinition, req managedpostgres.SnapshotRestoreDeletionRequest, mutate bool) (managedpostgres.SnapshotRestoreDeletionObservation, error) {
	if err := req.Validate(); err != nil || req.Restore.Snapshot.SourceResourceID != definition.DataResourceID {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrInvalid
	}
	target := req.Restore.ExpectedTargetResourceID
	op := restoreDeletionOperation(target)
	if target != resourceUUID(req.Restore.ResourceID) || len(req.OperationIDs) > 1 || len(req.OperationIDs) == 1 && req.OperationIDs[0] != op {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrConflict
	}
	if _, _, err := ownedSnapshotID(req.Restore.Snapshot, req.Restore.ProviderSnapshotID); err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	done := managedpostgres.SnapshotRestoreDeletionObservation{ProviderResourceID: target, OperationIDs: []string{op}, Done: true}
	rt, err := p.scheduler.GetDatabase(ctx, &scheddpb.DatabaseRef{DatabaseId: target})
	if status.Code(err) == codes.NotFound {
		// Schedd forgets a database only once it is deleted.
		return done, nil
	}
	if err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, providerError(err)
	}
	// Only the database the receipts recorded: same lineage, same birth.
	if rt.GetRestoreSourceId() != req.Restore.Snapshot.SourceResourceID || rt.GetRestoreTarget() == nil ||
		!rt.GetRestoreTarget().AsTime().Equal(req.Restore.Snapshot.PointInTime) || rt.GetCreatedAt() == nil ||
		!rt.GetCreatedAt().AsTime().Truncate(time.Microsecond).Equal(req.TargetCreatedAt) {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, managedpostgres.ErrConflict
	}
	if !mutate {
		return managedpostgres.SnapshotRestoreDeletionObservation{ProviderResourceID: target, OperationIDs: req.OperationIDs}, nil
	}
	if _, err := p.Delete(ctx, managedpostgres.DeleteRequest{ProviderResourceID: target}); err != nil {
		return managedpostgres.SnapshotRestoreDeletionObservation{}, err
	}
	return done, nil
}
