package state_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgStorePrivateNetworkAndAttachmentLifecycle covers the Postgres SQL
// boundary for the Gregale-owned network fabric. The in-memory tests exercise
// the same contract, but this pins transactions, CIDR round-trips, status
// filtering, and the attachment guard that prevents deleting an in-use
// network.
func TestPgStorePrivateNetworkAndAttachmentLifecycle(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "private-network")
	networkCIDR := netip.MustParsePrefix("10.60.0.0/28")

	network, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: accountID,
		ID:        "net-pg-private-network",
		Name:      "prod",
		Region:    "fra1",
		CIDR:      networkCIDR,
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetwork: %v", err)
	}
	if network.ID != "net-pg-private-network" || network.CIDR != networkCIDR || network.Status != "ready" {
		t.Fatalf("created network = %+v", network)
	}

	got, err := s.GetPrivateNetwork(ctx, accountID, network.ID)
	if err != nil {
		t.Fatalf("GetPrivateNetwork: %v", err)
	}
	if got.CIDR != networkCIDR || got.Region != "fra1" {
		t.Fatalf("round-tripped network = %+v", got)
	}
	listed, err := s.ListPrivateNetworks(ctx, accountID)
	if err != nil || len(listed) != 1 || listed[0].ID != network.ID {
		t.Fatalf("ListPrivateNetworks = %+v, err=%v", listed, err)
	}

	if _, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: accountID, Name: "prod", Region: "fra1", CIDR: netip.MustParsePrefix("10.61.0.0/28"),
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate network name = %v, want conflict", err)
	}
	if _, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: accountID, Name: "overlap", Region: "fra1", CIDR: networkCIDR,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping network CIDR = %v, want conflict", err)
	}
	if _, err := s.GetPrivateNetwork(ctx, uuid.NewString(), network.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account GetPrivateNetwork = %v, want not found", err)
	}

	attachment, err := s.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: accountID,
		AppID:     appID,
		NetworkID: network.ID,
		Region:    network.Region,
		CIDRs:     []netip.Prefix{networkCIDR},
	})
	if err != nil {
		t.Fatalf("UpsertAppPrivateNetworkAttachment: %v", err)
	}
	if attachment.Status != "pending" || attachment.StatusDetail == "" {
		t.Fatalf("default attachment state = %+v", attachment)
	}
	if err := s.DeletePrivateNetwork(ctx, accountID, network.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete attached network = %v, want conflict", err)
	}
	fetchedAttachment, err := s.GetAppPrivateNetworkAttachment(ctx, accountID, appID)
	if err != nil || len(fetchedAttachment.CIDRs) != 1 || fetchedAttachment.CIDRs[0] != networkCIDR {
		t.Fatalf("GetAppPrivateNetworkAttachment = %+v, err=%v", fetchedAttachment, err)
	}
	attachments, err := s.ListAppPrivateNetworkAttachments(ctx, []string{"pending"}, 10)
	if err != nil || len(attachments) != 1 || attachments[0].AppID != appID {
		t.Fatalf("ListAppPrivateNetworkAttachments = %+v, err=%v", attachments, err)
	}
	if _, err := s.ListAppPrivateNetworkAttachments(ctx, []string{"bogus"}, 10); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid attachment status = %v, want invalid argument", err)
	}
	if _, err := s.UpdateAppPrivateNetworkAttachmentStatus(ctx, accountID, appID, "ready", "routes applied"); err != nil {
		t.Fatalf("UpdateAppPrivateNetworkAttachmentStatus: %v", err)
	}
	ready, err := s.ListAppPrivateNetworkAttachments(ctx, []string{"ready"}, 10)
	if err != nil || len(ready) != 1 || ready[0].StatusDetail != "routes applied" {
		t.Fatalf("ready attachments = %+v, err=%v", ready, err)
	}

	if _, err := s.AllocatePrivateNetworkAddress(ctx, accountID, network.ID, "", "owner"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid address owner = %v, want invalid argument", err)
	}
	first, err := s.AllocatePrivateNetworkAddress(ctx, accountID, network.ID, "app", "one")
	if err != nil {
		t.Fatalf("AllocatePrivateNetworkAddress: %v", err)
	}
	if first.Address.String() != "10.60.0.2" {
		t.Fatalf("first allocated address = %s, want 10.60.0.2", first.Address)
	}
	replayed, err := s.AllocatePrivateNetworkAddress(ctx, accountID, network.ID, "app", "one")
	if err != nil || replayed.ID != first.ID || replayed.Address != first.Address {
		t.Fatalf("idempotent allocation = %+v, err=%v", replayed, err)
	}
	if _, err := s.AllocatePrivateNetworkAddress(ctx, uuid.NewString(), network.ID, "app", "other"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account allocation = %v, want not found", err)
	}
	if err := s.ReleasePrivateNetworkAddress(ctx, accountID, network.ID, "app", "one"); err != nil {
		t.Fatalf("ReleasePrivateNetworkAddress: %v", err)
	}
	if err := s.ReleasePrivateNetworkAddress(ctx, accountID, network.ID, "app", "one"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("release missing address = %v, want not found", err)
	}

	if err := s.DeleteAppPrivateNetworkAttachment(ctx, uuid.NewString(), appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account attachment delete = %v, want not found", err)
	}
	if err := s.DeleteAppPrivateNetworkAttachment(ctx, accountID, appID); err != nil {
		t.Fatalf("DeleteAppPrivateNetworkAttachment: %v", err)
	}
	if _, err := s.GetAppPrivateNetworkAttachment(ctx, accountID, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("attachment after delete = %v, want not found", err)
	}
	if err := s.DeletePrivateNetwork(ctx, accountID, network.ID); err != nil {
		t.Fatalf("DeletePrivateNetwork: %v", err)
	}
	if _, err := s.GetPrivateNetwork(ctx, accountID, network.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("network after delete = %v, want not found", err)
	}
}
