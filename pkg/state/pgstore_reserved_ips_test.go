package state_test

// adr: 171 — provider-neutral reserved public IP lease persistence and fail-closed assignment.

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreReservedIPLeaseLifecycle(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "reserved-ip-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID:      account.ID,
		Slug:           "reserved-ip-" + uuid.NewString()[:8],
		Type:           state.AppTypeApp,
		RAMMB:          512,
		MaxConcurrency: 1,
		IdleTimeoutS:   60,
	})
	if err != nil {
		t.Fatal(err)
	}

	lease, err := store.UpsertReservedIP(ctx, state.ReservedIP{
		AccountID: account.ID,
		Region:    "fra1",
		Address:   netip.MustParseAddr("198.51.100.77"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Status != "available" || lease.Generation != 0 {
		t.Fatalf("created lease = %#v", lease)
	}
	if _, err := store.GetReservedIP(ctx, account.ID, lease.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ListReservedIPs(ctx, account.ID, "fra1"); err != nil || len(got) != 1 {
		t.Fatalf("ListReservedIPs = %#v, %v", got, err)
	}

	lease, err = store.UpsertReservedIP(ctx, state.ReservedIP{
		ID:           lease.ID,
		AccountID:    account.ID,
		Region:       "fra1",
		Address:      lease.Address,
		StatusDetail: "inventory ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if lease.StatusDetail != "inventory ready" {
		t.Fatalf("updated lease detail = %#v", lease)
	}

	lease, err = store.AssignReservedIP(ctx, account.ID, lease.ID, app.ID, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Status != "pending" || lease.AppID != app.ID || lease.Generation != 1 {
		t.Fatalf("pending lease = %#v", lease)
	}
	lease, err = store.UpdateReservedIPStatus(ctx, account.ID, lease.ID, "assigned", "route ready", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Status != "assigned" || lease.StatusDetail != "route ready" || lease.Generation != 2 {
		t.Fatalf("assigned lease = %#v", lease)
	}
	// Repeating the same assignment is idempotent when the connector has
	// already converged on the requested node.
	if got, err := store.AssignReservedIP(ctx, account.ID, lease.ID, app.ID, "node-a"); err != nil || got.Status != "assigned" {
		t.Fatalf("idempotent assignment = %#v, %v", got, err)
	}
	lease, err = store.UpdateReservedIPStatus(ctx, account.ID, lease.ID, "error", "route unavailable", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Status != "error" {
		t.Fatalf("error lease = %#v", lease)
	}
	lease, err = store.UpdateReservedIPStatus(ctx, account.ID, lease.ID, "pending", "retrying", "node-b")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Status != "pending" || lease.NodeID != "node-b" {
		t.Fatalf("retry lease = %#v", lease)
	}
	if err := store.ReleaseReservedIP(ctx, account.ID, lease.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	released, err := store.GetReservedIP(ctx, account.ID, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != "available" || released.AppID != "" {
		t.Fatalf("released lease = %#v", released)
	}

	if _, err := store.GetReservedIP(ctx, uuid.NewString(), lease.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account Get error = %v, want not found", err)
	}
	if _, err := store.UpsertReservedIP(ctx, state.ReservedIP{AccountID: account.ID, Region: "fra1", Address: netip.MustParseAddr("10.0.0.8")}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("private address error = %v, want invalid argument", err)
	}
}
