package state_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStaticProvisioningBundleOwnersAreIndependent(t *testing.T) {
	store := state.NewMemStore()
	verifyStaticProvisioningOwners(t, store)
}

type provisioningStore interface {
	state.StaticEgressProvisioningStore
	ProvisionedStaticEgressIPExists(ctx context.Context, accountID string, ip netip.Addr) (bool, error)
}

func verifyStaticProvisioningOwners(t *testing.T, store provisioningStore) {
	t.Helper()
	account := uuid.NewString()
	ip := netip.MustParseAddr("203.0.113.95")
	grants := []state.StaticEgressIPGrant{{AccountID: account, IP: ip}}
	for _, source := range []string{"node-one", "node-two"} {
		if err := store.ReplaceStaticEgressProvisioning(t.Context(), source, grants); err != nil {
			t.Fatal(err)
		}
	}
	if legacy, ok := store.(interface {
		ReplaceProvisionedStaticEgressIPs(context.Context, string, []netip.Addr) error
	}); ok {
		if err := legacy.ReplaceProvisionedStaticEgressIPs(t.Context(), account, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ReplaceStaticEgressProvisioning(t.Context(), "node-one", nil); err != nil {
		t.Fatal(err)
	}
	if exists, err := store.ProvisionedStaticEgressIPExists(t.Context(), account, ip); err != nil || !exists {
		t.Fatalf("one node revoked another node's grant: exists=%v err=%v", exists, err)
	}
	if err := store.ReplaceStaticEgressProvisioning(t.Context(), "node-two", nil); err != nil {
		t.Fatal(err)
	}
	if exists, err := store.ProvisionedStaticEgressIPExists(t.Context(), account, ip); err != nil || exists {
		t.Fatalf("empty final bundle retained a grant: exists=%v err=%v", exists, err)
	}
}

func TestPgStaticProvisioningBundleOwnersAreIndependent(t *testing.T) {
	store, _ := pgStore(t)
	verifyStaticProvisioningOwners(t, store)
}

func TestPgPeeringDeletionRollsBackWithoutDurableRevocation(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "peering-outbox-"+uuid.NewString()+"@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	networks := make([]state.PrivateNetwork, 2)
	for i, name := range []string{"left", "right"} {
		networks[i], err = store.CreatePrivateNetwork(ctx, state.PrivateNetwork{AccountID: account.ID, Name: name, Region: "fra1", CIDR: netip.MustParsePrefix([]string{"10.92.0.0/24", "10.93.0.0/24"}[i])})
		if err != nil {
			t.Fatal(err)
		}
	}
	peer, err := store.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{AccountID: account.ID, LeftNetworkID: networks[0].ID, RightNetworkID: networks[1].ID, Region: "fra1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `alter table notification_outbox add constraint reject_peering_revocation_test check (channel <> 'private_network_changed')`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePrivateNetworkPeeringDurably(ctx, account.ID, peer.ID); err == nil {
		t.Fatal("failed outbox insertion acknowledged deletion")
	}
	if _, err := store.GetPrivateNetworkPeering(ctx, account.ID, peer.ID); err != nil {
		t.Fatalf("failed handoff lost final peering intent: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table notification_outbox drop constraint reject_peering_revocation_test`); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePrivateNetworkPeeringDurably(ctx, account.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := pool.QueryRow(ctx, `select payload from notification_outbox where channel=$1 order by id desc limit 1`, db.NotifyPrivateNetworkChanged).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var event struct {
		Kind, Region, Status string
		PeeringID            string `json:"peering_id"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != "private_network_peering" || event.Region != "fra1" || event.Status != "deleted" || event.PeeringID != peer.ID {
		t.Fatalf("wrong durable revocation: %s", payload)
	}
}

func TestPrivateAttachmentScopeWalksAllPages(t *testing.T) {
	store := state.NewMemStore()
	for i := range 1003 {
		account := "acct-pages"
		if i == 1002 {
			account = "other-account"
		}
		if _, err := store.UpsertAppPrivateNetworkAttachment(t.Context(), state.AppPrivateNetworkAttachment{AccountID: account, AppID: fmt.Sprintf("app-%04d", i), NetworkID: "network", Region: "fra1", Status: api.PrivateNetworkAttachmentStatusReady}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := state.ListScopedPrivateNetworkAttachments(t.Context(), store, state.PrivateNetworkAttachmentPage{AccountID: "acct-pages", Region: "fra1", Statuses: []string{api.PrivateNetworkAttachmentStatusReady}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1002 {
		t.Fatalf("scoped pagination returned %d rows, want 1002", len(rows))
	}
}

func TestPgPrivateAttachmentScopedPage(t *testing.T) {
	store, ctx := pgStore(t)
	account, app, _ := seedLiveDeploy(t, store, ctx, "scoped-page")
	cidr := netip.MustParsePrefix("10.94.0.0/24")
	network, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{AccountID: account, Name: "scoped-page", Region: "fra1", CIDR: cidr})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{AccountID: account, AppID: app, NetworkID: network.ID, Region: "fra1", CIDRs: []netip.Prefix{cidr}, AllowedCIDRs: []netip.Prefix{cidr}}); err != nil {
		t.Fatal(err)
	}
	page := state.PrivateNetworkAttachmentPage{AccountID: account, Region: "fra1", NetworkID: network.ID, Statuses: []string{"pending"}, Limit: 1}
	rows, err := store.ListPrivateNetworkAttachmentPage(ctx, page)
	if err != nil || len(rows) != 1 {
		t.Fatalf("scoped first page: rows=%+v err=%v", rows, err)
	}
	if rows[0].AppID != app || len(rows[0].CIDRs) != 1 || rows[0].CIDRs[0] != cidr || len(rows[0].AllowedCIDRs) != 1 || rows[0].AllowedCIDRs[0] != cidr {
		t.Fatalf("scoped page lost identifiers or CIDRs: %+v", rows[0])
	}
	page.AfterAppID = app
	rows, err = store.ListPrivateNetworkAttachmentPage(ctx, page)
	if err != nil || len(rows) != 0 {
		t.Fatalf("cursor repeated first page: rows=%+v err=%v", rows, err)
	}
	page.AfterAppID, page.Region = "", "ams1"
	rows, err = store.ListPrivateNetworkAttachmentPage(ctx, page)
	if err != nil || len(rows) != 0 {
		t.Fatalf("region filter leaked a row: rows=%+v err=%v", rows, err)
	}
}
