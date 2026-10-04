// adr: 568
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type serviceSnapshotProvider struct {
	fakeProvider
	captures, finds, deletes int
	absent                   bool
	deadlines                bool
	lastCapture              SnapshotCaptureRequest
	lastDelete               SnapshotDeleteRequest
}

func (p *serviceSnapshotProvider) CaptureSnapshot(ctx context.Context, r SnapshotCaptureRequest) (DatabaseSnapshot, error) {
	p.captures++
	p.lastCapture = r
	_, p.deadlines = ctx.Deadline()
	return DatabaseSnapshot{ProviderSnapshotID: "project/snapshots/snapshot", SourceResourceID: r.SourceResourceID, PointInTime: r.PointInTime, CreatedAt: r.PointInTime.Add(time.Second)}, nil
}
func (p *serviceSnapshotProvider) FindSnapshot(ctx context.Context, r SnapshotCaptureRequest) (DatabaseSnapshot, error) {
	p.finds++
	_, p.deadlines = ctx.Deadline()
	if p.absent {
		return DatabaseSnapshot{}, ErrNotFound
	}
	return DatabaseSnapshot{ProviderSnapshotID: "project/snapshots/snapshot", SourceResourceID: r.SourceResourceID, PointInTime: r.PointInTime, CreatedAt: r.PointInTime.Add(time.Second)}, nil
}
func (p *serviceSnapshotProvider) InspectSnapshot(context.Context, string) (DatabaseSnapshot, error) {
	return DatabaseSnapshot{}, ErrUnsupported
}
func (p *serviceSnapshotProvider) DeleteSnapshot(ctx context.Context, r SnapshotDeleteRequest) (DeleteResult, error) {
	p.deletes++
	p.lastDelete = r
	_, p.deadlines = ctx.Deadline()
	return DeleteResult{Done: true}, nil
}

func TestSnapshotServiceUsesPinnedBackendAndExactPoint(t *testing.T) {
	provider := &serviceSnapshotProvider{fakeProvider: fakeProvider{capabilities: testCapabilities()}}
	registry := testRegistry(t, provider, nil)
	service := testService(t, registry, NewMemoryStore())
	backend, err := registry.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	definition := RestoreSourceDefinition{Spec: testSpec(), BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "project", DataResourceID: "project/branch"}
	request := SnapshotCaptureRequest{ResourceID: "operation/database", SourceResourceID: definition.DataResourceID, PointInTime: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour), IdempotencyKey: "checkpoint"}
	actual, err := service.CaptureSnapshot(t.Context(), "account", definition, request)
	if err != nil || actual.SourceResourceID != request.SourceResourceID || provider.lastCapture != request || !provider.deadlines {
		t.Fatalf("capture = %+v, %v", actual, err)
	}
	for _, fault := range []string{"backend", "fingerprint", "source", "rollout", "account"} {
		t.Run(fault, func(t *testing.T) {
			d := definition
			service.provisioningEnabled = func() bool { return true }
			service.provisioningAllowed = func(context.Context, string) bool { return true }
			switch fault {
			case "backend":
				d.BackendID = "missing"
			case "fingerprint":
				d.BackendFingerprint = "changed"
			case "source":
				d.DataResourceID = "project/other"
			case "rollout":
				service.provisioningEnabled = func() bool { return false }
			case "account":
				service.provisioningAllowed = func(context.Context, string) bool { return false }
			}
			if _, err := service.CaptureSnapshot(t.Context(), "account", d, request); err == nil {
				t.Fatalf("%s captured", fault)
			}
			if provider.captures != 1 {
				t.Fatal("rejected capture reached provider")
			}
		})
	}
}

func TestSnapshotCleanupDiscoversUnknownIntentWithoutCreating(t *testing.T) {
	for _, mode := range []string{"unknown_present", "unknown_absent", "known_absent_name"} {
		t.Run(mode, func(t *testing.T) {
			provider := &serviceSnapshotProvider{fakeProvider: fakeProvider{capabilities: testCapabilities()}, absent: mode != "unknown_present"}
			registry := testRegistry(t, provider, nil)
			service := testService(t, registry, NewMemoryStore())
			service.provisioningEnabled = func() bool { return false }
			service.provisioningAllowed = func(context.Context, string) bool { return false }
			backend, _ := registry.Default(testSpec().Region)
			definition := RestoreSourceDefinition{Spec: testSpec(), BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "project", DataResourceID: "project/branch"}
			request := SnapshotCaptureRequest{ResourceID: "operation/database", SourceResourceID: definition.DataResourceID, PointInTime: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour), IdempotencyKey: "checkpoint"}
			expected := ""
			if mode == "known_absent_name" {
				expected = "project/snapshots/known"
			}
			result, err := service.DeleteSnapshot(t.Context(), definition, request, expected)
			if err != nil || !result.Done || provider.captures != 0 {
				t.Fatalf("cleanup = %+v, %v", result, err)
			}
			if mode == "unknown_absent" {
				if provider.deletes != 0 || provider.finds != 1 {
					t.Fatal("missing intent mutated provider")
				}
			} else {
				want := expected
				if want == "" {
					want = "project/snapshots/snapshot"
				}
				if provider.deletes != 1 || provider.lastDelete.ProviderSnapshotID != want || !provider.deadlines {
					t.Fatal("cleanup did not authenticate exact snapshot")
				}
				if mode == "known_absent_name" && provider.finds != 0 {
					t.Fatal("name lookup substituted for exact-ID absence")
				}
			}
			definition.DataResourceID = "project/other"
			if _, err := service.DeleteSnapshot(t.Context(), definition, request, expected); !errors.Is(err, ErrInvalid) {
				t.Fatalf("changed source cleanup: %v", err)
			}
		})
	}
}

func (p *serviceSnapshotProvider) RetainSnapshot(ctx context.Context, r SnapshotCaptureRequest, id string) (DatabaseSnapshot, error) {
	if id != "project/snapshots/snapshot" {
		return DatabaseSnapshot{}, ErrConflict
	}
	return p.FindSnapshot(ctx, r)
}
