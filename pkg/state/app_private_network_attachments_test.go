package state

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestMemStorePrivateNetworkAttachmentLifecycle(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	in := AppPrivateNetworkAttachment{
		AccountID: "acct-1", AppID: "app-1", NetworkID: "prod-vpc", Region: "fra1",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}, Status: "pending",
	}
	got, err := m.UpsertAppPrivateNetworkAttachment(ctx, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got.ID == "" || got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("upsert timestamps/id missing: %+v", got)
	}
	got.CIDRs[0] = netip.MustParsePrefix("10.30.0.0/16")
	read, err := m.GetAppPrivateNetworkAttachment(ctx, "acct-1", "app-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if read.CIDRs[0].String() != "10.20.0.0/16" {
		t.Fatalf("store aliased CIDRs: %v", read.CIDRs)
	}
	updated, err := m.UpsertAppPrivateNetworkAttachment(ctx, AppPrivateNetworkAttachment{
		AccountID: "acct-1", AppID: "app-1", NetworkID: "prod-vpc-2", Region: "ams1",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.40.0.0/16")}, Status: "ready",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.ID != got.ID || updated.Status != "ready" {
		t.Fatalf("update did not preserve identity/status: %+v", updated)
	}
	if err := m.DeleteAppPrivateNetworkAttachment(ctx, "acct-1", "app-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.GetAppPrivateNetworkAttachment(ctx, "acct-1", "app-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete err = %v, want ErrNotFound", err)
	}
}

func TestMemStorePrivateNetworkAttachmentAccountIsolation(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	_, err := m.UpsertAppPrivateNetworkAttachment(ctx, AppPrivateNetworkAttachment{
		AccountID: "acct-1", AppID: "app-1", NetworkID: "prod-vpc", Region: "fra1",
		CIDRs: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := m.GetAppPrivateNetworkAttachment(ctx, "acct-2", "app-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account get err = %v, want ErrNotFound", err)
	}
	if err := m.DeleteAppPrivateNetworkAttachment(ctx, "acct-2", "app-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account delete err = %v, want ErrNotFound", err)
	}
}
