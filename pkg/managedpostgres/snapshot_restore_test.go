// adr:375
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type serviceSnapshotRestoreProvider struct {
	fakeProvider
	creates, finds int
	definition     RestoreSourceDefinition
	request        SnapshotRestoreRequest
	deadline       bool
	fault          string
}

func (p *serviceSnapshotRestoreProvider) restoreResult(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreRequest) (SnapshotRestoreObservation, error) {
	p.definition, p.request = d, r
	_, p.deadline = ctx.Deadline()
	o := SnapshotRestoreObservation{ProviderResourceID: "project/target", ProviderSnapshotID: r.ProviderSnapshotID, SourceDataResourceID: d.DataResourceID,
		PointInTime: r.Snapshot.PointInTime, SnapshotCreatedAt: r.Snapshot.PointInTime.Add(time.Minute), TargetCreatedAt: r.Snapshot.PointInTime.Add(2 * time.Minute), Restored: true}
	switch p.fault {
	case "source":
		o.SourceDataResourceID = "project/other"
	case "target_source":
		o.ProviderResourceID = d.DataResourceID
	case "target_pin":
		o.ProviderResourceID = "project/other"
	case "snapshot":
		o.ProviderSnapshotID = "project/snapshots/other"
	case "point":
		o.PointInTime = o.PointInTime.Add(time.Second)
	case "created":
		o.TargetCreatedAt = o.SnapshotCreatedAt.Add(-time.Second)
	case "missing_created":
		o.SnapshotCreatedAt = time.Time{}
	case "future_created":
		o.TargetCreatedAt = time.Now().Add(time.Hour)
	case "missing":
		return SnapshotRestoreObservation{}, ErrNotFound
	}
	return o, nil
}

func (p *serviceSnapshotRestoreProvider) RestoreSnapshot(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreRequest) (SnapshotRestoreObservation, error) {
	p.creates++
	return p.restoreResult(ctx, d, r)
}
func (p *serviceSnapshotRestoreProvider) FindSnapshotRestore(ctx context.Context, d RestoreSourceDefinition, r SnapshotRestoreRequest) (SnapshotRestoreObservation, error) {
	p.finds++
	return p.restoreResult(ctx, d, r)
}

func snapshotRestoreServiceFixture(t *testing.T) (*Service, *serviceSnapshotRestoreProvider, RestoreSourceDefinition, SnapshotRestoreRequest) {
	t.Helper()
	p := &serviceSnapshotRestoreProvider{fakeProvider: fakeProvider{capabilities: testCapabilities()}}
	registry := testRegistry(t, p, nil)
	s := testService(t, registry, NewMemoryStore())
	backend, err := registry.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	d := RestoreSourceDefinition{Spec: testSpec(), BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "project", DataResourceID: "project/source"}
	r := SnapshotRestoreRequest{ResourceID: "owned-target", ProviderSnapshotID: "project/snapshots/owned", Snapshot: SnapshotCaptureRequest{
		ResourceID: "operation/source", SourceResourceID: d.DataResourceID, IdempotencyKey: "capture", PointInTime: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)}}
	return s, p, d, r
}

func TestSnapshotRestoreServicePreservesOwnedSelectorsAndRollout(t *testing.T) {
	s, p, d, r := snapshotRestoreServiceFixture(t)
	actual, err := s.RestoreSnapshot(t.Context(), "account", d, r)
	if err != nil || !actual.Restored || p.definition != d || p.request != r || !p.deadline || p.creates != 1 {
		t.Fatalf("owned restore: %+v %v", actual, err)
	}
	s.provisioningEnabled = func() bool { return false }
	if _, err := s.RestoreSnapshot(t.Context(), "account", d, r); !errors.Is(err, ErrUnavailable) || p.creates != 1 {
		t.Fatalf("disabled rollout created target: %v", err)
	}
	r.ExpectedTargetResourceID = actual.ProviderResourceID
	if _, err := s.FindSnapshotRestore(t.Context(), d, r); err != nil || p.finds != 1 || p.creates != 1 || p.request != r {
		t.Fatalf("disabled rollout prevented owned discovery: %v", err)
	}
	for _, fault := range []string{"backend", "fingerprint", "source", "region", "target_source", "missing_snapshot", "missing_owner", "point"} {
		t.Run(fault, func(t *testing.T) {
			definition, request := d, r
			switch fault {
			case "backend":
				definition.BackendID = "other"
			case "fingerprint":
				definition.BackendFingerprint = "changed"
			case "source":
				definition.DataResourceID += "-other"
			case "region":
				definition.Spec.Region = "eu-central-1"
			case "target_source":
				request.ExpectedTargetResourceID = definition.DataResourceID
			case "missing_snapshot":
				request.ProviderSnapshotID = ""
			case "missing_owner":
				request.ResourceID = ""
			case "point":
				request.Snapshot.PointInTime = time.Now().Add(time.Hour)
			}
			before := p.finds
			if _, err := s.FindSnapshotRestore(t.Context(), definition, request); err == nil || p.finds != before {
				t.Fatalf("invalid %s reached provider: %v", fault, err)
			}
		})
	}
}

func TestSnapshotRestoreServiceRejectsSubstitutedStorageProof(t *testing.T) {
	s, p, d, r := snapshotRestoreServiceFixture(t)
	r.ExpectedTargetResourceID = "project/target"
	for _, fault := range []string{"source", "target_source", "target_pin", "snapshot", "point", "created", "missing_created", "future_created", "missing"} {
		t.Run(fault, func(t *testing.T) {
			p.fault = fault
			want := ErrConflict
			if fault == "missing" {
				want = ErrNotFound
			}
			if actual, err := s.FindSnapshotRestore(t.Context(), d, r); !errors.Is(err, want) || actual != (SnapshotRestoreObservation{}) {
				t.Fatalf("accepted %s storage proof: %+v %v", fault, actual, err)
			}
			if p.creates != 0 {
				t.Fatal("proof discovery created target")
			}
		})
	}
}
