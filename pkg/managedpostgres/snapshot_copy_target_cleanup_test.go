// adr: 581
package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

type serviceCopyCleanupProvider struct {
	serviceSnapshotCopyProvider
	discoveries, deletions, observations int
	cleanupFault                         string
}

func (p *serviceCopyCleanupProvider) cleanupResult(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error) {
	p.definition, p.copyRequest = d, r
	_, p.deadline = ctx.Deadline()
	o := SnapshotCopyTargetIdentity{ProviderResourceID: "independent-project", CreatedAt: r.CaptureCreatedAt.Add(time.Minute), Deleted: true}
	switch p.cleanupFault {
	case "source":
		o.ProviderResourceID = d.ProviderResourceID
	case "pin":
		o.ProviderResourceID = "another-project"
	case "time":
		o.CreatedAt = o.CreatedAt.Add(time.Second)
	case "missing":
		return SnapshotCopyTargetIdentity{}, ErrNotFound
	}
	return o, nil
}
func (p *serviceCopyCleanupProvider) DiscoverSnapshotCopyTargetForCleanup(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error) {
	p.discoveries++
	return p.cleanupResult(ctx, d, r)
}
func (p *serviceCopyCleanupProvider) DeleteSnapshotCopyTarget(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error) {
	p.deletions++
	return p.cleanupResult(ctx, d, r)
}
func (p *serviceCopyCleanupProvider) ObserveSnapshotCopyTargetDeletion(ctx context.Context, d RestoreSourceDefinition, r SnapshotCopyTargetRequest) (SnapshotCopyTargetIdentity, error) {
	p.observations++
	return p.cleanupResult(ctx, d, r)
}

func TestSnapshotCopyTargetCleanupServiceUngatedOwnershipAndProof(t *testing.T) {
	_, original, d, r := snapshotCopyServiceFixture(t)
	p := &serviceCopyCleanupProvider{serviceSnapshotCopyProvider: *original}
	s := testService(t, testRegistry(t, p, nil), NewMemoryStore())
	s.provisioningEnabled = func() bool { return false }
	o, err := s.DiscoverSnapshotCopyTargetForCleanup(t.Context(), d, r)
	if err != nil || p.discoveries != 1 || p.copyCreates != 0 || !p.deadline || p.copyRequest != r || p.definition != d {
		t.Fatalf("ungated discovery: %+v %v", o, err)
	}
	if _, err := s.DeleteSnapshotCopyTarget(t.Context(), d, r); !errors.Is(err, ErrInvalid) || p.deletions != 0 {
		t.Fatalf("unknown identity reached DELETE: %v", err)
	}
	r.ExpectedProviderResourceID, r.ExpectedCreatedAt = o.ProviderResourceID, o.CreatedAt
	if _, err := s.DeleteSnapshotCopyTarget(t.Context(), d, r); err != nil || p.deletions != 1 {
		t.Fatalf("ungated cleanup: %v", err)
	}
	for _, fault := range []string{"source", "pin", "time", "missing"} {
		p.cleanupFault = fault
		want := ErrConflict
		if fault == "missing" {
			want = ErrNotFound
		}
		if o, err := s.ObserveSnapshotCopyTargetDeletion(t.Context(), d, r); !errors.Is(err, want) || o != (SnapshotCopyTargetIdentity{}) {
			t.Fatalf("accepted %s cleanup proof: %+v %v", fault, o, err)
		}
	}
}
