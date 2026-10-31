// adr: 20261024090000 — a reaper park re-checks its idle decision against
// fresh activity before the instance leaves RUNNING.

package sched_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// wakeIdleInstance wakes one instance of a fresh Pro app and backdates its
// last request by ten minutes, past the app's 60 s idle timeout.
func wakeIdleInstance(t *testing.T) (*state.MemStore, *sched.Engine, *sched.FakeVMM, state.Instance) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := sched.SeedApp(t, store, api.PlanPro, 128, 1)
	vmm := &sched.FakeVMM{}
	e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	res, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	idleSince := time.Now().Add(-10 * time.Minute)
	if _, err := store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{{InstanceID: res.InstanceID, LastRequest: idleSince}}); err != nil {
		t.Fatal(err)
	}
	row, err := store.InstanceByID(ctx, res.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	return store, e, vmm, row
}

func TestParkIdleRechecksFreshActivity(t *testing.T) {
	for _, tc := range []struct {
		name string
		// newRequest lands a request after the reaper's tick.
		newRequest bool
		guest      *sched.GuestActivity
		allowNew   bool
		wantParked bool
	}{
		{name: "still idle parks", wantParked: true},
		{name: "a request since the tick keeps it", newRequest: true},
		{name: "a forward in flight keeps it", guest: &sched.GuestActivity{Inflight: 1}},
		{name: "an open connection keeps it", guest: &sched.GuestActivity{OpenConns: 1}},
		{name: "a newer request vmmd saw keeps it", guest: &sched.GuestActivity{LastRequest: time.Now()}},
		{name: "quiet guest activity parks", guest: &sched.GuestActivity{}, wantParked: true},
		{name: "scale-in parks an instance still taking traffic", newRequest: true, allowNew: true, wantParked: true},
		{name: "scale-in keeps a forward in flight", allowNew: true, guest: &sched.GuestActivity{Inflight: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, e, vmm, row := wakeIdleInstance(t)
			check := sched.IdleParkCheck{LastRequest: row.LastRequestAt, RequestCount: row.RequestCount, AllowNewRequests: tc.allowNew}
			if tc.guest != nil {
				guest := *tc.guest
				check.Guest = func(context.Context) (sched.GuestActivity, bool) { return guest, true }
			}
			if tc.newRequest {
				if _, err := store.TouchInstancesLastSeen(ctx, []state.InstanceTouch{{InstanceID: row.ID, LastRequest: time.Now(), RequestDelta: 1}}); err != nil {
					t.Fatal(err)
				}
			}

			err := e.ParkIdle(ctx, row.ID, check)
			after, readErr := store.InstanceByID(ctx, row.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.wantParked {
				if err != nil || after.State != string(state.StateParked) || *vmm.Snapshots() != 1 {
					t.Fatalf("ParkIdle = %v, state %q, snapshots %d; want parked with one snapshot", err, after.State, *vmm.Snapshots())
				}
				return
			}
			if !errors.Is(err, sched.ErrParkActivity) {
				t.Fatalf("ParkIdle = %v, want ErrParkActivity", err)
			}
			if after.State != string(state.StateRunning) || *vmm.Snapshots() != 0 {
				t.Fatalf("state %q, snapshots %d; want the instance left running and untouched", after.State, *vmm.Snapshots())
			}
		})
	}
}

// changingActivity is vmmd activity that changes after the reaper's tick
// snapshot: the first read shows the instance quiet, later ones show after.
type changingActivity struct {
	mu       sync.Mutex
	reads    int
	instance string
	before   sched.InstanceActivity
	after    sched.InstanceActivity
}

func (a *changingActivity) SnapshotActivity(time.Time) map[string]sched.InstanceActivity {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reads++
	if a.reads == 1 {
		return map[string]sched.InstanceActivity{a.instance: a.before}
	}
	return map[string]sched.InstanceActivity{a.instance: a.after}
}

// The reaper's tick snapshot can be minutes old when a park runs. A request
// that reached the guest after the tick keeps the instance running.
func TestRunReaperRechecksActivityBeforeParking(t *testing.T) {
	for _, tc := range []struct {
		name       string
		after      sched.InstanceActivity
		wantParked bool
	}{
		{name: "still quiet at park time", wantParked: true},
		{name: "a request reached the guest after the tick", after: sched.InstanceActivity{Inflight: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, e, vmm, row := wakeIdleInstance(t)
			activity := &changingActivity{instance: row.ID, before: sched.InstanceActivity{LastRequest: row.LastRequestAt}, after: tc.after}
			if tc.after.LastRequest.IsZero() {
				activity.after.LastRequest = row.LastRequestAt
			}
			sched.NewLoop(nil, e, sched.TestLog()).
				WithInstanceActivity(activity).
				RunReaper(context.Background())

			after, err := store.InstanceByID(context.Background(), row.ID)
			if err != nil {
				t.Fatal(err)
			}
			parked := after.State == string(state.StateParked)
			if parked != tc.wantParked || (*vmm.Snapshots() == 1) != tc.wantParked {
				t.Fatalf("state %q, snapshots %d; want parked=%v", after.State, *vmm.Snapshots(), tc.wantParked)
			}
			if activity.reads < 2 {
				t.Fatalf("activity read %d times, want the tick read plus a recheck at park time", activity.reads)
			}
		})
	}
}
