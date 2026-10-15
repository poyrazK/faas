// adr: 1027
package firecracker

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func (f *fakeScheduler) CaptureDatabaseSnapshot(_ context.Context, in *scheddpb.CaptureDatabaseSnapshotRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.vms[in.GetDatabaseId()]; !ok {
		return nil, status.Error(codes.NotFound, "dbsched: database not found")
	}
	key := in.GetDatabaseId() + "/" + in.GetName()
	if snap, ok := f.snapshots[key]; ok {
		if !snap.GetPointInTime().AsTime().Equal(in.GetPointInTime().AsTime()) {
			return nil, status.Error(codes.FailedPrecondition, "dbsched: snapshot holds another point")
		}
		return snap, nil
	}
	if f.snapshots == nil {
		f.snapshots = map[string]*scheddpb.DatabaseSnapshot{}
	}
	snap := &scheddpb.DatabaseSnapshot{DatabaseId: in.GetDatabaseId(), Name: in.GetName(), PointInTime: in.GetPointInTime(),
		CreatedAt: timestamppb.New(time.Now().Truncate(time.Microsecond))}
	f.snapshots[key] = snap
	return snap, nil
}

func (f *fakeScheduler) GetDatabaseSnapshot(_ context.Context, in *scheddpb.DatabaseSnapshotRef, _ ...grpc.CallOption) (*scheddpb.DatabaseSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snap, ok := f.snapshots[in.GetDatabaseId()+"/"+in.GetName()]
	if !ok {
		return nil, status.Error(codes.NotFound, "dbsched: no such snapshot")
	}
	return snap, nil
}

func (f *fakeScheduler) DeleteDatabaseSnapshot(_ context.Context, in *scheddpb.DatabaseSnapshotRef, _ ...grpc.CallOption) (*scheddpb.DeleteDatabaseSnapshotResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snapshots, in.GetDatabaseId()+"/"+in.GetName())
	return &scheddpb.DeleteDatabaseSnapshotResponse{}, nil
}

const snapshotTarget = "1b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"

func snapshotSource(t *testing.T) (*Provider, *fakeScheduler, managedpostgres.Spec, managedpostgres.SnapshotCaptureRequest) {
	t.Helper()
	p, sched := newPITRProvider(t)
	spec := devSpec()
	spec.RestoreWindowSeconds = 86400
	if _, err := p.Provision(context.Background(), managedpostgres.ProvisionRequest{ResourceID: testDatabaseID, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	point := time.Now().Add(-time.Second).UTC().Truncate(time.Microsecond)
	return p, sched, spec, managedpostgres.SnapshotCaptureRequest{ResourceID: "clone-op/source-1", SourceResourceID: testDatabaseID, IdempotencyKey: "capture-1", PointInTime: point}
}

// A native snapshot is found again under the id its owner derives, never
// expires, and is gone once deleted.
func TestSnapshotLifecycle(t *testing.T) {
	p, sched, _, req := snapshotSource(t)
	ctx := context.Background()
	snap, err := p.CaptureSnapshot(ctx, req)
	if err != nil || snap.ProviderSnapshotID != testDatabaseID+"/"+snapshotName(req.ResourceID) || snap.SourceResourceID != testDatabaseID ||
		!snap.PointInTime.Equal(req.PointInTime) || snap.CreatedAt.Before(req.PointInTime) || snap.CreatedAt.Nanosecond()%1000 != 0 || snap.ExpiresAt != nil {
		t.Fatalf("capture = %+v, %v", snap, err)
	}
	for name, call := range map[string]func() (managedpostgres.DatabaseSnapshot, error){
		"capture again": func() (managedpostgres.DatabaseSnapshot, error) { return p.CaptureSnapshot(ctx, req) },
		"find":          func() (managedpostgres.DatabaseSnapshot, error) { return p.FindSnapshot(ctx, req) },
		"retain": func() (managedpostgres.DatabaseSnapshot, error) {
			return p.RetainSnapshot(ctx, req, snap.ProviderSnapshotID)
		},
		"inspect": func() (managedpostgres.DatabaseSnapshot, error) {
			return p.InspectSnapshot(ctx, snap.ProviderSnapshotID)
		},
	} {
		if got, err := call(); err != nil || got != snap {
			t.Errorf("%s = %+v, %v; want %+v", name, got, err, snap)
		}
	}
	moved := req
	moved.PointInTime = req.PointInTime.Add(-time.Second)
	if _, err := p.CaptureSnapshot(ctx, moved); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Errorf("another point = %v, want ErrConflict", err)
	}
	if _, err := p.FindSnapshot(ctx, moved); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Errorf("find at another point = %v, want ErrConflict", err)
	}
	if _, err := p.RetainSnapshot(ctx, req, testDatabaseID+"/"+snapshotName("someone else")); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Errorf("retain of another owner's id = %v, want ErrConflict", err)
	}
	for _, bad := range []string{"", "x", testDatabaseID + "/short", "nope/" + snapshotName("x")} {
		if _, err := p.InspectSnapshot(ctx, bad); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Errorf("inspect %q = %v, want ErrInvalid", bad, err)
		}
	}
	for name, mutate := range map[string]func(*managedpostgres.SnapshotCaptureRequest){
		"no owner":        func(r *managedpostgres.SnapshotCaptureRequest) { r.ResourceID = "" },
		"no key":          func(r *managedpostgres.SnapshotCaptureRequest) { r.IdempotencyKey = "" },
		"source not uuid": func(r *managedpostgres.SnapshotCaptureRequest) { r.SourceResourceID = "branch" },
		"nanoseconds":     func(r *managedpostgres.SnapshotCaptureRequest) { r.PointInTime = r.PointInTime.Add(1) },
		"future": func(r *managedpostgres.SnapshotCaptureRequest) {
			r.PointInTime = time.Now().Add(time.Hour).Truncate(time.Microsecond)
		},
	} {
		bad := req
		mutate(&bad)
		if _, err := p.CaptureSnapshot(ctx, bad); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Errorf("%s: capture = %v, want ErrInvalid", name, err)
		}
	}
	del := managedpostgres.SnapshotDeleteRequest{ResourceID: req.ResourceID, ProviderSnapshotID: snap.ProviderSnapshotID, SourceResourceID: testDatabaseID, PointInTime: req.PointInTime}
	for range 2 {
		if res, err := p.DeleteSnapshot(ctx, del); err != nil || !res.Done {
			t.Fatalf("delete = %+v, %v", res, err)
		}
	}
	if len(sched.snapshots) != 0 {
		t.Fatalf("snapshots left: %v", sched.snapshots)
	}
	if _, err := p.FindSnapshot(ctx, req); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("find after delete = %v, want ErrNotFound", err)
	}
}

// Restoring a snapshot creates the target the owner names, recovered to
// the snapshot's point; deleting it removes only that database.
func TestSnapshotRestoreAndItsDeletion(t *testing.T) {
	p, sched, spec, capture := snapshotSource(t)
	ctx := context.Background()
	snap, err := p.CaptureSnapshot(ctx, capture)
	if err != nil {
		t.Fatal(err)
	}
	definition := managedpostgres.RestoreSourceDefinition{Spec: spec, BackendID: "native", BackendFingerprint: "fp", ProviderResourceID: testDatabaseID, DataResourceID: testDatabaseID}
	req := managedpostgres.SnapshotRestoreRequest{ResourceID: snapshotTarget, ProviderSnapshotID: snap.ProviderSnapshotID, Snapshot: capture}
	if _, err := p.FindSnapshotRestore(ctx, definition, req); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("find before the restore = %v, want ErrNotFound", err)
	}
	obs, err := p.RestoreSnapshot(ctx, definition, req)
	if err != nil || obs.ProviderResourceID != snapshotTarget || obs.ProviderSnapshotID != snap.ProviderSnapshotID || obs.SourceDataResourceID != testDatabaseID ||
		!obs.PointInTime.Equal(capture.PointInTime) || !obs.SnapshotCreatedAt.Equal(snap.CreatedAt) || obs.TargetCreatedAt.Before(obs.SnapshotCreatedAt) || obs.Restored {
		t.Fatalf("restore = %+v, %v", obs, err)
	}
	sent := sched.restores[0]
	if sent.GetSnapshot() != snapshotName(capture.ResourceID) || sent.GetSourceDatabaseId() != testDatabaseID || !sent.GetPointInTime().AsTime().Equal(capture.PointInTime) ||
		sent.GetDatabase().GetDatabaseId() != snapshotTarget || sent.GetDatabase().GetRestoreWindowSeconds() != spec.RestoreWindowSeconds {
		t.Fatalf("restore request = %+v", sent)
	}
	sched.mu.Lock()
	sched.vms[snapshotTarget].State, sched.vms[snapshotTarget].Bootstrapped = "parked", true
	sched.mu.Unlock()
	req.ExpectedTargetResourceID = snapshotTarget
	done, err := p.FindSnapshotRestore(ctx, definition, req)
	if err != nil || !done.Restored || done.TargetCreatedAt != obs.TargetCreatedAt {
		t.Fatalf("find after the restore = %+v, %v", done, err)
	}
	other := req
	other.ExpectedTargetResourceID = "2b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"
	if _, err := p.FindSnapshotRestore(ctx, definition, other); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("another expected target = %v, want ErrConflict", err)
	}

	deletion := managedpostgres.SnapshotRestoreDeletionRequest{Restore: req, SnapshotCreatedAt: snap.CreatedAt, TargetCreatedAt: obs.TargetCreatedAt}
	stale := deletion
	stale.TargetCreatedAt = obs.TargetCreatedAt.Add(-time.Microsecond)
	if _, err := p.DeleteSnapshotRestore(ctx, definition, stale); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("delete of a database born at another time = %v, want ErrConflict", err)
	}
	if seen, err := p.ObserveSnapshotRestoreDeletion(ctx, definition, deletion); err != nil || seen.Done || len(seen.OperationIDs) != 0 {
		t.Fatalf("observe before the delete = %+v, %v", seen, err)
	}
	gone, err := p.DeleteSnapshotRestore(ctx, definition, deletion)
	if err != nil || !gone.Done || gone.ProviderResourceID != snapshotTarget || len(gone.OperationIDs) != 1 {
		t.Fatalf("delete = %+v, %v", gone, err)
	}
	if _, ok := sched.vms[snapshotTarget]; ok {
		t.Fatal("the restored database survived")
	}
	if _, ok := sched.vms[testDatabaseID]; !ok {
		t.Fatal("deleting the restore deleted its source")
	}
	deletion.OperationIDs = gone.OperationIDs
	if seen, err := p.ObserveSnapshotRestoreDeletion(ctx, definition, deletion); err != nil || !seen.Done || seen.OperationIDs[0] != gone.OperationIDs[0] {
		t.Fatalf("observe after the delete = %+v, %v", seen, err)
	}
	deletion.OperationIDs = []string{"another"}
	if _, err := p.ObserveSnapshotRestoreDeletion(ctx, definition, deletion); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("another operation = %v, want ErrConflict", err)
	}
}

// A failed restore never completes, and a released snapshot proves no
// fork.
func TestSnapshotRestoreRefusals(t *testing.T) {
	p, sched, spec, capture := snapshotSource(t)
	ctx := context.Background()
	snap, err := p.CaptureSnapshot(ctx, capture)
	if err != nil {
		t.Fatal(err)
	}
	definition := managedpostgres.RestoreSourceDefinition{Spec: spec, BackendID: "native", BackendFingerprint: "fp", ProviderResourceID: testDatabaseID, DataResourceID: testDatabaseID}
	req := managedpostgres.SnapshotRestoreRequest{ResourceID: snapshotTarget, ProviderSnapshotID: snap.ProviderSnapshotID, Snapshot: capture}
	if _, err := p.RestoreSnapshot(ctx, definition, req); err != nil {
		t.Fatal(err)
	}
	sched.setState(snapshotTarget, "failed")
	if _, err := p.FindSnapshotRestore(ctx, definition, req); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("failed restore = %v, want ErrConflict", err)
	}
	other := definition
	other.DataResourceID = snapshotTarget
	if _, err := p.RestoreSnapshot(ctx, other, req); !errors.Is(err, managedpostgres.ErrInvalid) {
		t.Fatalf("another source definition = %v, want ErrInvalid", err)
	}
	if _, err := p.DeleteSnapshot(ctx, managedpostgres.SnapshotDeleteRequest{ResourceID: capture.ResourceID, ProviderSnapshotID: snap.ProviderSnapshotID, SourceResourceID: testDatabaseID, PointInTime: capture.PointInTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.FindSnapshotRestore(ctx, definition, req); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("restore of a released snapshot = %v, want ErrConflict", err)
	}
}
