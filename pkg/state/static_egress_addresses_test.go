package state_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStaticEgressAddressesMem(t *testing.T) {
	m := state.NewMemStore()
	testStaticEgressAddresses(t, context.Background(), m, m)
}

func TestStaticEgressAddressesPg(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	testStaticEgressAddresses(t, ctx, s, s)
}

func testStaticEgressAddresses(t *testing.T, ctx context.Context, st state.Store, inv state.StaticEgressAddressStore) {
	t.Helper()
	newNode := func() state.ComputeNode {
		n, err := st.CreateComputeNode(ctx, state.ComputeNode{Name: "se-" + uuid.NewString()[:8], TargetURL: "tcp://127.0.0.1:50051",
			VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true})
		if err != nil {
			t.Fatalf("CreateComputeNode: %v", err)
		}
		return n
	}
	newAccount := func() state.Account {
		a, err := st.CreateAccount(ctx, "se-"+uuid.NewString()[:8]+"@example.com", api.PlanScale)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
		return a
	}
	nodeA, nodeB := newNode(), newNode()
	owner, other := newAccount(), newAccount()
	ip := netip.MustParseAddr("198.51.100.17")

	if ok, _ := st.ProvisionedStaticEgressIPExists(ctx, owner.ID, ip); ok {
		t.Fatal("unprovisioned address reported as provisioned")
	}
	if err := inv.AddStaticEgressAddress(ctx, state.StaticEgressAddress{IP: ip, NodeID: nodeA.ID, AccountID: owner.ID, Note: "ovh"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	t.Cleanup(func() { _ = inv.RemoveStaticEgressAddress(context.WithoutCancel(ctx), ip) })
	if err := inv.AddStaticEgressAddress(ctx, state.StaticEgressAddress{IP: ip, NodeID: nodeA.ID}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate Add err = %v, want ErrConflict", err)
	}
	if ok, err := st.ProvisionedStaticEgressIPExists(ctx, owner.ID, ip); err != nil || !ok {
		t.Fatalf("provisioned = %v, %v; want true", ok, err)
	}
	if ok, _ := st.ProvisionedStaticEgressIPExists(ctx, other.ID, ip); ok {
		t.Fatal("address provisioned for an account it is not assigned to")
	}
	if nodes, err := inv.StaticEgressAddressNodeIDs(ctx, owner.ID, ip); err != nil || len(nodes) != 1 || nodes[0] != nodeA.ID {
		t.Fatalf("node ids = %v, %v; want [%s]", nodes, err, nodeA.ID)
	}
	if grants, err := inv.AssignedStaticEgressAddressesForNode(ctx, nodeA.ID); err != nil || !hasGrant(grants, owner.ID, ip) {
		t.Fatalf("node A grants = %v, %v", grants, err)
	}

	app, err := st.CreateApp(ctx, state.App{AccountID: owner.ID, Slug: "se-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := st.UpdateApp(ctx, app.ID, state.UpdateAppParams{StaticEgressIP: &ip, SetStaticEgressIP: true}); err != nil {
		t.Fatalf("pin: %v", err)
	}
	got, err := inv.StaticEgressAddressByIP(ctx, ip)
	if err != nil || got.PinnedAppID != app.ID || got.NodeName != nodeA.Name || got.AccountID != owner.ID {
		t.Fatalf("read = %+v, %v", got, err)
	}

	// A node move follows the pin; an account change and a removal do not.
	if err := inv.UpdateStaticEgressAddress(ctx, state.StaticEgressAddress{IP: ip, NodeID: nodeB.ID, AccountID: owner.ID}); err != nil {
		t.Fatalf("move: %v", err)
	}
	if grants, _ := inv.AssignedStaticEgressAddressesForNode(ctx, nodeA.ID); hasGrant(grants, owner.ID, ip) {
		t.Fatal("moved address still granted on its old node")
	}
	if err := inv.UpdateStaticEgressAddress(ctx, state.StaticEgressAddress{IP: ip, NodeID: nodeB.ID, AccountID: other.ID}); !errors.Is(err, state.ErrStaticEgressAddressPinned) {
		t.Fatalf("reassign pinned err = %v", err)
	}
	if err := inv.RemoveStaticEgressAddress(ctx, ip); !errors.Is(err, state.ErrStaticEgressAddressPinned) {
		t.Fatalf("remove pinned err = %v", err)
	}

	// A deleted app's pin no longer holds the address.
	if err := st.DeleteApp(ctx, app.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if err := inv.UpdateStaticEgressAddress(ctx, state.StaticEgressAddress{IP: ip, NodeID: nodeB.ID, AccountID: other.ID}); err != nil {
		t.Fatalf("reassign after delete: %v", err)
	}
	if deleted, err := st.AppByID(ctx, app.ID); err != nil || deleted.StaticEgressIP != nil {
		t.Fatalf("deleted app keeps pin: %+v, %v", deleted.StaticEgressIP, err)
	}
	if ips, err := inv.StaticEgressAddressesForAccount(ctx, other.ID); err != nil || len(ips) != 1 || ips[0] != ip {
		t.Fatalf("account addresses = %v, %v", ips, err)
	}
	if err := inv.RemoveStaticEgressAddress(ctx, ip); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := inv.StaticEgressAddressByIP(ctx, ip); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("read removed err = %v", err)
	}
	if err := inv.RemoveStaticEgressAddress(ctx, ip); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("remove twice err = %v", err)
	}
}

func hasGrant(grants []state.StaticEgressIPGrant, account string, ip netip.Addr) bool {
	for _, g := range grants {
		if g.AccountID == account && g.IP == ip {
			return true
		}
	}
	return false
}
