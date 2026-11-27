package dbsched

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

type restoreCheckVMM struct {
	*fakeVMM
	node, id string
	major    int
	timeline uint32
}

func (v *restoreCheckVMM) CheckArchiveRestore(_ context.Context, node Node, id string, _ time.Time, major int) (dbarchive.RestoreCheck, error) {
	v.node, v.id, v.major = node.ID, id, major
	if v.timeline != 0 {
		return dbarchive.RestoreCheck{Outcome: dbarchive.RestoreCovered, Timeline: v.timeline}, nil
	}
	return dbarchive.RestoreCheck{Outcome: dbarchive.RestoreWALGap, Reason: "missing_wal_segment",
		MissingWAL: "000000010000000000000003"}, nil
}

func TestRestoreCheckLeavesSourceParkedAndCreatesNoTarget(t *testing.T) {
	s, store, fake := newTestScheduler(t, 4096)
	vmm := &restoreCheckVMM{fakeVMM: fake}
	s.vmm = vmm
	fake.archive = true
	source, err := s.Provision(t.Context(), ProvisionRequest{
		DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, RestoreWindowSeconds: 3600,
	})
	if err != nil {
		t.Fatal(err)
	}
	boots := fake.callCount("start")
	volumes := fake.callCount("create")
	check, err := s.CheckRestoreTarget(t.Context(), source.DatabaseID, source.CreatedAt)
	if err != nil || check.Outcome != dbarchive.RestoreWALGap || check.MissingWAL == "" {
		t.Fatalf("restore check = %+v, %v", check, err)
	}
	if vmm.node != source.NodeID || vmm.id != source.DatabaseID || vmm.major != source.Major {
		t.Fatalf("archive checked wrong placement/source: %+v", vmm)
	}
	stored, err := store.Get(t.Context(), source.DatabaseID)
	if err != nil || stored.State != StateParked || fake.callCount("start") != boots || fake.callCount("create") != volumes {
		t.Fatalf("archive check changed placement or booted source: %+v, %v", stored, err)
	}
}

func TestRestorePinsVerifiedTimelineBeforeBoot(t *testing.T) {
	s, store, fake := newTestScheduler(t, 4096)
	s.vmm = &restoreCheckVMM{fakeVMM: fake, timeline: 2}
	fake.archive = true
	source := provisionMajor(t, s, 16)
	id := uuid.NewString()
	if _, err := s.Restore(t.Context(), RestoreRequest{
		ProvisionRequest: ProvisionRequest{DatabaseID: id, Major: 16, VcpuCount: 1, MemMiB: 384, VolumeBytes: source.VolumeBytes},
		Source:           source.DatabaseID, Target: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	s.restoreWG.Wait()
	copy, err := store.Get(t.Context(), id)
	if err != nil || copy.State != StateParked || copy.RestoreTimeline != 2 || fake.restoreTimeline[id] != 2 {
		t.Fatalf("restore did not boot on the archived timeline: %+v, %v", copy, err)
	}
}
