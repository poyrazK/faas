package main

import (
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrivateNetworkMemberMoveKeepsOldNodeFenced(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "private-member-node@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "private-member", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:member", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	network, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{AccountID: account.ID, Name: "members", Region: "fra1", CIDR: netip.MustParsePrefix("10.98.0.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{AccountID: account.ID, AppID: app.ID, NetworkID: network.ID, Region: "fra1", Status: "ready", CIDRs: []netip.Prefix{network.CIDR}}); err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(ctx, app.ID, deployment.ID, string(state.StateRunning), 128, "node-a", "wake-a")
	if err != nil {
		t.Fatal(err)
	}
	request := fcvm.PrivateNetworkMemberRequest{AccountID: account.ID, AppID: app.ID, NetworkID: network.ID, InstanceID: instance.ID, DeploymentID: deployment.ID, Eligible: true, RequireReady: true}
	first, err := privateNetworkMemberSource(store, "node-a")(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Service.IsValid() {
		t.Fatal("first node did not own the stable service address")
	}
	if err := store.MarkInstanceMigrating(ctx, instance.ID, "node-a", "migration"); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateInstanceOwner(ctx, instance.ID, "node-a", "node-b", "migration"); err != nil {
		t.Fatal(err)
	}
	second, err := privateNetworkMemberSource(store, "node-b")(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Address == first.Address || second.Service.IsValid() {
		t.Fatalf("replacement inherited an unfenced owner's identity: %+v %+v", first, second)
	}
	// vmmd calls release only after proving the previous node's physical cleanup.
	if err := privateNetworkMemberRelease(store, "node-a")(ctx, account.ID, app.ID, network.ID, instance.ID, false); err != nil {
		t.Fatal(err)
	}
	promoted, err := privateNetworkMemberSource(store, "node-b")(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Address != second.Address || promoted.Service != first.Service {
		t.Fatalf("confirmed cleanup failed to promote the new owner: %+v %v", promoted, err)
	}
}
