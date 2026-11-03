// adr: 20261102160622 — an app's serialisation lock leaves the engine once
// nobody holds or awaits it, and still excludes while anyone does.
package sched_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppLocksAreReleasedWhenNobodyHoldsThem(t *testing.T) {
	e := sched.XNewEngine(t, state.NewMemStore(), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	for _, app := range []string{"a", "b", "c"} {
		e.LockApp(app)()
	}
	if got := e.AppLocks(); got != 0 {
		t.Fatalf("app locks after every unlock = %d, want 0", got)
	}

	unlock := e.LockApp("a")
	if _, ok := e.TryLockApp("a"); ok {
		t.Fatal("TryLockApp took a held lock")
	}
	if got := e.AppLocks(); got != 1 {
		t.Fatalf("app locks while one is held = %d, want 1", got)
	}
	unlock()
	again, ok := e.TryLockApp("a")
	if !ok {
		t.Fatal("TryLockApp refused a free lock")
	}
	again()
	if got := e.AppLocks(); got != 0 {
		t.Fatalf("app locks after TryLockApp = %d, want 0", got)
	}
}

// Holders that come and go while others wait never share the lock: the entry
// is dropped only when its last waiter has unlocked.
func TestAppLockExcludesWhileItsEntryComesAndGoes(t *testing.T) {
	e := sched.XNewEngine(t, state.NewMemStore(), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	var inside, worst atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				unlock := e.LockApp("app")
				if n := inside.Add(1); n > worst.Load() {
					worst.Store(n)
				}
				inside.Add(-1)
				unlock()
			}
		}()
	}
	wg.Wait()
	if got := worst.Load(); got != 1 {
		t.Fatalf("%d holders were inside one app's lock at once", got)
	}
	if got := e.AppLocks(); got != 0 {
		t.Fatalf("app locks after the holders left = %d, want 0", got)
	}
}
