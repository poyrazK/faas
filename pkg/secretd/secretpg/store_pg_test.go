//go:build !no_pg

// adr: 20261110210000
package secretpg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/secretd/secretpg"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStoreOnPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	st := state.NewPgStore(pool)
	store := secretpg.New(pool, st)
	ctx := t.Context()

	node, err := st.CreateComputeNode(ctx, state.ComputeNode{
		Name: "secretd-" + uuid.NewString()[:8], TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := st.CreateComputeNode(ctx, state.ComputeNode{
		Name: "secretd-peer-" + uuid.NewString()[:8], TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@secretd.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "secretd-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAppNodeID(ctx, app.ID, node.ID); err != nil {
		t.Fatal(err)
	}

	// Node names resolve to active nodes only.
	if id, err := store.NodeIDByName(ctx, node.Name); err != nil || id != node.ID {
		t.Fatalf("node by name = %q, %v", id, err)
	}
	if _, err := store.NodeIDByName(ctx, "no-such-node"); !errors.Is(err, secretd.ErrUnknownNode) {
		t.Fatalf("unknown node = %v", err)
	}
	if err := st.SetComputeNodeActive(ctx, peer.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NodeIDByName(ctx, peer.Name); !errors.Is(err, secretd.ErrUnknownNode) {
		t.Fatalf("inactive node = %v", err)
	}

	// The app's owner and resource account.
	target, err := store.AppTarget(ctx, app.ID)
	if err != nil || target.OwnerNodeID != node.ID || target.AccountID != account.ID {
		t.Fatalf("app target = %+v, %v", target, err)
	}

	// Grants: valid until expiry, renewed by upsert, swept after.
	now := time.Now().UTC().Truncate(time.Microsecond)
	expires, err := store.UpsertGrant(ctx, app.ID, peer.ID, node.ID, now.Add(10*time.Minute))
	if err != nil || !expires.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("upsert grant = %v, %v", expires, err)
	}
	if ok, err := store.GrantValid(ctx, app.ID, peer.ID, now); err != nil || !ok {
		t.Fatalf("grant valid now = %v, %v", ok, err)
	}
	if ok, _ := store.GrantValid(ctx, app.ID, peer.ID, now.Add(11*time.Minute)); ok {
		t.Fatal("grant valid after expiry")
	}
	if ok, _ := store.GrantValid(ctx, app.ID, node.ID, now); ok {
		t.Fatal("a grant for the peer is valid for another node")
	}
	if _, err := store.UpsertGrant(ctx, app.ID, peer.ID, node.ID, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.GrantValid(ctx, app.ID, peer.ID, now.Add(20*time.Minute)); !ok {
		t.Fatal("renewed grant not extended")
	}
	if n, err := store.DeleteExpiredGrants(ctx, now.Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("expired sweep = %d, %v", n, err)
	}

	// Audit rows land.
	if err := store.RecordOpen(ctx, node.ID, app.ID, "owner", 3); err != nil {
		t.Fatal(err)
	}
	var via string
	var entries int
	if err := pool.QueryRow(ctx, `select via, entries from secret_open_audit where app_id = $1`, app.ID).Scan(&via, &entries); err != nil || via != "owner" || entries != 3 {
		t.Fatalf("audit = %s %d %v", via, entries, err)
	}
	if err := store.RecordOpen(ctx, node.ID, app.ID, "stolen", 1); err == nil {
		t.Fatal("audit accepted an unknown via")
	}
}
