package privatenetwork

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReconciliationDeletedPeeringCannotBeResurrected(t *testing.T) {
	store, peer, _ := newPeeringReconcilerFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var mu sync.Mutex
	var installed []PeeringRoute
	calls := 0
	applier := FuncPeeringRouteApplier(func(ctx context.Context, _, _ string, routes []PeeringRoute) error {
		if len(routes) > 0 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		mu.Lock()
		installed = append([]PeeringRoute(nil), routes...)
		calls++
		mu.Unlock()
		return nil
	})
	reconciler, err := NewPeeringReconciler(store, applier, PeeringReconcilerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := reconciler.Sweep(t.Context()); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old sweep did not reach apply")
	}

	blockedCtx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	err = store.DeletePrivateNetworkPeering(blockedCtx, peer.AccountID, peer.ID)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("delete passed active reconciliation: %v", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePrivateNetworkPeering(t.Context(), peer.AccountID, peer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.SweepAccountRegion(t.Context(), peer.AccountID, peer.Region); err != nil {
		t.Fatal(err)
	}
	// The final row is absent, so a future periodic sweep cannot repair this group.
	if _, err := reconciler.Sweep(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(installed) != 0 {
		t.Fatalf("deleted final peering restored %d routes after successful withdrawal; apply calls=%d, later sweep found no group", len(installed), calls)
	}
}

type audit5Connector struct{ entered, release chan struct{} }

func (c audit5Connector) Check(ctx context.Context, a state.AppPrivateNetworkAttachment) (CheckResult, error) {
	if a.NetworkID == "old-network" {
		close(c.entered)
		select {
		case <-c.release:
		case <-ctx.Done():
			return CheckResult{}, ctx.Err()
		}
		return CheckResult{Ready: true}, nil
	}
	return CheckResult{Ready: false}, nil
}

func TestReconciliationOldAttachmentCheckCannotApproveReplacement(t *testing.T) {
	store := state.NewMemStore()
	old := state.AppPrivateNetworkAttachment{AccountID: "acct-audit5", AppID: "app-audit5", NetworkID: "old-network", Region: "fra1", Status: api.PrivateNetworkAttachmentStatusPending, CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}}
	if _, err := store.UpsertAppPrivateNetworkAttachment(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var applied []netip.Prefix
	r, err := NewReconciler(store, audit5Connector{entered, release}, FuncRouteApplier(func(_ context.Context, _ string, cidrs []netip.Prefix) error {
		applied = append([]netip.Prefix(nil), cidrs...)
		return nil
	}), ReconcilerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.Sweep(t.Context()); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("check did not start")
	}
	replacement := old
	replacement.NetworkID = "new-network"
	replacement.CIDRs = []netip.Prefix{netip.MustParsePrefix("10.43.0.0/24")}

	blockedCtx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err = store.UpsertAppPrivateNetworkAttachment(blockedCtx, replacement)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("replacement passed active check: %v", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAppPrivateNetworkAttachment(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetAppPrivateNetworkAttachment(t.Context(), old.AccountID, old.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == api.PrivateNetworkAttachmentStatusReady {
		t.Fatalf("replacement %q marked ready without its provider check; installed routes=%v, stored intent=%v", got.NetworkID, applied, got.CIDRs)
	}
}
