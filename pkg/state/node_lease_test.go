// adr: 1344
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

type nodeLeaseTestStore interface {
	state.Store
	state.NodeLeaseStore
}

func leaseNode(t *testing.T, store state.Store) state.ComputeNode {
	t.Helper()
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "lease-" + uuid.NewString(), Lifecycle: state.NodeLifecycleActive,
		TargetURL: "unix:///run/faas/vmmd.sock", VPCPUs: 4, VCPUBudget: 160,
		MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

// runNodeLeaseTests is the contract both stores meet (ADR-1344 §4).
func runNodeLeaseTests(t *testing.T, open func(*testing.T) nodeLeaseTestStore) {
	t.Run("renew keeps the epoch for the same holder", func(t *testing.T) {
		store := open(t)
		node := leaseNode(t, store)
		first, err := store.RenewNodeLease(t.Context(), node.ID, "host-a/boot-1", 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if first.Epoch != 1 || first.Holder != "host-a/boot-1" || !first.ExpiresAt.After(first.RenewedAt) {
			t.Fatalf("first lease = %+v", first)
		}
		again, err := store.RenewNodeLease(t.Context(), node.ID, "host-a/boot-1", 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if again.Epoch != 1 || again.ExpiresAt.Before(first.ExpiresAt) {
			t.Fatalf("renewal = %+v after %+v", again, first)
		}
		read, err := store.GetNodeLease(t.Context(), node.ID)
		if err != nil {
			t.Fatal(err)
		}
		if read.Epoch != 1 || read.ExpiredFor(0) {
			t.Fatalf("read lease = %+v, want epoch 1 and live", read)
		}
	})

	t.Run("another holder waits for expiry, then takes over with the next epoch", func(t *testing.T) {
		store := open(t)
		node := leaseNode(t, store)
		if _, err := store.RenewNodeLease(t.Context(), node.ID, "host-a/boot-1", time.Second); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RenewNodeLease(t.Context(), node.ID, "host-a/boot-2", time.Second); !errors.Is(err, state.ErrNodeLeaseHeld) {
			t.Fatalf("takeover of a live lease err = %v, want ErrNodeLeaseHeld", err)
		}
		advanceLeaseClock(t, store, 1100*time.Millisecond)
		read, err := store.GetNodeLease(t.Context(), node.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !read.ExpiredFor(0) {
			t.Fatalf("lease %+v not expired after its ttl", read)
		}
		taken, err := store.RenewNodeLease(t.Context(), node.ID, "host-a/boot-2", time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if taken.Epoch != 2 || taken.Holder != "host-a/boot-2" {
			t.Fatalf("takeover = %+v, want epoch 2 held by boot-2", taken)
		}
	})

	t.Run("a node without a lease is unknown, not expired", func(t *testing.T) {
		store := open(t)
		node := leaseNode(t, store)
		if _, err := store.GetNodeLease(t.Context(), node.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("GetNodeLease on a node without a lease err = %v, want ErrNotFound", err)
		}
		if _, err := store.RenewNodeLease(t.Context(), uuid.NewString(), "h", time.Second); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("renewing an unknown node err = %v, want ErrNotFound", err)
		}
	})

	t.Run("invalid renewals are refused", func(t *testing.T) {
		store := open(t)
		node := leaseNode(t, store)
		for _, tc := range []struct {
			id, holder string
			ttl        time.Duration
		}{
			{"not-a-uuid", "h", time.Second},
			{node.ID, " ", time.Second},
			{node.ID, "h", 0},
			{node.ID, "h", state.MaxNodeLeaseTTL + time.Second},
		} {
			if _, err := store.RenewNodeLease(t.Context(), tc.id, tc.holder, tc.ttl); err == nil {
				t.Errorf("RenewNodeLease(%q, %q, %s) accepted", tc.id, tc.holder, tc.ttl)
			}
		}
	})
}

// advanceLeaseClock moves the memory store's clock, or waits on PostgreSQL,
// whose clock cannot be moved.
func advanceLeaseClock(t *testing.T, store nodeLeaseTestStore, d time.Duration) {
	t.Helper()
	if mem, ok := store.(*state.MemStore); ok {
		now := time.Now().UTC().Add(d)
		mem.SetClockForTest(func() time.Time { return now })
		return
	}
	time.Sleep(d)
}

func TestNodeLeaseMemory(t *testing.T) {
	runNodeLeaseTests(t, func(*testing.T) nodeLeaseTestStore { return state.NewMemStore() })
}
