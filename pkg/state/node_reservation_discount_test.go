// adr: 20261107122928 — the durable node guard leaves the scheduler's idle
// ladder discount (instances vmmd holds at the ladder's freeze level) out of
// a node's reservations, only with a measured allowance, and at most twice
// the ceiling, so it admits what the scheduler's ledger admits.
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkReservationDiscount(t *testing.T, s state.Store) {
	t.Helper()
	const ceilingMB, ramMB = 1024, 248
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "discount-" + uuid.NewString(), Lifecycle: state.NodeLifecycleActive,
		TargetURL: "unix:///run/faas/vmmd.sock", VPCPUs: 4, VCPUBudget: 160,
		MemMB: 8192, AdmissionCeilingMB: ceilingMB, MaxConcurrency: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, dep := completionApplication(t, s)
	bound := func(percent, discountMB int) context.Context {
		return state.WithNodeAdmission(t.Context(), state.NodeAdmission{
			NodeID: node.ID, Generation: node.LifecycleGeneration,
			ReservationPercent: percent, ReservationDiscountMB: discountMB,
		})
	}
	admit := func(ctx context.Context) error {
		t.Helper()
		_, err := s.CreateInstanceWithMode(ctx, app, dep, string(state.StateWaking), ramMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
		if err != nil && !errors.Is(err, state.ErrNodeCapacity) {
			t.Fatal(err)
		}
		return err
	}
	used := func() int64 {
		t.Helper()
		mb, err := s.ComputeNodeUsedMB(t.Context(), node.ID)
		if err != nil {
			t.Fatal(err)
		}
		return mb
	}
	full := api.MeasuredAdmissionMaxOvercommitPercent
	for admit(bound(full, 0)) == nil {
	}
	if got := used(); got != 2*ceilingMB {
		t.Fatalf("measured allowance holds %d MB, want %d", got, 2*ceilingMB)
	}
	// 512 MB of idle-ladder reservations left out: two more 256 MB VMs fit.
	for i := range 2 {
		if err := admit(bound(full, 512)); err != nil {
			t.Fatalf("admission %d with a 512 MB discount = %v, want admitted", i+1, err)
		}
	}
	if err := admit(bound(full, 512)); !errors.Is(err, state.ErrNodeCapacity) {
		t.Fatalf("third admission with a 512 MB discount = %v, want ErrNodeCapacity", err)
	}
	// Without a measured allowance the discount does nothing.
	if err := admit(bound(100, 4096)); !errors.Is(err, state.ErrNodeCapacity) {
		t.Fatalf("discount at a 100%% allowance = %v, want ErrNodeCapacity", err)
	}
	// An oversized discount is bounded at twice the ceiling: reservations reach
	// at most 400% of it.
	for admit(bound(full, 1_000_000)) == nil {
	}
	if got, want := used(), int64(ceilingMB*(full+api.IdleLadderReservationDiscountMaxPercent)/100); got != want {
		t.Fatalf("bounded discount holds %d MB, want %d", got, want)
	}
}

func TestNodeReservationDiscountMemory(t *testing.T) {
	checkReservationDiscount(t, state.NewMemStore())
}

// Both admission paths take the discount the same way.
func TestPgAdmissionReservationDiscount(t *testing.T) {
	store, _ := lifecycleCompletionPGStore(t)
	if !state.AdmissionFunctionsUsable(t.Context(), store) {
		t.Fatal("migrated database lacks the admission functions")
	}
	t.Run("function", func(t *testing.T) { checkReservationDiscount(t, store) })
	t.Run("statements", func(t *testing.T) { checkReservationDiscount(t, state.StatementAdmissionStore(store)) })
}
