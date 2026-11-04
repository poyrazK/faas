package state_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomDomainInventoryIncludesActiveCompanyMemberships(t *testing.T) {
	for name, open := range tlsHostStores() {
		t.Run(name, func(t *testing.T) {
			raw, ctx := open(t)
			store := raw.(state.Store)
			inventory := raw.(state.CustomDomainInventoryStore)
			creatorApp := tlsHostApp(t, ctx, raw, "")
			creator, err := store.AppByID(ctx, creatorApp)
			if err != nil {
				t.Fatal(err)
			}
			suffix := uuid.NewString()[:8]
			actor, err := store.CreateAccount(ctx, "inventory-"+suffix+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			org, err := store.CreateOrg(ctx, state.Org{Slug: "inventory-" + suffix, Name: "Inventory", Plan: api.PlanPro})
			if err != nil {
				t.Fatal(err)
			}
			for account, role := range map[string]state.OrgRole{creator.AccountID: state.OrgRoleOwner, actor.ID: state.OrgRoleViewer} {
				if err := store.AddOrgMember(ctx, org.ID, account, role, nil); err != nil {
					t.Fatal(err)
				}
			}
			company, err := store.CreateApp(ctx, state.App{AccountID: creator.AccountID, OrgID: org.ID, Slug: "company-" + suffix, Type: state.AppTypeApp, RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			shared := "shared-" + suffix + ".example.com"
			for domain, app := range map[string]string{shared: company.ID, "private-" + suffix + ".example.com": creatorApp} {
				if _, err := store.CreateCustomDomain(ctx, domain, app, "proof"); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := inventory.ListDomainsForActor(ctx, actor.ID)
			if err != nil || len(rows) != 1 || rows[0].Domain != shared || rows[0].Verified() {
				t.Fatalf("collaborator inventory = %+v, %v", rows, err)
			}
			// Creator-owned accounting inventories keep their original meaning.
			if rows, err := store.ListDomainsForAccount(ctx, actor.ID); err != nil || len(rows) != 0 {
				t.Fatalf("accounting inventory changed = %+v, %v", rows, err)
			}
			if err := store.RemoveOrgMember(ctx, org.ID, actor.ID); err != nil {
				t.Fatal(err)
			}
			if rows, err := inventory.ListDomainsForActor(ctx, actor.ID); err != nil || len(rows) != 0 {
				t.Fatalf("removed collaborator inventory = %+v, %v", rows, err)
			}
		})
	}
}
