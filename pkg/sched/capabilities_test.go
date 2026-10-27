// adr: 20261023115741 — schedd holds a capability's background work while
// its platform flag is off for the owning account.
package sched_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDrainHoldsWorkWhileItsCapabilityIsOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanHobby, true)
	// queues is a preview capability: with no operator value it is off.
	capabilities, err := capflags.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	(*d.Engine()).WithCapabilities(capabilities)
	inv := sched.SeedDrainInvocation(t, store, state.InvocationQueue)

	d.Tick(ctx)

	if got := ds.Calls().Load(); got != 0 {
		t.Fatalf("synth calls = %d, want 0 while queues is off", got)
	}
	got, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.InvocationPending || !got.DueAt.After(time.Now().Add(30*time.Second)) {
		t.Fatalf("invocation = %s due %s, want pending and deferred", got.State, got.DueAt)
	}
	if !strings.Contains(got.LastError, "queues") || got.Attempts != inv.Attempts {
		t.Fatalf("last_error = %q attempts = %d, want the capability named and no attempt spent", got.LastError, got.Attempts)
	}
}

func TestDrainDeliversWhenTheCapabilityIsReleased(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanHobby, true)
	key, _ := platformflags.CapabilityFlag("queues")
	set := platformflags.NewSet(map[string]platformflags.Definition{key: {Enabled: true, Default: true}})
	capabilities, err := capflags.Load(set.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	(*d.Engine()).WithCapabilities(capabilities)
	sched.SeedDrainInvocation(t, store, state.InvocationQueue)

	d.Tick(ctx)

	if got := ds.Calls().Load(); got == 0 {
		t.Fatal("a released capability's queued work must be delivered")
	}
}
