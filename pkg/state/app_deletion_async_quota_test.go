package state_test

// adr: 202610171908

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// appDeletionReleasesPooledAsyncQuota deletes a company app while one of its
// deliveries holds a slot on the company's pooled async counter. The
// cancelled delivery's slot goes back to the company; the creator's own
// counter, which holds a slot for a personal app, is left alone.
func appDeletionReleasesPooledAsyncQuota(t *testing.T, newStore func(*testing.T) state.Store) {
	t.Helper()
	for _, tc := range []struct {
		name   string
		delete func(state.Store, string) error
	}{
		{"soft delete", func(store state.Store, id string) error {
			_, err := store.SoftDeleteAppCascade(t.Context(), id)
			return err
		}},
		{"scheduled deletion", func(store state.Store, id string) error {
			_, err := store.ScheduleAppDeletion(t.Context(), id, time.Now().Add(time.Hour))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, base := t.Context(), newStore(t)
			f := setupOffboarding(t, base)
			adoptObjectCompany(t, f)
			poolCompanyAsyncQuota(t, f)
			const maxInflight = 10
			claim := func(app state.App) state.Invocation {
				t.Helper()
				inv, err := base.EnqueueInvocation(ctx, state.Invocation{AccountID: app.AccountID, AppID: app.ID,
					Source: state.InvocationAsyncInvoke, DueAt: time.Now().Add(-time.Second)})
				if err != nil {
					t.Fatal(err)
				}
				claimed, err := base.ClaimInvocationWithCap(ctx, inv.ID, "", 30, maxInflight)
				if err != nil {
					t.Fatal(err)
				}
				return claimed
			}
			inflight := func(subject string) int {
				t.Helper()
				_, current, err := base.EnsureAccountAsyncQuota(ctx, subject, maxInflight)
				if err != nil {
					t.Fatal(err)
				}
				return current
			}
			company, err := base.CreateApp(ctx, state.App{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: "company-async",
				Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			personal, err := base.CreateApp(ctx, state.App{AccountID: f.actor.ID, Slug: "personal-async",
				Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
			if err != nil {
				t.Fatal(err)
			}
			if got := claim(company); got.AsyncQuotaSubjectID != f.org.ID {
				t.Fatalf("company delivery reserved on %q, want the company's pooled counter %s", got.AsyncQuotaSubjectID, f.org.ID)
			}
			claim(personal)
			companyBefore, creatorBefore := inflight(f.org.ID), inflight(f.actor.ID)
			if err := tc.delete(base, company.ID); err != nil {
				t.Fatal(err)
			}
			if got := inflight(f.org.ID); got != companyBefore-1 {
				t.Fatalf("company in-flight = %d after deleting its app, want %d: the cancelled delivery's slot leaked", got, companyBefore-1)
			}
			if got := inflight(f.actor.ID); got != creatorBefore {
				t.Fatalf("creator in-flight = %d after deleting the company app, want %d", got, creatorBefore)
			}
		})
	}
}

func TestMemAppDeletionReleasesPooledAsyncQuota(t *testing.T) {
	appDeletionReleasesPooledAsyncQuota(t, func(*testing.T) state.Store { return state.NewMemStore() })
}
