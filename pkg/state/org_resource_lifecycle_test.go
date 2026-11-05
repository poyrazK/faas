package state_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreOrgResourceLifecycle(t *testing.T) {
	orgResourceLifecycleSuite(t, func(t *testing.T) state.Store { t.Helper(); return state.NewMemStore() })
}

// The in-memory twin of TestPostgresOrgResourceRehearsalFixtureBlocksErasureOnlyWhileLive.
func TestMemStoreRehearsalFixtureBlocksErasureOnlyWhileLive(t *testing.T) {
	for _, tc := range []struct {
		status  string
		blocked bool
	}{
		{"complete", false},
		{"deleted", false},
		{"provisioning", true},
		{"sealing", true},
	} {
		t.Run(tc.status, func(t *testing.T) {
			store := state.NewMemStore()
			f := setupOffboarding(t, store)
			ctx := t.Context()
			if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_rehearsal_guard"); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_rehearsal_guard"); err != nil {
				t.Fatal(err)
			}
			*store.ScenarioPostgres() = map[string]rehearsalpg.Resource{
				f.actor.ID + ":run": {ID: uuid.NewString(), AccountID: f.actor.ID, RunID: "run", AppID: f.app.ID, Status: tc.status},
			}
			if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
				t.Fatal(err)
			}
			err := store.MarkAccountDeletionPending(ctx, f.actor.ID)
			if blocked := errors.Is(err, state.ErrOrgAppOwnershipPending); blocked != tc.blocked || (!blocked && err != nil) {
				t.Fatalf("erasure with a %s fixture: %v, want blocked=%v", tc.status, err, tc.blocked)
			}
		})
	}
}

func orgResourceLifecycleSuite(t *testing.T, newStore func(*testing.T) state.Store) {
	t.Helper()
	t.Run("sole owner must delete or transfer the organization", func(t *testing.T) {
		store := newStore(t)
		ctx := t.Context()
		owner, err := store.CreateAccount(ctx, "sole-company-owner@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		org, err := store.CreateOrg(ctx, state.Org{Slug: "sole-company", Name: "Company", Plan: api.PlanFree})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkAccountDeletionPending(ctx, owner.ID); err == nil {
			t.Fatal("sole owner could schedule erasure of an active organization")
		}
		if err := store.SoftDeleteOrg(ctx, org.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkAccountDeletionPending(ctx, owner.ID); err != nil {
			t.Fatalf("deleted empty organization blocked erasure: %v", err)
		}
		if err := store.DeleteAccount(ctx, owner.ID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("creator erasure preserves company and erases personal resources", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		ctx := t.Context()
		if err := f.store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_company_lifecycle"); err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_company_lifecycle"); err != nil {
			t.Fatal(err)
		}
		personal, err := f.store.CreateApp(ctx, state.App{AccountID: f.actor.ID, Slug: "personal-erasure"})
		if err != nil {
			t.Fatal(err)
		}
		identity, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		ciphertext, err := secretbox.Seal(identity.Recipient(), secretbox.Envelope{"TOKEN": "test-organization-value"})
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range []state.App{f.app, personal} {
			if err := f.store.UpsertAppEnv(ctx, app.AccountID, app.ID, "REGION", "eu"); err != nil {
				t.Fatal(err)
			}
			if err := f.store.UpsertAppSecret(ctx, app.AccountID, app.ID, "TOKEN", ciphertext); err != nil {
				t.Fatal(err)
			}
		}
		dep, err := f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:0123456789abcdef"})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpdateAccountPlan(ctx, f.actor.ID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		owner, err := state.AppResourceAccount(ctx, f.store, f.app)
		if err != nil || owner.Plan != api.PlanPro || owner.ResourceOwnerID() != f.org.ID {
			t.Fatalf("company policy: %+v %v", owner, err)
		}
		if err := f.store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkAccountDeletionPending(ctx, f.actor.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.store.DeleteAccount(ctx, f.actor.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.AccountByID(ctx, f.actor.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("human remains: %v", err)
		}
		if _, err := f.store.AccountByID(ctx, f.org.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("org authenticates as human: %v", err)
		}
		if _, err := f.store.AppByID(ctx, personal.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("personal app remains: %v", err)
		}
		saved, err := f.store.AppByID(ctx, f.app.ID)
		if err != nil || saved.AccountID != f.app.AccountID || saved.OrgID != f.org.ID {
			t.Fatalf("company app changed: %+v %v", saved, err)
		}
		if _, err := f.store.DeploymentByID(ctx, dep.ID); err != nil {
			t.Fatalf("deployment erased: %v", err)
		}
		secret, err := f.store.GetAppSecret(ctx, f.app.AccountID, f.app.ID, "TOKEN")
		if err != nil || secret == nil || !bytes.Equal(secret.Ciphertext, ciphertext) {
			t.Fatalf("sealed company configuration changed: %v", err)
		}
		env, err := f.store.ListAppEnv(ctx, f.app.AccountID, f.app.ID)
		if err != nil || len(env) != 1 || env[0].Value != "eu" {
			t.Fatalf("company env: %+v %v", env, err)
		}
		if err := f.store.UpsertAppEnv(ctx, f.app.AccountID, f.app.ID, "REGION", "eu-west"); err != nil {
			t.Fatalf("company config depends on erased identity: %v", err)
		}
		owner, err = state.AppResourceAccount(ctx, f.store, saved)
		if err != nil || !owner.Active() || owner.Plan != api.PlanPro {
			t.Fatalf("runtime depends on erased identity: %+v %v", owner, err)
		}

		holds, ok := f.store.(state.AccountAbuseHoldStore)
		if !ok {
			t.Fatal("resource hold store unavailable")
		}
		if placed, err := holds.SetAccountAbuseHold(ctx, f.org.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil || !placed {
			t.Fatalf("company hold: %v %v", placed, err)
		}
		held, err := state.AppResourceAccount(ctx, f.store, saved)
		if err != nil || held.Active() {
			t.Fatalf("company hold missed runtime: %+v %v", held, err)
		}
		if released, err := holds.ReleaseAccountAbuseHold(ctx, f.org.ID); err != nil || !released {
			t.Fatalf("release company hold: %v %v", released, err)
		}
		minute := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
		if err := f.store.AppendUsage(ctx, saved.AccountID, saved.ID, uuid.NewString(), minute, 3600, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		usage, err := f.store.UsageByMonth(ctx, f.org.ID, minute)
		if err != nil || len(usage) != 1 || usage[0].MBSeconds != 3600 {
			t.Fatalf("company usage attribution: %+v %v", usage, err)
		}
		if _, err := f.store.ScheduleAppDeletion(ctx, saved.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.RestoreApp(ctx, saved.ID, api.MustLimitsFor(api.PlanFree)); err != nil {
			t.Fatalf("restore depends on erased creator: %v", err)
		}
	})
	t.Run("sole owner must transfer even without other members", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		if err := f.store.RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkAccountDeletionPending(t.Context(), f.owner.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
			t.Fatalf("owner deletion permitted: %v", err)
		}
	})
}

func orgResourceBillingAdoption(t *testing.T, store state.Store) {
	t.Helper()
	f := setupOffboarding(t, store)
	ctx := t.Context()
	if err := store.UpdateAccountProviderCustomerID(ctx, f.actor.ID, "cus_legacy_personal"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.actor.ID, "si_legacy_personal"); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	instance := uuid.NewString()
	if err := store.AppendUsage(ctx, f.actor.ID, f.app.ID, instance, before, 1200, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveOrgMember(ctx, f.org.ID, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
		t.Fatalf("unconfigured company billing permitted erasure: %v", err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, f.org.ID, "cus_owned_company"); err != nil {
		t.Fatal(err)
	}
	partial, err := store.OrgByID(ctx, f.org.ID)
	if err != nil || partial.ResourceOwnershipEnabled {
		t.Fatalf("customer alone activated ownership: %+v %v", partial, err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, "si_owned_company"); err != nil {
		t.Fatal(err)
	}
	active, err := store.OrgByID(ctx, f.org.ID)
	if err != nil || !active.ResourceOwnershipEnabled {
		t.Fatalf("subscription did not activate ownership: %+v %v", active, err)
	}
	subject, err := store.AccountByBillingCustomerID(ctx, "stripe", "cus_owned_company")
	if err != nil || subject.ID != f.org.ID || subject.ResourceOrgID != f.org.ID {
		t.Fatalf("company provider identity: %+v %v", subject, err)
	}
	personal, err := store.AccountByID(ctx, f.actor.ID)
	if err != nil || personal.ProviderCustomerID != "cus_legacy_personal" || personal.StripeSubscriptionItem != "si_legacy_personal" {
		t.Fatalf("personal billing identity changed: %+v %v", personal, err)
	}
	// A replay spanning adoption must retain its first financial subject.
	if err := store.AppendUsage(ctx, f.actor.ID, f.app.ID, instance, before, 1200, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, f.actor.ID, f.app.ID, instance, before.Add(time.Minute), 2400, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	old, err := store.UsageByMonth(ctx, f.actor.ID, before)
	if err != nil || len(old) != 1 || old[0].MBSeconds != 1200 {
		t.Fatalf("historical attribution moved: %+v %v", old, err)
	}
	owned, err := store.UsageByMonth(ctx, f.org.ID, before)
	if err != nil || len(owned) != 1 || owned[0].MBSeconds != 2400 {
		t.Fatalf("future attribution: %+v %v", owned, err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, f.org.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, f.org.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	canceled, err := store.OrgByID(ctx, f.org.ID)
	if err != nil || !canceled.ResourceOwnershipEnabled {
		t.Fatalf("cancellation restored creator ownership: %+v %v", canceled, err)
	}
	if err := store.MarkAccountDeletionPending(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	old, err = store.UsageByMonth(ctx, f.actor.ID, before)
	if err != nil || len(old) != 1 || old[0].MBSeconds != 1200 {
		t.Fatalf("company history erased with creator: %+v %v", old, err)
	}
	owner, err := state.AppResourceAccount(ctx, store, f.app)
	if err != nil || owner.ResourceOwnerID() != f.org.ID || owner.Plan != api.PlanFree {
		t.Fatalf("canceled company depends on creator: %+v %v", owner, err)
	}
}

func TestMemStoreOrgResourceBillingAdoption(t *testing.T) {
	orgResourceBillingAdoption(t, state.NewMemStore())
}
