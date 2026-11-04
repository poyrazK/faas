// adr: 191 — explicit app wakes run on the loop's work pool, so a boot no
// longer holds Loop.Run's select goroutine, and only the app's owner serves
// them.
package sched_test

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func explicitWake(appID, wakeID string) db.Notification {
	return db.Notification{Channel: db.NotifyAppWake, Payload: `{"app_id":"` + appID + `","wake_id":"` + wakeID + `"}`}
}

// The select goroutine hands the wake off and returns while vmmd is still
// booting it; the wake still completes with the requested correlation.
func TestExplicitAppWakeLeavesTheLoopGoroutine(t *testing.T) {
	const wakeID = "0198f89a-0000-7000-8000-000000000011"
	release, booting := make(chan struct{}), make(chan struct{}, 1)
	f := newFailoverFleet(t, 1, 4096, func(string) error {
		select {
		case booting <- struct{}{}:
		default:
		}
		<-release
		return nil
	})
	app := f.app(t, api.PlanPro, 256, 1)
	loop := sched.NewLoop(nil, f.e, slog.New(slog.DiscardHandler))
	releaseBoot := sync.OnceFunc(func() { close(release) })
	defer loop.WaitPrimes()
	defer releaseBoot()

	returned := make(chan struct{})
	go func() {
		loop.XDispatchExplicitWake(f.ctx, explicitWake(app.ID, wakeID))
		close(returned)
	}()
	select {
	case <-booting:
	case <-time.After(30 * time.Second):
		t.Fatal("the explicit wake never reached vmmd")
	}
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch waited for the boot: the select goroutine would stall for the whole wake")
	}
	releaseBoot()
	loop.WaitPrimes()
	ins, err := f.store.RunningInstanceForApp(f.ctx, app.ID)
	if err != nil {
		t.Fatalf("running instance after the explicit wake: %v", err)
	}
	if ins.WakeID != wakeID {
		t.Fatalf("instance wake id = %q, want %s", ins.WakeID, wakeID)
	}
}

// A peer schedd hears every explicit wake; it leaves another node's app
// alone instead of booting it or reporting a failure.
func TestExplicitAppWakeLeavesAnotherNodesApp(t *testing.T) {
	f := newFailoverFleet(t, 2, 4096, func(string) error { return nil })
	app := f.app(t, api.PlanPro, 256, 1, func(a *state.App) { a.NodeID = f.nodes[1].ID })
	loop := sched.NewLoop(nil, f.e, slog.New(slog.DiscardHandler))

	loop.XDispatchExplicitWake(f.ctx, explicitWake(app.ID, "0198f89a-0000-7000-8000-000000000012"))
	loop.WaitPrimes()

	rows, err := f.store.ListInstancesForApp(f.ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 || f.vmm.totalCalls() != 0 {
		t.Fatalf("peer schedd admitted %d instances and called vmmd %d times for another node's app", len(rows), f.vmm.totalCalls())
	}
}
