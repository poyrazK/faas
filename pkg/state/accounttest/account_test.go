package accounttest

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func stores(t *testing.T, test func(*testing.T, state.Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { test(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { test(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func account(t *testing.T, store state.Store) state.Account {
	t.Helper()
	result, err := store.CreateAccountWithPersonalOrg(t.Context(), state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.com", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	return result.Account
}

func TestBillingPatchPreservesConcurrentFields(t *testing.T) {
	stores(t, func(t *testing.T, store state.Store) {
		for _, subject := range []string{"personal", "organization"} {
			t.Run(subject, func(t *testing.T) {
				id := account(t, store).ID
				if subject == "organization" {
					org, err := store.CreateOrg(t.Context(), state.Org{Slug: "billing-" + uuid.NewString()[:8], Name: "Billing", Plan: api.PlanPro})
					if err != nil {
						t.Fatal(err)
					}
					id = org.ID
				}
				patcher := store.(state.AccountBillingInfoPatchStore)
				name, address, tax := "Company", "Street", "TAX"
				start := make(chan struct{})
				errs := make(chan error, 3)
				var wg sync.WaitGroup
				for _, patch := range []state.AccountBillingInfoPatch{{BusinessName: &name}, {BillingAddress: &address}, {TaxID: &tax}} {
					wg.Go(func() {
						<-start
						_, err := patcher.PatchAccountBillingInfo(t.Context(), id, patch)
						errs <- err
					})
				}
				close(start)
				wg.Wait()
				close(errs)
				for err := range errs {
					if err != nil {
						t.Fatal(err)
					}
				}
				// A subsequent partial clear must preserve the other committed fields.
				empty := ""
				got, err := patcher.PatchAccountBillingInfo(t.Context(), id, state.AccountBillingInfoPatch{TaxID: &empty})
				if err != nil || got.BusinessName != name || got.BillingAddress != address || got.TaxID != "" {
					t.Fatalf("disjoint updates or explicit clear lost: %+v %v", got, err)
				}
				if _, err := patcher.PatchAccountBillingInfo(t.Context(), uuid.NewString(), state.AccountBillingInfoPatch{TaxID: &tax}); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("missing subject did not return not found", err)
				}
			})
		}
	})
}

func incoming(t *testing.T, store state.Store, recipient state.Account) (state.Org, state.Account) {
	t.Helper()
	owner := account(t, store)
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "incoming-" + uuid.NewString()[:8], Name: "Incoming", Plan: api.PlanHobby})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		id   string
		role state.OrgRole
	}{{owner.ID, state.OrgRoleOwner}, {recipient.ID, state.OrgRoleViewer}} {
		if err := store.AddOrgMember(t.Context(), org.ID, member.id, member.role, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Existing members remain after the paid plan ends.
	if err := store.UpdateOrgPlan(t.Context(), org.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	return org, owner
}

func ownershipActivity(org state.Org, owner state.Account) state.OrgActivity {
	actor := uuid.MustParse(owner.ID)
	return state.OrgActivity{OrgID: uuid.MustParse(org.ID), Kind: "org.ownership_transferred", ActorType: state.OrgActivityActorUser,
		ActorAccountID: &actor, ActorLabel: owner.Email, ResourceType: "member", ResourceLabel: "member", SourceType: "org.ownership_transferred", SourceID: uuid.NewString(), Data: []byte(`{}`)}
}

func transfer(t *testing.T, store state.Store, org state.Org, from, to state.Account, activity bool) error {
	t.Helper()
	if activity {
		_, err := store.(state.OrgActivityOwnershipTransferMutationStore).TransferOrgOwnershipWithActivity(t.Context(), org.ID, from.ID, to.ID, ownershipActivity(org, from))
		return err
	}
	return store.TransferOrgOwnership(t.Context(), org.ID, from.ID, to.ID)
}

func ownedFree(t *testing.T, store state.Store, owner state.Account) int {
	t.Helper()
	orgs, err := store.ListOrgsForAccount(t.Context(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, org := range orgs {
		member, err := store.OrgMemberByAccount(t.Context(), org.ID, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !org.Personal && !org.DeletedPending && org.Plan == api.PlanFree && member.RemovedAt == nil && member.Role == state.OrgRoleOwner {
			count++
		}
	}
	return count
}

func TestFreeOwnershipTransferAdmission(t *testing.T) {
	stores(t, func(t *testing.T, store state.Store) {
		for _, activity := range []bool{false, true} {
			recipient := account(t, store)
			creation := store.(state.SharedOrgCreationStore)
			if _, err := creation.CreateSharedOrg(t.Context(), recipient.ID, "owned-"+uuid.NewString()[:8], "Owned"); err != nil {
				t.Fatal(err)
			}
			org, owner := incoming(t, store, recipient)
			var limit *state.OrgFreeLimitError
			if err := transfer(t, store, org, owner, recipient, activity); !errors.As(err, &limit) || limit.Owned != api.FreeOrganizationsOwnedMax {
				t.Fatal("Free ownership transfer ignored recipient capacity", err)
			}
			for _, member := range []struct {
				id   string
				role state.OrgRole
			}{{owner.ID, state.OrgRoleOwner}, {recipient.ID, state.OrgRoleViewer}} {
				got, err := store.OrgMemberByAccount(t.Context(), org.ID, member.id)
				if err != nil || got.Role != member.role {
					t.Fatal("rejected transfer mutated membership", err)
				}
			}
			rows, err := store.(state.OrgActivityStore).ListOrgActivity(t.Context(), state.OrgActivityFilter{OrgID: uuid.MustParse(org.ID), Limit: 10})
			if err != nil || len(rows) != 0 {
				t.Fatal("rejected transfer published activity", err)
			}
			// Paid transfers consume no Free slot.
			if err := store.UpdateOrgPlan(t.Context(), org.ID, api.PlanHobby); err != nil {
				t.Fatal(err)
			}
			if err := transfer(t, store, org, owner, recipient, activity); err != nil {
				t.Fatal("paid transfer was blocked", err)
			}
			if got := ownedFree(t, store, recipient); got != api.FreeOrganizationsOwnedMax {
				t.Fatal("paid transfer changed Free usage", got)
			}
		}
	})
}

func TestFreeOwnershipAdmissionRaces(t *testing.T) {
	stores(t, func(t *testing.T, store state.Store) {
		for _, mode := range []string{"creation", "transfer"} {
			t.Run(mode, func(t *testing.T) {
				recipient := account(t, store)
				org, owner := incoming(t, store, recipient)
				otherOrg, otherOwner := incoming(t, store, recipient)
				start := make(chan struct{})
				results := make(chan error, 2)
				var wg sync.WaitGroup
				wg.Go(func() { <-start; results <- transfer(t, store, org, owner, recipient, true) })
				wg.Go(func() {
					<-start
					if mode == "creation" {
						_, err := store.(state.SharedOrgCreationStore).CreateSharedOrg(t.Context(), recipient.ID, "racing-"+uuid.NewString()[:8], "Racing")
						results <- err
					} else {
						results <- transfer(t, store, otherOrg, otherOwner, recipient, false)
					}
				})
				close(start)
				wg.Wait()
				close(results)
				wins := 0
				for err := range results {
					if err == nil {
						wins++
						continue
					}
					var limit *state.OrgFreeLimitError
					if !errors.As(err, &limit) {
						t.Fatal("unexpected contention error", err)
					}
				}
				if wins != 1 || ownedFree(t, store, recipient) != api.FreeOrganizationsOwnedMax {
					t.Fatal("creation and transfer did not share capacity", wins)
				}
			})
		}
	})
}
