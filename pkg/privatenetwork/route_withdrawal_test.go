package privatenetwork_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	pn "github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/state"
)

type reviewConnector struct {
	ready bool
	err   error
}

func (c *reviewConnector) Check(context.Context, state.AppPrivateNetworkAttachment) (pn.CheckResult, error) {
	return pn.CheckResult{Ready: c.ready}, c.err
}

type reviewRoutes struct {
	current        []netip.Prefix
	calls          int
	failWithdrawal bool
}

func (r *reviewRoutes) Apply(_ context.Context, _ string, p []netip.Prefix) error {
	if len(p) == 0 && r.failWithdrawal {
		return errors.New("node temporarily unavailable")
	}
	r.current = append([]netip.Prefix(nil), p...)
	r.calls++
	return nil
}

func TestReconcilerRetriesWithdrawalAndRestoresRecoveredAttachment(t *testing.T) {
	for _, connectorError := range []bool{false, true} {
		t.Run(map[bool]string{false: "not-ready", true: "connector-error"}[connectorError], func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			_, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{AccountID: "acct", AppID: "app", NetworkID: "vpc", Region: "fra1", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}, Status: api.PrivateNetworkAttachmentStatusPending})
			if err != nil {
				t.Fatal(err)
			}
			connector := &reviewConnector{ready: true}
			routes := &reviewRoutes{}
			reconciler, err := pn.NewReconciler(store, connector, routes, pn.ReconcilerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = reconciler.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			connector.ready = false
			if connectorError {
				connector.err = errors.New("provider check failed")
			}
			routes.failWithdrawal = true
			if _, err = reconciler.Sweep(ctx); err == nil {
				t.Fatal("failed withdrawal was acknowledged")
			}
			row, err := store.GetAppPrivateNetworkAttachment(ctx, "acct", "app")
			if err != nil || row.Status != api.PrivateNetworkAttachmentStatusError {
				t.Fatalf("failed withdrawal status=%+v, %v", row, err)
			}
			routes.failWithdrawal = false
			_, _ = reconciler.Sweep(ctx) // A provider error is still reported, but routes must be revoked.
			if len(routes.current) != 0 {
				t.Fatalf("withdrawal retry left routes installed: %v", routes.current)
			}
			connector.ready = true
			connector.err = nil
			if _, err = reconciler.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			row, err = store.GetAppPrivateNetworkAttachment(ctx, "acct", "app")
			if err != nil || row.Status != api.PrivateNetworkAttachmentStatusReady || len(routes.current) != 1 {
				t.Fatalf("recovery failed: attachment=%+v routes=%v err=%v", row, routes.current, err)
			}
		})
	}
}
func TestReconcilerWithdrawsRoutesAfterReadyBecomesPending(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{AccountID: "acct-review", AppID: "app-review", NetworkID: "review-net", Region: "fra1", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}, Status: api.PrivateNetworkAttachmentStatusPending})
	if err != nil {
		t.Fatal(err)
	}
	c := &reviewConnector{ready: true}
	routes := &reviewRoutes{}
	r, err := pn.NewReconciler(store, c, routes, pn.ReconcilerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(routes.current) != 1 {
		t.Fatal("initial route not applied")
	}
	c.ready = false
	if _, err = r.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetAppPrivateNetworkAttachment(ctx, "acct-review", "app-review")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != api.PrivateNetworkAttachmentStatusPending {
		t.Fatal("expected pending status")
	}
	if len(routes.current) != 0 {
		t.Fatalf("attachment is pending but live routes remain %v; route applier calls=%d", routes.current, routes.calls)
	}
}
