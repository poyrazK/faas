package state_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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

func TestPgStorePrivateNetworkPeeringLifecycle(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, _, _ := seedLiveDeploy(t, s, ctx, "private-network-peering")
	for _, network := range []state.PrivateNetwork{
		{ID: "net-peering-left", AccountID: accountID, Name: "left", Region: "fra1", CIDR: netip.MustParsePrefix("10.80.0.0/28")},
		{ID: "net-peering-right", AccountID: accountID, Name: "right", Region: "fra1", CIDR: netip.MustParsePrefix("10.80.1.0/28")},
	} {
		if _, err := s.CreatePrivateNetwork(ctx, network); err != nil {
			t.Fatalf("CreatePrivateNetwork(%s): %v", network.ID, err)
		}
	}

	created, err := s.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{
		ID: "peer-pg-left-right", AccountID: accountID, LeftNetworkID: "net-peering-right", RightNetworkID: "net-peering-left", Region: "fra1",
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetworkPeering: %v", err)
	}
	if created.LeftNetworkID != "net-peering-left" || created.RightNetworkID != "net-peering-right" || created.Status != "pending" || created.StatusDetail == "" {
		t.Fatalf("created peering = %+v", created)
	}
	listed, err := s.ListPrivateNetworkPeerings(ctx, accountID, "net-peering-left")
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("ListPrivateNetworkPeerings = %+v, err=%v", listed, err)
	}
	reconcileRows, err := s.ListPrivateNetworkPeeringsForReconcile(ctx, []string{api.PrivateNetworkPeeringStatusPending}, 10)
	if err != nil || len(reconcileRows) != 1 || reconcileRows[0].ID != created.ID {
		t.Fatalf("ListPrivateNetworkPeeringsForReconcile = %+v, err=%v", reconcileRows, err)
	}
	if _, err := s.ListPrivateNetworkPeeringsForReconcile(ctx, []string{"bogus"}, 10); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid peering status = %v, want invalid argument", err)
	}
	if _, err := s.ListPrivateNetworkPeeringsForReconcile(ctx, nil, 0); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid peering limit = %v, want invalid argument", err)
	}
	updated, err := s.UpdatePrivateNetworkPeeringStatus(ctx, accountID, created.ID, api.PrivateNetworkPeeringStatusReady, "routes active")
	if err != nil || updated.Status != api.PrivateNetworkPeeringStatusReady || updated.StatusDetail != "routes active" {
		t.Fatalf("UpdatePrivateNetworkPeeringStatus = %+v, err=%v", updated, err)
	}
	if _, err := s.UpdatePrivateNetworkPeeringStatus(ctx, accountID, created.ID, "bogus", ""); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid peering update status = %v, want invalid argument", err)
	}
	got, err := s.GetPrivateNetworkPeering(ctx, accountID, created.ID)
	if err != nil || got.ID != created.ID || got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("GetPrivateNetworkPeering = %+v, err=%v", got, err)
	}
	if _, err := s.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{
		ID: "peer-pg-reverse", AccountID: accountID, LeftNetworkID: "net-peering-left", RightNetworkID: "net-peering-right", Region: "fra1",
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate peering = %v, want conflict", err)
	}
	if _, err := s.GetPrivateNetworkPeering(ctx, uuid.NewString(), created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account peering get = %v, want not found", err)
	}
	if err := s.DeletePrivateNetworkPeering(ctx, uuid.NewString(), created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account peering delete = %v, want not found", err)
	}
	if err := s.DeletePrivateNetworkPeering(ctx, accountID, created.ID); err != nil {
		t.Fatalf("DeletePrivateNetworkPeering: %v", err)
	}
	if err := s.DeletePrivateNetworkPeering(ctx, accountID, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("delete missing peering = %v, want not found", err)
	}
}
