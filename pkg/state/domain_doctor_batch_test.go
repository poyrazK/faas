package state

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainDoctorBatchInterleavesAccounts(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	large, err := store.CreateAccount(ctx, "doctor-large@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	small, err := store.CreateAccount(ctx, "doctor-small@example.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	largeApp, err := store.CreateApp(ctx, App{AccountID: large.ID, Slug: "doctor-large"})
	if err != nil {
		t.Fatal(err)
	}
	smallApp, err := store.CreateApp(ctx, App{AccountID: small.ID, Slug: "doctor-small"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		domain := fmt.Sprintf("large-%02d.example.test", i)
		if _, err := store.CreateCustomDomain(ctx, domain, largeApp.ID, "token"); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDomainVerified(ctx, domain); err != nil {
			t.Fatal(err)
		}
	}
	const smallDomain = "small.example.test"
	if _, err := store.CreateCustomDomain(ctx, smallDomain, smallApp.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, smallDomain); err != nil {
		t.Fatal(err)
	}

	batch, err := store.ListCustomDomainsForDoctorBatch(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 2 || !slices.Contains(batch, smallDomain) {
		t.Fatalf("batch = %v; small account was starved by large account", batch)
	}
}
