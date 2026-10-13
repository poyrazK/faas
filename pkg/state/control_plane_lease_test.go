// adr: 1237
// control_plane_lease_test.go pins the control-plane singleton lease
// contract (ADR-1237 §4) on both stores: one holder at a time, takeover only
// after expiry by the database clock, and a new epoch on every takeover.

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMem_ControlPlaneLeaseContract(t *testing.T) {
	runControlPlaneLeaseContract(t, context.Background(), state.NewMemStore())
}

func TestPg_ControlPlaneLeaseContract(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	runControlPlaneLeaseContract(t, ctx, s)
}

// runControlPlaneLeaseContract uses a one-second TTL and waits it out, so the
// expiry is decided by each store's own clock.
func runControlPlaneLeaseContract(t *testing.T, ctx context.Context, s state.ControlPlaneLeaseStore) {
	t.Helper()
	const name = "central-scheduler"
	const ttl = time.Second
	acquire := func(holder string) (state.ControlPlaneLease, bool) {
		t.Helper()
		lease, held, err := s.AcquireControlPlaneLease(ctx, name, holder, ttl)
		if err != nil {
			t.Fatalf("acquire by %s: %v", holder, err)
		}
		return lease, held
	}
	heldAt := func(epoch int64) bool {
		t.Helper()
		held, err := s.ControlPlaneLeaseHeld(ctx, name, epoch)
		if err != nil {
			t.Fatalf("held(%d): %v", epoch, err)
		}
		return held
	}
	expire := func() { time.Sleep(ttl + 200*time.Millisecond) }

	a1, held := acquire("node-a")
	if !held || a1.Epoch <= 0 || !a1.ExpiresAt.After(a1.CheckedAt) {
		t.Fatalf("first acquire = %+v held=%v", a1, held)
	}
	if _, held := acquire("node-b"); held {
		t.Fatal("second holder acquired a live lease")
	}
	a2, held := acquire("node-a")
	if !held || a2.Epoch != a1.Epoch || !a2.AcquiredAt.Equal(a1.AcquiredAt) {
		t.Fatalf("renewal = %+v held=%v, want same epoch %d", a2, held, a1.Epoch)
	}
	if !heldAt(a1.Epoch) || heldAt(a1.Epoch+1000) {
		t.Fatal("fencing check disagrees with the live epoch")
	}

	expire()
	b1, held := acquire("node-b")
	if !held || b1.Epoch <= a1.Epoch {
		t.Fatalf("takeover after expiry = %+v held=%v, want an epoch above %d", b1, held, a1.Epoch)
	}
	if heldAt(a1.Epoch) {
		t.Fatal("the previous holder's epoch still passes the fencing check")
	}
	if _, held := acquire("node-a"); held {
		t.Fatal("the previous holder renewed a lease another node took over")
	}

	if err := s.ReleaseControlPlaneLease(ctx, name, "node-b", b1.Epoch+1); err != nil {
		t.Fatal(err)
	}
	if !heldAt(b1.Epoch) {
		t.Fatal("release with a stale epoch dropped the live lease")
	}
	if err := s.ReleaseControlPlaneLease(ctx, name, "node-b", b1.Epoch); err != nil {
		t.Fatal(err)
	}
	a3, held := acquire("node-a")
	if !held || a3.Epoch <= b1.Epoch {
		t.Fatalf("acquire after release = %+v held=%v, want an epoch above %d", a3, held, b1.Epoch)
	}

	expire()
	a4, held := acquire("node-a")
	if !held || a4.Epoch <= a3.Epoch {
		t.Fatalf("re-acquire of own expired lease = %+v held=%v, want a new epoch above %d", a4, held, a3.Epoch)
	}

	if _, _, err := s.AcquireControlPlaneLease(ctx, name, "node-a", 1500*time.Millisecond); err == nil {
		t.Fatal("fractional-second TTL accepted")
	}
	if _, _, err := s.AcquireControlPlaneLease(ctx, "", "node-a", ttl); err == nil {
		t.Fatal("empty lease name accepted")
	}
}
