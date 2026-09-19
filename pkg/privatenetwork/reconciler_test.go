package privatenetwork

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReconcilerTransitionsReadyOnlyAfterRouteActivation(t *testing.T) {
	store := state.NewMemStore()
	attachment, err := store.UpsertAppPrivateNetworkAttachment(context.Background(), state.AppPrivateNetworkAttachment{
		AccountID: "acct", AppID: "app", NetworkID: "vpc-1", Region: "nyc3",
		CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")},
		Status: api.PrivateNetworkAttachmentStatusPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	connector, err := NewConfiguredConnector([]ConfiguredNetwork{{ID: "vpc-1", Region: "nyc3", Ready: true}})
	if err != nil {
		t.Fatal(err)
	}
	var applied []netip.Prefix
	reconciler, err := NewReconciler(store, connector, FuncRouteApplier(func(_ context.Context, appID string, cidrs []netip.Prefix) error {
		if appID != attachment.AppID {
			t.Fatalf("applier appID = %q, want %q", appID, attachment.AppID)
		}
		applied = append([]netip.Prefix(nil), cidrs...)
		return nil
	}), ReconcilerOptions{Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := reconciler.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Ready != 1 || len(applied) != 1 {
		t.Fatalf("summary=%+v applied=%v", summary, applied)
	}
	got, err := store.GetAppPrivateNetworkAttachment(context.Background(), "acct", "app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != api.PrivateNetworkAttachmentStatusReady || got.StatusDetail != "private network route active" {
		t.Fatalf("attachment after sweep = %+v", got)
	}
}

func TestReconcilerSweepNetworkTargetsOnlyAffectedAttachments(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	for _, attachment := range []state.AppPrivateNetworkAttachment{
		{AccountID: "acct-1", AppID: "app-target", NetworkID: "vpc-1", Region: "nyc3", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}, Status: api.PrivateNetworkAttachmentStatusReady},
		{AccountID: "acct-1", AppID: "app-other-network", NetworkID: "vpc-2", Region: "nyc3", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.43.0.0/16")}, Status: api.PrivateNetworkAttachmentStatusReady},
		{AccountID: "acct-2", AppID: "app-other-account", NetworkID: "vpc-1", Region: "nyc3", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.44.0.0/16")}, Status: api.PrivateNetworkAttachmentStatusReady},
	} {
		if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, attachment); err != nil {
			t.Fatalf("UpsertAppPrivateNetworkAttachment(%s): %v", attachment.AppID, err)
		}
	}
	connector, err := NewConfiguredConnector([]ConfiguredNetwork{
		{ID: "vpc-1", Region: "nyc3", Ready: true},
		{ID: "vpc-2", Region: "nyc3", Ready: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var applied []string
	reconciler, err := NewReconciler(store, connector, FuncRouteApplier(func(_ context.Context, appID string, _ []netip.Prefix) error {
		applied = append(applied, appID)
		return nil
	}), ReconcilerOptions{Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := reconciler.SweepNetwork(ctx, "acct-1", "vpc-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Discovered != 1 || len(applied) != 1 || applied[0] != "app-target" {
		t.Fatalf("summary=%+v applied=%v, want only app-target", summary, applied)
	}
}

func TestReconcilerKeepsUnreadyAttachmentPending(t *testing.T) {
	store := state.NewMemStore()
	_, err := store.UpsertAppPrivateNetworkAttachment(context.Background(), state.AppPrivateNetworkAttachment{
		AccountID: "acct", AppID: "app", NetworkID: "vpc-1", Region: "nyc3",
		CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")},
		Status: api.PrivateNetworkAttachmentStatusPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	connector, err := NewConfiguredConnector([]ConfiguredNetwork{{ID: "vpc-1", Region: "nyc3", Detail: "VPC is still attaching"}})
	if err != nil {
		t.Fatal(err)
	}
	applied := false
	reconciler, err := NewReconciler(store, connector, FuncRouteApplier(func(context.Context, string, []netip.Prefix) error {
		applied = true
		return nil
	}), ReconcilerOptions{Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := reconciler.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Pending != 1 || applied {
		t.Fatalf("summary=%+v applied=%v", summary, applied)
	}
	got, err := store.GetAppPrivateNetworkAttachment(context.Background(), "acct", "app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != api.PrivateNetworkAttachmentStatusPending || got.StatusDetail != "VPC is still attaching" {
		t.Fatalf("attachment after sweep = %+v", got)
	}
}

func TestReconcilerFailsClosedWhenRouteActivationFails(t *testing.T) {
	store := state.NewMemStore()
	_, err := store.UpsertAppPrivateNetworkAttachment(context.Background(), state.AppPrivateNetworkAttachment{
		AccountID: "acct", AppID: "app", NetworkID: "vpc-1", Region: "nyc3",
		CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")},
		Status: api.PrivateNetworkAttachmentStatusPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	connector, err := NewConfiguredConnector([]ConfiguredNetwork{{ID: "vpc-1", Region: "nyc3", Ready: true}})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewReconciler(store, connector, FuncRouteApplier(func(context.Context, string, []netip.Prefix) error {
		return errors.New("ip route: permission denied")
	}), ReconcilerOptions{Interval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Sweep(context.Background()); err == nil {
		t.Fatal("Sweep unexpectedly succeeded")
	}
	got, err := store.GetAppPrivateNetworkAttachment(context.Background(), "acct", "app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != api.PrivateNetworkAttachmentStatusError || got.StatusDetail != "route activation failed: ip route: permission denied" {
		t.Fatalf("attachment after failed sweep = %+v", got)
	}
}

type reportingRouteApplier struct {
	report RouteApplyReport
	err    error
}

func (a reportingRouteApplier) Apply(context.Context, string, []netip.Prefix) error {
	return a.err
}

func (a reportingRouteApplier) ApplyWithReport(context.Context, string, []netip.Prefix) (RouteApplyReport, error) {
	return a.report, a.err
}

func TestReconcilerIncludesPerNodeHealthInObservation(t *testing.T) {
	store := state.NewMemStore()
	_, err := store.UpsertAppPrivateNetworkAttachment(context.Background(), state.AppPrivateNetworkAttachment{
		AccountID: "acct", AppID: "app", NetworkID: "vpc-1", Region: "nyc3",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}, Status: api.PrivateNetworkAttachmentStatusPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	connector, err := NewConfiguredConnector([]ConfiguredNetwork{{ID: "vpc-1", Region: "nyc3", Ready: true}})
	if err != nil {
		t.Fatal(err)
	}
	applyErr := errors.New("node-b route update failed")
	var observed ReconcileObservation
	reconciler, err := NewReconciler(store, connector, reportingRouteApplier{
		report: RouteApplyReport{Nodes: []RouteNodeObservation{
			{NodeID: "node-a", Status: api.PrivateNetworkAttachmentStatusReady, Detail: "routes applied"},
			{NodeID: "node-b", Status: api.PrivateNetworkAttachmentStatusError, Detail: applyErr.Error()},
		}},
		err: applyErr,
	}, ReconcilerOptions{
		Interval: time.Second,
		Observe:  func(obs ReconcileObservation) { observed = obs },
	})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := reconciler.Sweep(context.Background())
	if !errors.Is(err, applyErr) {
		t.Fatalf("Sweep error = %v, want %v", err, applyErr)
	}
	if summary.NodeReady != 1 || summary.NodeFailed != 1 {
		t.Fatalf("summary = %+v, want one ready and one failed node", summary)
	}
	if observed.Outcome != "error" || len(observed.Nodes) != 2 {
		t.Fatalf("observation = %+v, want error with two node results", observed)
	}
	if observed.Nodes[1].NodeID != "node-b" || observed.Nodes[1].Status != api.PrivateNetworkAttachmentStatusError {
		t.Fatalf("failed node observation = %+v", observed.Nodes[1])
	}
}

type recordingFabricApplier struct {
	called bool
	err    error
}

func (a *recordingFabricApplier) ApplyWithReport(_ context.Context, attachment state.AppPrivateNetworkAttachment) (FabricApplyReport, error) {
	a.called = true
	if attachment.NetworkID == "" {
		return FabricApplyReport{}, errors.New("missing network id")
	}
	return FabricApplyReport{Nodes: []RouteNodeObservation{{NodeID: "node-a", Status: api.PrivateNetworkAttachmentStatusReady, Detail: "fabric bridge ready"}}}, a.err
}

func TestReconcilerRequiresFabricBeforeRouteActivation(t *testing.T) {
	store := state.NewMemStore()
	_, err := store.UpsertAppPrivateNetworkAttachment(context.Background(), state.AppPrivateNetworkAttachment{
		AccountID: "acct", AppID: "app", NetworkID: "vpc-1", Region: "nyc3",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}, Status: api.PrivateNetworkAttachmentStatusPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	connector, err := NewConfiguredConnector([]ConfiguredNetwork{{ID: "vpc-1", Region: "nyc3", Ready: true}})
	if err != nil {
		t.Fatal(err)
	}
	fabricErr := errors.New("bridge creation failed")
	fabric := &recordingFabricApplier{err: fabricErr}
	routesCalled := false
	reconciler, err := NewReconciler(store, connector, FuncRouteApplier(func(context.Context, string, []netip.Prefix) error {
		routesCalled = true
		return nil
	}), ReconcilerOptions{Interval: time.Second, Fabric: fabric})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Sweep(context.Background()); !errors.Is(err, fabricErr) {
		t.Fatalf("Sweep error = %v, want %v", err, fabricErr)
	}
	if !fabric.called || routesCalled {
		t.Fatalf("fabric called=%v routes called=%v, want fabric-only fail-closed path", fabric.called, routesCalled)
	}
	got, err := store.GetAppPrivateNetworkAttachment(context.Background(), "acct", "app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != api.PrivateNetworkAttachmentStatusError {
		t.Fatalf("attachment status = %q, want error", got.Status)
	}
}

func TestMergePrivateNetworkPolicyOnlyAllowsAttachmentNarrowing(t *testing.T) {
	destination := netip.MustParsePrefix("10.42.0.0/16")
	network := []netip.Prefix{netip.MustParsePrefix("10.42.8.0/24")}
	app := []netip.Prefix{netip.MustParsePrefix("10.42.8.0/25")}
	effective, err := mergePrivateNetworkPolicy(network, app, []netip.Prefix{destination})
	if err != nil || len(effective) != 1 || effective[0].String() != "10.42.8.0/25" {
		t.Fatalf("effective policy = %v, err=%v", effective, err)
	}
	if _, err := mergePrivateNetworkPolicy(network, []netip.Prefix{netip.MustParsePrefix("10.42.9.0/24")}, []netip.Prefix{destination}); err == nil {
		t.Fatal("broader/outside app policy unexpectedly accepted")
	}
}
