// adr: 20261110160000 — reset connections before a capture and after a restore.
package fcvm

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// releaseOrderVMM records connection resets and snapshots in order.
type releaseOrderVMM struct {
	*fakeVMM
	mu     sync.Mutex
	events []string
}

func (v *releaseOrderVMM) ReleaseGuestConnections(_ context.Context, l Lease) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.events = append(v.events, "release:"+l.Instance)
	return 1, nil
}

func (v *releaseOrderVMM) Snapshot(ctx context.Context, l Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	v.mu.Lock()
	v.events = append(v.events, "snapshot:"+l.Instance)
	v.mu.Unlock()
	return v.fakeVMM.Snapshot(ctx, l, spec)
}

func (v *releaseOrderVMM) seen() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.events...)
}

func wakeForReset(t *testing.T, m *Manager, instance string, snapshot bool, keepPaused bool) *Instance {
	t.Helper()
	req := WakeRequest{Instance: instance, AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4",
		VcpuCount: 1, MemSizeMiB: 512, Plan: api.PlanPro, EgressMbit: 100, KeepPaused: keepPaused}
	if snapshot {
		req.Snapshot = usableSnapshot()
	}
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatalf("Wake %s: %v", instance, err)
	}
	return inst
}

func TestParkResetsConnectionsBeforeTheCapture(t *testing.T) {
	vmm := &releaseOrderVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(&fakeInputRunner{}, vmm).WithReleaseConnectionsOnDestroy(true)
	wakeForReset(t, m, "cap-a", false, false)
	if _, err := m.Park(t.Context(), "cap-a", SnapshotSpec{StorageKey: "snap/cap-a"}); err != nil {
		t.Fatalf("Park: %v", err)
	}
	events := vmm.seen()
	release, snapshot := slices.Index(events, "release:cap-a"), slices.Index(events, "snapshot:cap-a")
	if release < 0 || snapshot < 0 || release > snapshot {
		t.Fatalf("events = %v, want a reset before the snapshot", events)
	}
}

func TestRestoreResetsInheritedConnections(t *testing.T) {
	for _, tc := range []struct {
		name               string
		enabled, snapshot  bool
		keepPaused, expect bool
	}{
		{name: "restore", enabled: true, snapshot: true, expect: true},
		{name: "cold boot", enabled: true, snapshot: false, expect: false},
		{name: "warm spare stays paused", enabled: true, snapshot: true, keepPaused: true, expect: false},
		{name: "switch off", enabled: false, snapshot: true, expect: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm := &releaseOrderVMM{fakeVMM: &fakeVMM{}}
			m := newTestManager(&fakeInputRunner{}, vmm).WithReleaseConnectionsOnDestroy(tc.enabled)
			inst := wakeForReset(t, m, "res-a", tc.snapshot, tc.keepPaused)
			if tc.snapshot && !tc.keepPaused && inst.Method != WakeRestore {
				t.Fatalf("method = %v, want a restore", inst.Method)
			}
			if got := slices.Contains(vmm.seen(), "release:res-a"); got != tc.expect {
				t.Fatalf("reset after wake = %v, want %v (events %v)", got, tc.expect, vmm.seen())
			}
		})
	}
}
