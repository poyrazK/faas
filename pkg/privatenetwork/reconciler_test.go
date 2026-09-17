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
