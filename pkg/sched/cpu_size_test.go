// adr: 20261109172421

package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// An app instance's CPU follows its size, never its plan: one vCPU per GB, a
// weight of one per 16 MB, its own CPU value (vmmd runs the default without a
// quota), and a booking of its RAM's baseline bounded by what it can use.
func TestAppCPUFollowsInstanceSize(t *testing.T) {
	for _, tc := range []struct {
		ramMB, chosen       int
		vcpus, cpu, booking int
		weight              int32
	}{
		{128, 0, 1, 1000, 125, 8},
		{512, 0, 1, 1000, 500, 32},
		{1024, 0, 1, 1000, 1000, 64},
		{1536, 0, 2, 1000, 1500, 96},
		{2048, 0, 2, 1000, 2000, 128},
		{2048, 250, 2, 250, 250, 128}, // a chosen cap stays and bounds the booking
		{4096, 0, 4, 1000, 2000, 256}, // the booking stops at AppCPUBookingMaxMillicores
	} {
		app := state.App{ID: uuid.NewString(), RAMMB: tc.ramMB, CPUMillicores: tc.chosen}
		if got := sched.AppVCPUs(app); got != tc.vcpus {
			t.Errorf("%d MB: vCPUs = %d, want %d", tc.ramMB, got, tc.vcpus)
		}
		if got := sched.AppCPUMillicores(app); got != tc.cpu {
			t.Errorf("%d MB, chosen %d: CPU = %d, want %d", tc.ramMB, tc.chosen, got, tc.cpu)
		}
		if got := sched.AppCPUBookingMillicores(app); got != tc.booking {
			t.Errorf("%d MB, chosen %d: books %d m, want %d", tc.ramMB, tc.chosen, got, tc.booking)
		}
		if got := sched.AppCPUWeight(app); got != tc.weight {
			t.Errorf("%d MB: weight = %d, want %d", tc.ramMB, got, tc.weight)
		}
	}
}

// The weight and the uncapped flag reach vmmd's wake request.
func TestCPUSizeFieldsReachVMMD(t *testing.T) {
	var got fcvm.WakeRequest
	client := newClient(t, &fakeVMM{wakeFn: func(_ context.Context, req fcvm.WakeRequest) (*fcvm.Instance, error) {
		got = req
		return &fcvm.Instance{}, nil
	}})
	app := sched.AppSpec{BaseKey: "base.ext4", LayerKey: "app.ext4", VCPUCount: 1, MemSizeMiB: 512, CPUMillicores: 1000,
		CPUWeight: 32, CPUUncapped: true, AppID: "app-size", Plan: api.PlanHobby}
	if _, err := client.CreateColdBoot(t.Context(), "size-instance", app); err != nil {
		t.Fatalf("CreateColdBoot: %v", err)
	}
	if got.CPUWeight != 32 || !got.CPUUncapped {
		t.Fatalf("wake request weight %d, uncapped %v; want 32 and true", got.CPUWeight, got.CPUUncapped)
	}
}

// The fixture's apps are 256 MB: one vCPU. A snapshot whose last instance
// booked another count (one booted on its plan, before CPU followed size) is
// stale without asking vmmd; one whose instance booked one vCPU, or recorded
// no count, is asked about as usual.
func TestSnapshotRefreshPassFindsSnapshotsAtAnotherVCPUCount(t *testing.T) {
	f := newRefreshFixture(t)
	f.shadow(t)
	_, planShaped := f.appBooked(t, f.nodeA.ID, time.Hour, 2)
	_, sizeShaped := f.appBooked(t, f.nodeA.ID, 2*time.Hour, 1)
	_, unrecorded := f.appBooked(t, f.nodeA.ID, 3*time.Hour, 0)
	for _, key := range []string{planShaped, sizeShaped, unrecorded} {
		f.vmm.verdicts[key] = sched.SnapshotBackingCurrent
	}

	pass, err := f.refresher.Pass(f.ctx, f.nodeA.ID, "r1")
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if pass.Outcomes[sched.SnapshotRefreshWouldRefresh] != 1 || pass.Outcomes[sched.SnapshotRefreshCurrent] != 2 {
		t.Fatalf("pass outcomes = %v, want one would_refresh and two current", pass.Outcomes)
	}
	for _, key := range f.vmm.asked {
		if key == f.nodeA.ID+"|"+planShaped {
			t.Fatalf("vmmd was asked about the snapshot at another vCPU count: %v", f.vmm.asked)
		}
	}
	if len(f.vmm.asked) != 2 {
		t.Fatalf("vmmd asked %v, want the two snapshots at the app's count or none recorded", f.vmm.asked)
	}
}

// appBooked is app, with the parked instance having booked vcpus guest vCPUs
// (zero records none).
func (f *refreshFixture) appBooked(t *testing.T, node string, parkedAgo time.Duration, vcpus int) (state.App, string) {
	t.Helper()
	ctx := f.ctx
	f.ctx = state.WithNodeAdmission(ctx, state.NodeAdmission{NodeID: node, BookedVCPU: vcpus})
	defer func() { f.ctx = ctx }()
	return f.app(t, node, parkedAgo, state.Snapshot{CreatedAt: time.Now().UTC().Add(-parkedAgo)})
}
