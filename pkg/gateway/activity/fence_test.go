// adr: 611
package activity

import (
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestFenceClosesLateAdmissionAndRetainsExistingForwards(t *testing.T) {
	tracker, err := NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	app, old, candidate, binding := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	finish, ok := tracker.TryBegin(app, old)
	if !ok {
		t.Fatal("initial forward rejected")
	}
	f, err := tracker.InstallFence(app, old, a, binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok = tracker.TryBegin(app, old); ok {
		t.Fatal("late predecessor dispatch entered")
	}
	finishCandidate, ok := tracker.TryBegin(app, candidate)
	if !ok {
		t.Fatal("candidate dispatch blocked")
	}
	_, busy, closed := tracker.ObserveFence(app)
	if !closed || !busy.CoverageKnown || busy.ActiveForwards != 1 {
		t.Fatalf("existing forward disappeared: %+v", busy)
	}
	finish()
	finishCandidate()
	replay, err := tracker.InstallFence(app, old, a, binding)
	if err != nil || replay != f {
		t.Fatal("retry replaced fence", replay, err)
	}
	tracker.ReconcileRouting(app, a)
	tracker.ReconcileRouting(app, strings.Repeat("z", 64))
	if _, ok = tracker.TryBegin(app, old); ok {
		t.Fatal("same or invalid revision reopened admission")
	}
	tracker.ReconcileRouting(app, b)
	finish, ok = tracker.TryBegin(app, old)
	if !ok {
		t.Fatal("authoritative rollback did not reopen")
	}
	reactivated, err := tracker.InstallFence(app, old, c, binding)
	if err != nil || reactivated.ID == f.ID {
		t.Fatal("reactivation reused old fence", err)
	}
	_, busy, _ = tracker.ObserveFence(app)
	if busy.ActiveForwards != 1 {
		t.Fatal("reactivation lost rollback request")
	}
	finish()
	_, zero, _ := tracker.ObserveFence(app)
	if !zero.CoverageKnown || zero.ActiveForwards != 0 || zero.ActivityVersion <= busy.ActivityVersion {
		t.Fatal(zero)
	}
}

func TestFencePrivateAdmissionRejectsUntrackableIdentity(t *testing.T) {
	tracker, err := NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tracker.TryBegin("", uuid.NewString()); ok {
		t.Fatal("unidentified forward admitted")
	}
	if got := tracker.Observe(uuid.NewString(), uuid.NewString()); got.CoverageKnown || got.TotalActiveForwards != 0 || got.CoverageUnknownReason != MissingIdentity {
		t.Fatal(got)
	}
	disabled := newTracker(t)
	if _, err := disabled.InstallFence(uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64), "binding"); err == nil {
		t.Fatal("ordinary tracking acquired admission authority")
	}
}

func TestFenceCapacityAndRepairEnumerationAreBounded(t *testing.T) {
	tracker, err := NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	for range api.RuntimeUpgradeActivityFenceLimit {
		if _, err := tracker.InstallFence(uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64), "binding"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tracker.InstallFence(uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64), "binding"); err == nil {
		t.Fatal("unbounded fence registry")
	}
	apps := tracker.FenceApps()
	first := apps[0]
	apps[0] = "changed"
	if tracker.FenceApps()[0] != first || len(tracker.FenceApps()) != api.RuntimeUpgradeActivityFenceLimit {
		t.Fatal("enumeration mutated fences")
	}
	tracker.ReconcileRouting(first, strings.Repeat("b", 64))
	if _, err := tracker.InstallFence(uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64), "binding"); err != nil {
		t.Fatal("released slot not reusable", err)
	}
}

func TestFenceConcurrentAdmissionAndReconciliation(t *testing.T) {
	tracker, err := NewWithFences(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	app, old, binding := uuid.NewString(), uuid.NewString(), uuid.NewString()
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	var wg sync.WaitGroup
	for range 128 {
		wg.Go(func() {
			for range 16 {
				if finish, ok := tracker.TryBegin(app, old); ok {
					finish()
					finish()
				}
				_, _ = tracker.InstallFence(app, old, a, binding)
				tracker.ReconcileRouting(app, b)
			}
		})
	}
	wg.Wait()
	if got := tracker.Observe(app, old); !got.CoverageKnown || got.TotalActiveForwards != 0 {
		t.Fatal(got)
	}
}
