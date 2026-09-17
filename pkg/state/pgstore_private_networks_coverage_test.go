package state_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_PrivateNetworkLifecycle keeps the Postgres implementation in lockstep
// with the MemStore contract. The state coverage shard is the only job that
// exercises these transaction-heavy methods, so cover both idempotent and
// negative paths here instead of relying on handler tests.
func TestPg_PrivateNetworkLifecycle(t *testing.T) {
	s, ctx, account, app, _ := pgCoverageFixture(t)
	network, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: account.ID,
		Name:      "pg-private-network",
		Region:    "fra1",
		CIDR:      netip.MustParsePrefix("10.55.0.0/28"),
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetwork: %v", err)
	}
	if network.ID == "" || network.Status != "ready" || network.CIDR.String() != "10.55.0.0/28" {
		t.Fatalf("created network = %+v", network)
	}

	if got, err := s.GetPrivateNetwork(ctx, account.ID, network.ID); err != nil || got.ID != network.ID {
		t.Fatalf("GetPrivateNetwork = %+v, %v", got, err)
	}
	if got, err := s.ListPrivateNetworks(ctx, account.ID); err != nil || len(got) != 1 || got[0].ID != network.ID {
		t.Fatalf("ListPrivateNetworks = %+v, %v", got, err)
	}
	if _, err := s.GetPrivateNetwork(ctx, uuid.NewString(), network.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("GetPrivateNetwork wrong account = %v", err)
	}

	if _, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: account.ID,
		Name:      "pg-private-network",
		Region:    "fra1",
		CIDR:      netip.MustParsePrefix("10.56.0.0/28"),
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate name = %v, want conflict", err)
	}
	if _, err := s.CreatePrivateNetwork(ctx, state.PrivateNetwork{
		AccountID: account.ID,
		Name:      "pg-private-overlap",
		Region:    "fra1",
		CIDR:      netip.MustParsePrefix("10.55.0.0/28"),
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("overlapping CIDR = %v, want conflict", err)
	}

	first, err := s.AllocatePrivateNetworkAddress(ctx, account.ID, network.ID, "app", app.ID)
	if err != nil {
		t.Fatalf("AllocatePrivateNetworkAddress: %v", err)
	}
	if first.Address.String() != "10.55.0.2" {
		t.Fatalf("first allocated address = %s, want 10.55.0.2", first.Address)
	}
	replayed, err := s.AllocatePrivateNetworkAddress(ctx, account.ID, network.ID, " app ", " "+app.ID+" ")
	if err != nil || replayed.ID != first.ID || replayed.Address != first.Address {
		t.Fatalf("idempotent allocation = %+v, %v", replayed, err)
	}
	second, err := s.AllocatePrivateNetworkAddress(ctx, account.ID, network.ID, "app", "second-owner")
	if err != nil || second.Address.String() != "10.55.0.3" {
		t.Fatalf("second allocation = %+v, %v", second, err)
	}
	if _, err := s.AllocatePrivateNetworkAddress(ctx, account.ID, network.ID, "", "owner"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid owner = %v, want invalid argument", err)
	}
	if _, err := s.AllocatePrivateNetworkAddress(ctx, account.ID, "missing-network", "app", "owner"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing network allocation = %v, want not found", err)
	}

	if err := s.ReleasePrivateNetworkAddress(ctx, account.ID, network.ID, "app", app.ID); err != nil {
		t.Fatalf("ReleasePrivateNetworkAddress: %v", err)
	}
	if err := s.ReleasePrivateNetworkAddress(ctx, account.ID, network.ID, "app", app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("repeated release = %v, want not found", err)
	}

	if _, err := s.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{
		AccountID: account.ID,
		AppID:     app.ID,
		NetworkID: network.ID,
		Region:    "fra1",
		Status:    "pending",
	}); err != nil {
		t.Fatalf("UpsertAppPrivateNetworkAttachment: %v", err)
	}
	if err := s.DeletePrivateNetwork(ctx, account.ID, network.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete attached network = %v, want conflict", err)
	}
	if err := s.DeleteAppPrivateNetworkAttachment(ctx, account.ID, app.ID); err != nil {
		t.Fatalf("DeleteAppPrivateNetworkAttachment: %v", err)
	}
	if err := s.DeletePrivateNetwork(ctx, account.ID, network.ID); err != nil {
		t.Fatalf("DeletePrivateNetwork: %v", err)
	}
	if err := s.DeletePrivateNetwork(ctx, account.ID, network.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("repeated delete = %v, want not found", err)
	}
}
