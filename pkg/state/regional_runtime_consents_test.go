package state

// adr: 773
import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func runtimeConsentFixture(t *testing.T, s Store, quotas RegionalRuntimeQuotaStore, account Account, cell string) (regionalQuotaFixture, runtimequota.Consent) {
	t.Helper()
	if _, err := s.UpsertComputeRegion(t.Context(), ComputeRegion{ID: "eu-fsn", DisplayName: "Falkenstein", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	f := newRegionalQuotaFixture(t, s, quotas, account, cell)
	app, err := s.CreateApp(t.Context(), App{ID: uuid.NewString(), AccountID: account.ID, Slug: "consent-" + uuid.NewString()[:8], Type: AppTypeApp, Status: AppActive, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60, ComputeRegion: "eu-fsn"})
	if err != nil {
		t.Fatal(err)
	}
	f.grant.Binding.RegionalAppID, f.grant.Binding.RegionalAppSlug, f.grant.Binding.RegionalOrgID = app.ID, app.Slug, ""
	f.authority.Binding = f.grant.Binding
	c := runtimequota.Consent{ID: uuid.NewString(), IssuerID: f.grant.IssuerID, GlobalAppID: f.grant.GlobalAppID, OwnerID: f.grant.OwnerID, Binding: f.grant.Binding, ExpiresAtMicros: time.Now().Add(time.Hour).UnixMicro()}
	return f, c
}

func runRuntimeConsentContract(t *testing.T, s Store, q RegionalRuntimeQuotaStore, cs RegionalRuntimeConsentStore, account Account, cell string) {
	t.Helper()
	t.Run("original-source-revoke-renew-and-retirement", func(t *testing.T) {
		f, c := runtimeConsentFixture(t, s, q, account, cell)
		e := f.grantEnvelope(t, f.grant)
		if _, err := f.authority.AcceptDelegatedGrant(t.Context(), e, c.ID); !errors.Is(err, ErrRegionalRuntimeConsent) {
			t.Fatal("grant accepted without consent", err)
		}
		first, err := cs.PutRegionalRuntimeConsent(t.Context(), c)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := cs.PutRegionalRuntimeConsent(t.Context(), c)
		if err != nil || !retry.CreatedAt.Equal(first.CreatedAt) {
			t.Fatal("retry changed original consent", err)
		}
		r, err := f.authority.AcceptDelegatedGrant(t.Context(), e, c.ID)
		if err != nil || r.ConsentID != c.ID || r.Grant != f.grant || *r.GrantEnvelope != e {
			t.Fatal("delegated receipt lost original source", err)
		}
		for _, alter := range []func(*runtimequota.Consent){func(c *runtimequota.Consent) { c.OwnerID = uuid.NewString() }, func(c *runtimequota.Consent) { c.GlobalAppID = uuid.NewString() }, func(c *runtimequota.Consent) { c.ExpiresAtMicros++ }} {
			other := c
			alter(&other)
			if _, err := cs.PutRegionalRuntimeConsent(t.Context(), other); !errors.Is(err, ErrConflict) {
				t.Fatal("consent UUID reminted", err)
			}
		}
		for _, alter := range []func(*runtimequota.Grant){func(g *runtimequota.Grant) { g.OwnerID = uuid.NewString() }, func(g *runtimequota.Grant) { g.GlobalAppID = uuid.NewString() }, func(g *runtimequota.Grant) { g.Binding.CellGeneration++ }, func(g *runtimequota.Grant) { g.Binding.APIBase = "https://replacement.example.com" }} {
			wrong := f.grant
			wrong.AllocationID, wrong.Generation = uuid.NewString(), 2
			alter(&wrong)
			a := f.authority
			a.Binding = wrong.Binding
			if _, err := a.AcceptDelegatedGrant(t.Context(), f.grantEnvelope(t, wrong), c.ID); !errors.Is(err, ErrRegionalRuntimeConsent) {
				t.Fatal("foreign signed scope used consent", err)
			}
		}
		if _, err := cs.RegionalRuntimeConsent(t.Context(), uuid.NewString(), cell, c.Binding.RegionalAppID, c.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign owner read consent", err)
		}
		revoked, err := cs.RevokeRegionalRuntimeConsent(t.Context(), account.ID, cell, c.Binding.RegionalAppID, c.ID)
		if err != nil || revoked.RevokedAt == nil {
			t.Fatal("revoke failed", err)
		}
		retry, err = cs.PutRegionalRuntimeConsent(t.Context(), c)
		if err != nil || retry.RevokedAt == nil || !retry.RevokedAt.Equal(*revoked.RevokedAt) {
			t.Fatal("put resurrected revoked UUID", err)
		}
		if _, err = f.authority.AcceptDelegatedGrant(t.Context(), e, c.ID); !errors.Is(err, ErrRegionalRuntimeConsent) {
			t.Fatal("revoked consent accepted replay", err)
		}
		_, retirement := f.retirement(t, f.grant)
		if _, err = f.authority.AcceptRetirement(t.Context(), retirement); err != nil {
			t.Fatal("revocation blocked narrowing retirement", err)
		}
		renewed := c
		renewed.ID = uuid.NewString()
		if _, err = cs.PutRegionalRuntimeConsent(t.Context(), renewed); err != nil {
			t.Fatal(err)
		}
		next := f.grant
		next.AllocationID, next.Generation = uuid.NewString(), 2
		if _, err = f.authority.AcceptDelegatedGrant(t.Context(), f.grantEnvelope(t, next), renewed.ID); err != nil {
			t.Fatal("explicit new consent did not permit next allocation", err)
		}
		head, err := q.RegionalRuntimeQuotaHead(t.Context(), c.Binding.RegionalAppID)
		if err != nil || head.AdmissionEnabled {
			t.Fatal("consent activated quota capacity", err)
		}
		stored, err := q.RegionalRuntimeQuota(t.Context(), c.Binding.RegionalAppID, f.grant.AllocationID)
		if err != nil || stored.ConsentID != c.ID {
			t.Fatal("renewal replaced original receipt source", err)
		}
	})
	t.Run("expiry-and-collection", func(t *testing.T) {
		f, c := runtimeConsentFixture(t, s, q, account, cell)
		for _, expiry := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(api.RegionalRuntimeConsentMaxLifetime + time.Hour)} {
			bad := c
			bad.ExpiresAtMicros = expiry.UnixMicro()
			if _, err := cs.PutRegionalRuntimeConsent(t.Context(), bad); !errors.Is(err, ErrInvalidArgument) {
				t.Fatal("invalid consent lifetime accepted", err)
			}
		}
		if _, err := cs.PutRegionalRuntimeConsent(t.Context(), c); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteApp(t.Context(), c.Binding.RegionalAppID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.authority.AcceptDelegatedGrant(t.Context(), f.grantEnvelope(t, f.grant), c.ID); err == nil {
			t.Fatal("collected app admitted grant")
		}
		if _, err := cs.RevokeRegionalRuntimeConsent(t.Context(), account.ID, cell, c.Binding.RegionalAppID, c.ID); err != nil {
			t.Fatal("collection prevented original owner revocation", err)
		}
	})
	t.Run("concurrent-active-bound", func(t *testing.T) {
		_, c := runtimeConsentFixture(t, s, q, account, cell)
		var wg sync.WaitGroup
		errs := make(chan error, api.RegionalRuntimeConsentsPerAppMax+3)
		for range cap(errs) {
			wg.Go(func() {
				copy := c
				copy.ID = uuid.NewString()
				_, err := cs.PutRegionalRuntimeConsent(t.Context(), copy)
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		accepted, limited := 0, 0
		for err := range errs {
			var bound *RegionalRuntimeConsentLimitError
			switch {
			case err == nil:
				accepted++
			case errors.As(err, &bound):
				limited++
			default:
				t.Fatal(err)
			}
		}
		if accepted != api.RegionalRuntimeConsentsPerAppMax || limited != 3 {
			t.Fatalf("concurrent bound accepted=%d limited=%d", accepted, limited)
		}
	})
	t.Run("current-owner-and-tenant", func(t *testing.T) {
		owner, err := s.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@consent.example", Plan: api.PlanPro})
		if err != nil {
			t.Fatal(err)
		}
		f, c := runtimeConsentFixture(t, s, q, owner.Account, cell)
		if _, err := cs.PutRegionalRuntimeConsent(t.Context(), c); err != nil {
			t.Fatal(err)
		}
		envelope := f.grantEnvelope(t, f.grant)
		if _, err := f.authority.AcceptDelegatedGrant(t.Context(), envelope, c.ID); err != nil {
			t.Fatal(err)
		}
		for _, suspend := range []func() error{
			func() error { return s.UpdateAccountStatus(t.Context(), owner.Account.ID, AccountSuspended) },
			func() error { return s.UpdateOrgStatus(t.Context(), owner.PersonalOrg.ID, OrgStatusSuspended) },
		} {
			if err := suspend(); err != nil {
				t.Fatal(err)
			}
			if _, err := f.authority.AcceptDelegatedGrant(t.Context(), envelope, c.ID); !errors.Is(err, ErrRegionalRuntimeConsent) {
				t.Fatal("ineligible current owner or tenant accepted a replay", err)
			}
			if err := s.UpdateAccountStatus(t.Context(), owner.Account.ID, AccountActive); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestRegionalRuntimeConsentContract(t *testing.T) {
	s := NewMemStore()
	a, err := s.CreateAccount(t.Context(), "runtime-consent@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRuntimeConsentContract(t, s, s, s, a, uuid.NewString())
}
