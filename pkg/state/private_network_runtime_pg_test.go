package state_test

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
	"net/netip"
	"testing"
	"time"
)

func TestPgPrivateMemberOwnershipAndScope(t *testing.T) {
	store, ctx := pgStore(t)
	account, app, _ := seedLiveDeploy(t, store, ctx, "private-runtime")
	network, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{AccountID: account, Name: "runtime", Region: "fra1", CIDR: netip.MustParsePrefix("10.95.0.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{AccountID: account, AppID: app, NetworkID: network.ID, Region: "fra1", CIDRs: []netip.Prefix{network.CIDR}})
	if err != nil {
		t.Fatal(err)
	}
	vni, err := store.EnsurePrivateNetworkTransportVNI(ctx, account, network.ID, "fra1")
	if err != nil || vni == 0 {
		t.Fatalf("transport: %d %v", vni, err)
	}
	first, err := store.EnsurePrivateNetworkMember(ctx, account, app, network.ID, "first", true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.EnsurePrivateNetworkMember(ctx, account, app, network.ID, "second", true)
	if err != nil {
		t.Fatal(err)
	}
	if first.Address == second.Address || !first.Service.IsValid() || second.Service.IsValid() {
		t.Fatalf("ownership: %+v %+v", first, second)
	}
	if err := store.ReleasePrivateNetworkAddress(ctx, account, network.ID, "app", app); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("claimed service released: %v", err)
	}
	if err := store.WithPrivateNetworkScope(ctx, state.PrivateNetworkAttachmentScope(app), func(locked context.Context) error {
		blocked, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
		defer cancel()
		if err := store.DeleteAppPrivateNetworkAttachment(blocked, account, app); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cross-session writer passed lock: %v", err)
		}
		_, err := store.GetAppPrivateNetworkAttachment(locked, account, app)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	replacement := attachment
	replacement.CIDRs = []netip.Prefix{netip.MustParsePrefix("10.96.0.0/24")}
	if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePrivateNetworkAttachmentStatusForIntent(ctx, attachment, "ready", ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale intent approved: %v", err)
	}
	if err := store.ReleasePrivateNetworkMember(ctx, account, app, network.ID, "first", false); err != nil {
		t.Fatal(err)
	}
	second, err = store.EnsurePrivateNetworkMember(ctx, account, app, network.ID, "second", true)
	if err != nil || second.Service != first.Service {
		t.Fatalf("promotion: %+v %v", second, err)
	}
	if err := store.ReleasePrivateNetworkMember(ctx, "", "", "", "second", false); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAppPrivateNetworkAttachment(ctx, account, app); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePrivateNetwork(ctx, account, network.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsurePrivateNetworkTransportVNI(ctx, account, network.ID, "fra1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted transport recreated: %v", err)
	}
}

func TestPgPrivateTransportCollisionAndPinnedScope(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, _, _ := seedLiveDeploy(t, store, ctx, "vni-collision")
	network, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{AccountID: account, Name: "collision", Region: "fra1", CIDR: netip.MustParsePrefix("10.97.0.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	preferred := state.PreferredPrivateNetworkVNI(account, network.ID)
	if _, err := pool.Exec(ctx, `insert into private_network_transport_identities(network_id,account_id,region,vni) values($1,$2,$3,$4)`, "net-retired", account, "fra1", int32(preferred)); err != nil {
		t.Fatal(err)
	}
	vni, err := store.EnsurePrivateNetworkTransportVNI(ctx, account, network.ID, "fra1")
	if err != nil || vni == preferred || vni == 0 {
		t.Fatalf("collision retained: %d %v", vni, err)
	}
	config := pool.Config()
	config.MaxConns = 1
	single, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	// A background-work context must retain the connection holding the
	// private-network session lock, including nested queries and transactions.
	fresh := state.NewPgStore(single, state.WithBackgroundPool(pool))
	beforeBackground := pool.Stat().AcquireCount()
	timeout, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := fresh.WithPrivateNetworkScope(state.WithBackgroundWork(timeout), state.PrivateNetworkPeeringScope(account, "fra1"), func(locked context.Context) error {
		got, err := fresh.EnsurePrivateNetworkTransportVNI(locked, account, network.ID, "fra1")
		if err != nil {
			return err
		}
		if got != vni {
			t.Fatal("independent controller changed VNI")
		}
		return fresh.WithPrivateNetworkScope(locked, state.PrivateNetworkAttachmentScope("app"), func(nested context.Context) error {
			if _, err := fresh.GetPrivateNetwork(nested, account, network.ID); err != nil {
				return err
			}
			return fresh.CheckOrgAccess(nested)
		})
	}); err != nil {
		t.Fatalf("pinned pool deadlock: %v", err)
	}
	if got := pool.Stat().AcquireCount() - beforeBackground; got != 0 {
		t.Fatalf("pinned scope acquired %d background connections, want 0", got)
	}
}
