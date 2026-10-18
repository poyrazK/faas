package state_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreOrgAppQuota(t *testing.T) {
	orgAppQuotaSuite(t, func(t *testing.T) state.Store {
		t.Helper()
		return state.NewMemStore()
	})
}

func orgAppQuotaSuite(t *testing.T, newStore func(*testing.T) state.Store) {
	t.Helper()
	t.Run("shared pool and personal isolation", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		if err := f.store.UpdateOrgPlan(t.Context(), f.org.ID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		fillOrgAppQuota(t, f)
		_, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.owner.ID, OrgID: f.org.ID, Slug: "over-company-cap"}, api.MustLimitsFor(api.PlanScale))
		assertOrgAppQuota(t, err, api.PlanHobby, api.MustLimitsFor(api.PlanHobby).DeployedApps, state.QuotaErrorKindApps)
		if err := f.store.UpdateAccountPlan(t.Context(), f.actor.ID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		// Company apps, including those created by this account, cannot consume
		// the one personal Free slot. Unattributed callers resolve to personal.
		personal, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.actor.ID, Slug: "one-personal-slot"}, api.MustLimitsFor(api.PlanFree))
		if err != nil || personal.OrgID == "" || personal.OrgID == f.org.ID {
			t.Fatalf("personal slot = %+v, %v", personal, err)
		}
		_, err = f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.actor.ID, Slug: "second-personal-slot"}, api.MustLimitsFor(api.PlanFree))
		assertOrgAppQuota(t, err, api.PlanFree, 1, state.QuotaErrorKindApps)
		for _, developer := range []state.App{
			{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: "company-dev", PreviewOfSlug: f.app.Slug},
			{AccountID: f.actor.ID, Slug: "personal-dev", PreviewOfSlug: personal.Slug},
		} {
			if _, err := f.store.CreateAppIfUnderQuota(t.Context(), developer, api.MustLimitsFor(api.PlanFree)); err != nil {
				t.Fatal(err)
			}
		}
		if count, err := f.store.CountDeployedApps(t.Context(), f.actor.ID); err != nil || count != 1 {
			t.Fatalf("personal preflight/display count includes company apps: %d %v", count, err)
		}
		if count, err := f.store.CountDeveloperApps(t.Context(), f.actor.ID); err != nil || count != 1 {
			t.Fatalf("personal development count includes company apps: %d %v", count, err)
		}
		other, err := f.store.CreateOrg(t.Context(), state.Org{Slug: "quota-other", Name: "Other", Plan: api.PlanHobby})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.actor.ID, OrgID: other.ID, Slug: "another-company"}, api.MustLimitsFor(api.PlanFree)); err != nil {
			t.Fatalf("other company shares a budget: %v", err)
		}
	})
	t.Run("create and restore compete for the same last slot", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		if err := f.store.UpdateOrgPlan(t.Context(), f.org.ID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		fillOrgAppQuota(t, f)
		if _, err := f.store.ScheduleAppDeletion(t.Context(), f.app.ID, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var done sync.WaitGroup
		done.Add(2)
		go func() {
			defer done.Done()
			<-start
			_, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.owner.ID, OrgID: f.org.ID, Slug: "last-slot-new"}, api.MustLimitsFor(api.PlanScale))
			results <- err
		}()
		go func() {
			defer done.Done()
			<-start
			_, err := f.store.RestoreApp(t.Context(), f.app.ID, api.MustLimitsFor(api.PlanScale))
			results <- err
		}()
		close(start)
		done.Wait()
		close(results)
		succeeded, denied := 0, 0
		for err := range results {
			if err == nil {
				succeeded++
			} else if errors.Is(err, state.ErrQuotaExceeded) {
				denied++
			} else {
				t.Fatalf("admission failed unexpectedly: %v", err)
			}
		}
		if succeeded != 1 || denied != 1 {
			t.Fatalf("last slot: %d succeeded, %d denied", succeeded, denied)
		}
	})
	t.Run("different members race for one organization budget", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		if err := f.store.UpdateOrgPlan(t.Context(), f.org.ID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 12)
		var done sync.WaitGroup
		for i := range 12 {
			done.Go(func() {
				<-start
				accountID := f.owner.ID
				if i%2 == 1 {
					accountID = f.actor.ID
				}
				_, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: accountID, OrgID: f.org.ID, Slug: fmt.Sprintf("raced-company-%d", i)}, api.MustLimitsFor(api.PlanScale))
				results <- err
			})
		}
		close(start)
		done.Wait()
		close(results)
		succeeded := 1 // setupOffboarding already created one company app.
		for err := range results {
			if err == nil {
				succeeded++
			} else if !errors.Is(err, state.ErrQuotaExceeded) {
				t.Fatalf("concurrent create: %v", err)
			}
		}
		if succeeded != api.MustLimitsFor(api.PlanHobby).DeployedApps {
			t.Fatalf("organization admitted %d apps", succeeded)
		}
	})
	t.Run("preview and developer pools remain distinct", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		if err := f.store.UpdateOrgPlan(t.Context(), f.org.ID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		limits := api.MustLimitsFor(api.PlanHobby)
		for _, pool := range []struct {
			kind  state.QuotaErrorKind
			limit int
			pr    int
		}{{state.QuotaErrorKindDeveloperApps, limits.DeveloperApps, 0}, {state.QuotaErrorKindPreviewApps, limits.PreviewApps, 42}} {
			for i := range pool.limit {
				if _, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: fmt.Sprintf("%s-%d", strings.ReplaceAll(string(pool.kind), "_", "-"), i), PreviewOfSlug: f.app.Slug, PreviewPrNumber: pool.pr}, api.MustLimitsFor(api.PlanScale)); err != nil {
					t.Fatalf("%s admission: %v", pool.kind, err)
				}
			}
			_, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.owner.ID, OrgID: f.org.ID, Slug: strings.ReplaceAll(string(pool.kind), "_", "-") + "-excess", PreviewOfSlug: f.app.Slug, PreviewPrNumber: pool.pr}, api.MustLimitsFor(api.PlanScale))
			assertOrgAppQuota(t, err, api.PlanHobby, pool.limit, pool.kind)
		}
		if _, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: "production-still-has-room"}, api.MustLimitsFor(api.PlanFree)); err != nil {
			t.Fatalf("non-production apps consumed production quota: %v", err)
		}
	})
	t.Run("blocked erasure preserves company state", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		if err := f.store.RemoveOrgMember(t.Context(), f.org.ID, f.actor.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.store.UpsertAppEnvInScope(t.Context(), f.actor.ID, f.app.ID, "default", "COMPANY", "retained"); err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkAccountDeletionPending(t.Context(), f.actor.ID); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
			t.Fatalf("scheduled erasure with company storage reference: %v", err)
		}
		// Generic status writers must preserve the same barrier. A direct
		// purge must also fail without changing company resources.
		if err := f.store.UpdateAccountStatus(t.Context(), f.actor.ID, state.AccountDeletedPending); !errors.Is(err, state.ErrOrgAppOwnershipPending) {
			t.Fatalf("status writer scheduled unsupported company erasure: %v", err)
		}
		if err := f.store.DeleteAccount(t.Context(), f.actor.ID); err == nil {
			t.Fatalf("purged company storage reference: %v", err)
		}
		account, err := f.store.AccountByID(t.Context(), f.actor.ID)
		if err != nil || account.Status != state.AccountActive {
			t.Fatalf("blocked erasure changed the identity: %+v %v", account, err)
		}
		if _, err := f.store.AppByID(t.Context(), f.app.ID); err != nil {
			t.Fatalf("company app erased: %v", err)
		}
		values, err := f.store.ListAppEnvInScope(t.Context(), f.actor.ID, f.app.ID, "default")
		if err != nil || len(values) != 1 || values[0].Value != "retained" {
			t.Fatalf("company configuration erased: %+v %v", values, err)
		}
	})
	t.Run("inactive owners cannot admit new company apps", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		if err := f.store.UpdateOrgStatus(t.Context(), f.org.ID, state.OrgStatusSuspended); err != nil {
			t.Fatal(err)
		}
		_, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.actor.ID, OrgID: f.org.ID, Slug: "inactive-company"}, api.MustLimitsFor(api.PlanScale))
		if !errors.Is(err, state.ErrOrgAppAdmissionUnavailable) {
			t.Fatalf("inactive organization admitted app: %v", err)
		}
		if err := f.store.UpdateOrgStatus(t.Context(), f.org.ID, state.OrgStatusActive); err != nil {
			t.Fatal(err)
		}
		newCreator, err := f.store.CreateAccount(t.Context(), "pending-creator@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.AddOrgMember(t.Context(), f.org.ID, newCreator.ID, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkAccountDeletionPending(t.Context(), newCreator.ID); err != nil {
			t.Fatal(err)
		}
		_, err = f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: newCreator.ID, OrgID: f.org.ID, Slug: "pending-creator-company"}, api.MustLimitsFor(api.PlanScale))
		if !errors.Is(err, state.ErrOrgAppAdmissionUnavailable) {
			t.Fatalf("pending creator admitted a new company storage reference: %v", err)
		}
	})
	t.Run("personal organization must match storage account", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		personal, err := f.store.OrgByPersonalAccount(t.Context(), f.owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.actor.ID, OrgID: personal.ID, Slug: "foreign-personal-quota"}, api.MustLimitsFor(api.PlanScale))
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("foreign personal organization admitted app: %v", err)
		}
	})
	t.Run("project provisioning uses the personal pool", func(t *testing.T) {
		f := setupOffboarding(t, newStore(t))
		if err := f.store.UpdateOrgPlan(t.Context(), f.org.ID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		fillOrgAppQuota(t, f)
		if err := f.store.UpdateAccountPlan(t.Context(), f.owner.ID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		_, apps, _, err := f.store.ApplyProjectPlan(t.Context(), state.Project{AccountID: f.owner.ID, Slug: "personal-project"}, []state.App{{Slug: "personal-project-web", WorkloadName: "web", WorkloadClass: state.WorkloadClassHTTP}}, nil, api.MustLimitsFor(api.PlanFree))
		if err != nil || len(apps) != 1 {
			t.Fatalf("company apps consumed project slots: %+v %v", apps, err)
		}
		_, err = f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: f.owner.ID, Slug: "after-personal-project"}, api.MustLimitsFor(api.PlanFree))
		assertOrgAppQuota(t, err, api.PlanFree, 1, state.QuotaErrorKindApps)
	})
}

func fillOrgAppQuota(t *testing.T, f offboardingFixture) {
	t.Helper()
	limit := api.MustLimitsFor(api.PlanHobby).DeployedApps
	for i := 1; i < limit; i++ {
		accountID := f.owner.ID
		if i%2 == 1 {
			accountID = f.actor.ID
		}
		if _, err := f.store.CreateAppIfUnderQuota(t.Context(), state.App{AccountID: accountID, OrgID: f.org.ID, Slug: fmt.Sprintf("company-%d", i)}, api.MustLimitsFor(api.PlanScale)); err != nil {
			t.Fatal(err)
		}
	}
}

func assertOrgAppQuota(t *testing.T, err error, plan api.Plan, limit int, kind state.QuotaErrorKind) {
	t.Helper()
	var quota *state.QuotaError
	if !errors.As(err, &quota) || quota.Plan != plan || quota.Limit != limit || quota.Observed != limit || quota.Kind != kind {
		t.Fatalf("quota = %+v, want plan=%s limit=%d observed=%d kind=%s", err, plan, limit, limit, kind)
	}
}
