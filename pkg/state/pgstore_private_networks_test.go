package state_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPrivateNetworkLifecycleAndAttachment(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "private-network-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "private-network-app",
		Type:      state.AppTypeApp,
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	network, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: account.ID,
		Name:      "prod",
		Region:    "fra1",
		CIDR:      netip.MustParsePrefix("10.42.0.0/28"),
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetwork: %v", err)
	}
	if network.ID == "" || network.Status != api.PrivateNetworkStatusReady || network.CIDR.String() != "10.42.0.0/28" {
		t.Fatalf("created network = %+v", network)
	}
	if _, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: account.ID, Name: network.Name, Region: network.Region,
		CIDR: netip.MustParsePrefix("10.43.0.0/28"),
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate network name error = %v, want conflict", err)
	}
	if _, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: account.ID, Name: "overlap", Region: network.Region, CIDR: network.CIDR,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping network error = %v, want conflict", err)
	}

	got, err := store.GetPrivateNetwork(ctx, account.ID, network.ID)
	if err != nil || got.ID != network.ID {
		t.Fatalf("GetPrivateNetwork = %+v, %v", got, err)
	}
	secondNetwork, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: account.ID,
		Name:      "alpha",
		Region:    "fra1",
		CIDR:      netip.MustParsePrefix("10.43.0.0/28"),
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetwork(alpha): %v", err)
	}
	networks, err := store.ListPrivateNetworks(ctx, account.ID)
	if err != nil || len(networks) != 2 || networks[0].ID != secondNetwork.ID || networks[1].ID != network.ID {
		t.Fatalf("ListPrivateNetworks = %+v, %v", networks, err)
	}

	if _, err := store.AllocatePrivateNetworkAddress(ctx, account.ID, network.ID, "", "owner"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid address owner error = %v, want invalid argument", err)
	}
	first, err := store.AllocatePrivateNetworkAddress(ctx, account.ID, network.ID, "app", app.ID)
	if err != nil || first.Address.String() != "10.42.0.2" {
		t.Fatalf("first address = %+v, %v", first, err)
	}
	replayed, err := store.AllocatePrivateNetworkAddress(ctx, account.ID, network.ID, "app", app.ID)
	if err != nil || replayed.ID != first.ID || replayed.Address != first.Address {
		t.Fatalf("replayed address = %+v, %v", replayed, err)
	}
	second, err := store.AllocatePrivateNetworkAddress(ctx, account.ID, network.ID, "service", "worker")
	if err != nil || second.Address.String() != "10.42.0.3" {
		t.Fatalf("second address = %+v, %v", second, err)
	}

	attachment, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: account.ID,
		AppID:     app.ID,
		NetworkID: network.ID,
		Region:    network.Region,
		CIDRs:     []netip.Prefix{network.CIDR},
	})
	if err != nil {
		t.Fatalf("UpsertAppPrivateNetworkAttachment: %v", err)
	}
	if attachment.Status != "pending" || attachment.StatusDetail == "" {
		t.Fatalf("attachment defaults = %+v", attachment)
	}
	gotAttachment, err := store.GetAppPrivateNetworkAttachment(ctx, account.ID, app.ID)
	if err != nil || gotAttachment.ID != attachment.ID || len(gotAttachment.CIDRs) != 1 {
		t.Fatalf("GetAppPrivateNetworkAttachment = %+v, %v", gotAttachment, err)
	}
	attachments, err := store.ListAppPrivateNetworkAttachments(ctx, []string{"pending"}, 10)
	if err != nil || len(attachments) != 1 || attachments[0].ID != attachment.ID {
		t.Fatalf("ListAppPrivateNetworkAttachments = %+v, %v", attachments, err)
	}
	ready, err := store.UpdateAppPrivateNetworkAttachmentStatus(ctx, account.ID, app.ID, "ready", "connected")
	if err != nil || ready.Status != "ready" || ready.StatusDetail != "connected" {
		t.Fatalf("UpdateAppPrivateNetworkAttachmentStatus = %+v, %v", ready, err)
	}
	if _, err := store.UpdateAppPrivateNetworkAttachmentStatus(ctx, account.ID, app.ID, "invalid", ""); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid attachment status error = %v, want invalid argument", err)
	}
	if err := store.DeletePrivateNetwork(ctx, account.ID, network.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete attached network error = %v, want conflict", err)
	}
	if err := store.DeleteAppPrivateNetworkAttachment(ctx, account.ID, app.ID); err != nil {
		t.Fatalf("DeleteAppPrivateNetworkAttachment: %v", err)
	}

	if err := store.ReleasePrivateNetworkAddress(ctx, account.ID, network.ID, "app", "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("release missing address error = %v, want not found", err)
	}
	if err := store.ReleasePrivateNetworkAddress(ctx, account.ID, network.ID, "app", app.ID); err != nil {
		t.Fatalf("ReleasePrivateNetworkAddress: %v", err)
	}
	if err := store.DeletePrivateNetwork(ctx, account.ID, network.ID); err != nil {
		t.Fatalf("DeletePrivateNetwork: %v", err)
	}
	if _, err := store.GetPrivateNetwork(ctx, account.ID, network.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("GetPrivateNetwork(deleted) error = %v, want not found", err)
	}
	if err := store.DeletePrivateNetwork(ctx, account.ID, network.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("DeletePrivateNetwork(deleted) error = %v, want not found", err)
	}
}
