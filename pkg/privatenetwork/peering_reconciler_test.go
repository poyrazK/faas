package privatenetwork

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type peeringApplyCall struct {
	accountID string
	region    string
	routes    []PeeringRoute
}

type recordingPeeringApplier struct {
	err   error
	calls []peeringApplyCall
}

func (a *recordingPeeringApplier) Apply(_ context.Context, accountID, region string, routes []PeeringRoute) error {
	a.calls = append(a.calls, peeringApplyCall{
		accountID: accountID,
		region:    region,
		routes:    append([]PeeringRoute(nil), routes...),
	})
	return a.err
}

func newPeeringReconcilerFixture(t *testing.T) (*state.MemStore, state.PrivateNetworkPeering, *recordingPeeringApplier) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	for _, network := range []state.PrivateNetwork{
		{ID: "left", AccountID: "acct-1", Name: "left", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")},
		{ID: "right", AccountID: "acct-1", Name: "right", Region: "fra1", CIDR: netip.MustParsePrefix("10.43.0.0/24")},
	} {
		if _, err := store.CreatePrivateNetwork(ctx, network); err != nil {
			t.Fatalf("CreatePrivateNetwork(%s): %v", network.ID, err)
		}
	}
	peering, err := store.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{
		ID: "peer-left-right", AccountID: "acct-1", LeftNetworkID: "left", RightNetworkID: "right", Region: "fra1",
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetworkPeering: %v", err)
	}
	return store, peering, &recordingPeeringApplier{}
}

func TestPeeringReconcilerPromotesPendingAfterCompleteRouteApply(t *testing.T) {
	ctx := context.Background()
	store, peering, applier := newPeeringReconcilerFixture(t)
	var observations []PeeringReconcileObservation
	reconciler, err := NewPeeringReconciler(store, applier, PeeringReconcilerOptions{
		Observe: func(observation PeeringReconcileObservation) { observations = append(observations, observation) },
	})
	if err != nil {
		t.Fatalf("NewPeeringReconciler: %v", err)
	}

	summary, err := reconciler.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if summary != (PeeringReconcileSummary{Discovered: 1, Peerings: 1, Routes: 2, Ready: 1}) {
		t.Fatalf("summary = %+v, want one converged peering", summary)
	}
	if len(applier.calls) != 1 {
		t.Fatalf("applier calls = %d, want 1", len(applier.calls))
	}
	call := applier.calls[0]
	if call.accountID != "acct-1" || call.region != "fra1" || len(call.routes) != 2 {
		t.Fatalf("apply call = %+v, want account/region and two routes", call)
	}
	if call.routes[0].FromNetworkID != "left" || call.routes[0].DestinationNetwork != "right" || call.routes[1].FromNetworkID != "right" || call.routes[1].DestinationNetwork != "left" {
		t.Fatalf("routes = %+v, want deterministic bidirectional routes", call.routes)
	}
	if got, err := store.GetPrivateNetworkPeering(ctx, peering.AccountID, peering.ID); err != nil || got.Status != api.PrivateNetworkPeeringStatusReady || got.StatusDetail != "private network routes active" {
		t.Fatalf("peering after sweep = %+v, %v", got, err)
	}
	if len(observations) != 1 || observations[0].Outcome != "ready" || observations[0].Routes != 2 {
		t.Fatalf("observations = %+v, want one ready group observation", observations)
	}
}

func TestPeeringReconcilerSweepAccountRegionWithdrawsDeletedPeering(t *testing.T) {
	ctx := context.Background()
	store, peering, applier := newPeeringReconcilerFixture(t)
	if _, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		ID: "third", AccountID: "acct-1", Name: "third", Region: "fra1", CIDR: netip.MustParsePrefix("10.44.0.0/24"),
	}); err != nil {
		t.Fatalf("CreatePrivateNetwork(third): %v", err)
	}
	if _, err := store.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{
		ID: "peer-right-third", AccountID: "acct-1", LeftNetworkID: "right", RightNetworkID: "third", Region: "fra1",
	}); err != nil {
		t.Fatalf("CreatePrivateNetworkPeering(second): %v", err)
	}
	reconciler, err := NewPeeringReconciler(store, applier, PeeringReconcilerOptions{})
	if err != nil {
		t.Fatalf("NewPeeringReconciler: %v", err)
	}
	if _, err := reconciler.Sweep(ctx); err != nil {
		t.Fatalf("initial Sweep: %v", err)
	}
	if err := store.DeletePrivateNetworkPeering(ctx, peering.AccountID, peering.ID); err != nil {
		t.Fatalf("DeletePrivateNetworkPeering: %v", err)
	}
	if _, err := reconciler.SweepAccountRegion(ctx, peering.AccountID, peering.Region); err != nil {
		t.Fatalf("SweepAccountRegion: %v", err)
	}
	if len(applier.calls) != 2 {
		t.Fatalf("applier calls = %d, want activation plus withdrawal", len(applier.calls))
	}
	got := applier.calls[1].routes
	if len(got) != 2 {
		t.Fatalf("withdrawal routes = %v, want remaining complete set", got)
	}
	for _, route := range got {
		if route.FromNetworkID == "left" || route.DestinationNetwork == "left" {
			t.Fatalf("withdrawal routes still contain deleted peering: %v", got)
		}
	}
}

func TestPeeringReconcilerFailsClosedOnApplierErrorAndRetries(t *testing.T) {
	ctx := context.Background()
	store, peering, applier := newPeeringReconcilerFixture(t)
	fabricErr := errors.New("fabric unavailable")
	applier.err = fabricErr
	reconciler, err := NewPeeringReconciler(store, applier, PeeringReconcilerOptions{})
	if err != nil {
		t.Fatalf("NewPeeringReconciler: %v", err)
	}

	first, err := reconciler.Sweep(ctx)
	if !errors.Is(err, fabricErr) || first.Failed != 1 {
		t.Fatalf("first sweep = %+v, %v; want fabric error and failed peering", first, err)
	}
	failed, err := store.GetPrivateNetworkPeering(ctx, peering.AccountID, peering.ID)
	if err != nil || failed.Status != api.PrivateNetworkPeeringStatusError || !strings.Contains(failed.StatusDetail, "fabric unavailable") {
		t.Fatalf("failed peering = %+v, %v", failed, err)
	}

	applier.err = nil
	second, err := reconciler.Sweep(ctx)
	if err != nil || second.Ready != 1 {
		t.Fatalf("second sweep = %+v, %v; want retry to converge", second, err)
	}
	ready, err := store.GetPrivateNetworkPeering(ctx, peering.AccountID, peering.ID)
	if err != nil || ready.Status != api.PrivateNetworkPeeringStatusReady {
		t.Fatalf("retried peering = %+v, %v", ready, err)
	}
}

func TestPeeringReconcilerRejectsUnreadyNetwork(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	for _, network := range []state.PrivateNetwork{
		{ID: "left", AccountID: "acct-1", Name: "left", Region: "fra1", CIDR: netip.MustParsePrefix("10.44.0.0/24"), Status: api.PrivateNetworkStatusError, StatusDetail: "bridge failed"},
		{ID: "right", AccountID: "acct-1", Name: "right", Region: "fra1", CIDR: netip.MustParsePrefix("10.45.0.0/24")},
	} {
		if _, err := store.CreatePrivateNetwork(ctx, network); err != nil {
			t.Fatalf("CreatePrivateNetwork(%s): %v", network.ID, err)
		}
	}
	peering, err := store.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{
		ID: "peer-unready", AccountID: "acct-1", LeftNetworkID: "left", RightNetworkID: "right", Region: "fra1",
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetworkPeering: %v", err)
	}
	applier := &recordingPeeringApplier{}
	reconciler, err := NewPeeringReconciler(store, applier, PeeringReconcilerOptions{})
	if err != nil {
		t.Fatalf("NewPeeringReconciler: %v", err)
	}

	summary, err := reconciler.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if summary.Failed != 1 || len(applier.calls) != 1 || len(applier.calls[0].routes) != 0 {
		t.Fatalf("summary = %+v, calls = %+v; want blocked peering to withdraw routes", summary, applier.calls)
	}
	got, err := store.GetPrivateNetworkPeering(ctx, peering.AccountID, peering.ID)
	if err != nil || got.Status != api.PrivateNetworkPeeringStatusError {
		t.Fatalf("unready peering = %+v, %v", got, err)
	}
}

func TestNewPeeringReconcilerValidatesOptions(t *testing.T) {
	store := state.NewMemStore()
	applier := &recordingPeeringApplier{}
	for name, options := range map[string]PeeringReconcilerOptions{
		"interval too short": {Interval: 500 * time.Millisecond},
		"batch too small":    {BatchSize: -1},
		"batch too large":    {BatchSize: 1001},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewPeeringReconciler(store, applier, options); err == nil {
				t.Fatal("NewPeeringReconciler succeeded, want invalid options error")
			}
		})
	}
}
