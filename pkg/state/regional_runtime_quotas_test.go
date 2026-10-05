package state

// adr: 614
import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

type regionalQuotaFixture struct {
	authority RegionalRuntimeQuotaAuthority
	signer    runtimequota.Signer
	grant     runtimequota.Grant
}

func newRegionalQuotaFixture(t *testing.T, s Store, quota RegionalRuntimeQuotaStore, account Account, cell string) regionalQuotaFixture {
	t.Helper()
	app, err := s.CreateApp(t.Context(), App{ID: uuid.NewString(), AccountID: account.ID,
		Slug: "quota-" + uuid.NewString()[:8], Type: AppTypeApp, Status: AppActive, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuer := uuid.NewString()
	signer, err := runtimequota.NewSigner(issuer, private)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: issuer, Public: pub}})
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := api.RuntimeQuotaPolicyForPlan(api.PlanPro)
	binding := runtimequota.Binding{CellID: cell, Region: "eu-fsn", CellGeneration: 1, RegionalAccountID: regionalQuotaCanonicalID(app.AccountID), RegionalOrgID: regionalQuotaCanonicalID(app.OrgID), RegionalAppID: app.ID, RegionalAppSlug: app.Slug, APIBase: "https://fsn.example.com"}
	grant := runtimequota.Grant{IssuerID: issuer, GlobalAppID: uuid.NewString(), OwnerID: uuid.NewString(), AllocationID: uuid.NewString(), Generation: 1, Policy: policy, Concurrency: 2, RolloutConcurrency: api.RolloutConcurrencyGrant, IssuedAtMicros: time.Now().UnixMicro(), Binding: binding}
	if grant.Validate() != nil {
		t.Fatalf("invalid regional quota fixture: %+v", grant)
	}
	return regionalQuotaFixture{authority: RegionalRuntimeQuotaAuthority{Store: quota, Directories: trust, Binding: binding}, signer: signer, grant: grant}
}
func (f regionalQuotaFixture) grantEnvelope(t *testing.T, grant runtimequota.Grant) runtimequota.Envelope {
	t.Helper()
	e, err := f.signer.SignGrant(grant)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func (f regionalQuotaFixture) retirement(t *testing.T, grant runtimequota.Grant) (runtimequota.Retirement, runtimequota.Envelope) {
	t.Helper()
	r := runtimequota.Retirement{Grant: grant, RetirementID: uuid.NewString(), RetiredAtMicros: grant.IssuedAtMicros - 1}
	e, err := f.signer.SignRetirement(r)
	if err != nil {
		t.Fatal(err)
	}
	return r, e
}

func runRegionalRuntimeQuotaContract(t *testing.T, s Store, quota RegionalRuntimeQuotaStore, account Account, cell string) {
	t.Helper()
	fixture := func(t *testing.T) regionalQuotaFixture { return newRegionalQuotaFixture(t, s, quota, account, cell) }
	t.Run("lost-response-and-copied-snapshots", func(t *testing.T) {
		f := fixture(t)
		envelope := f.grantEnvelope(t, f.grant)
		first, err := f.authority.AcceptGrant(t.Context(), envelope)
		if err != nil || first.Grant != f.grant || first.GrantEnvelope == nil || *first.GrantEnvelope != envelope || first.Retirement != nil {
			t.Fatalf("accept: %+v %v", first, err)
		}
		first.GrantEnvelope.Payload = "caller-mutated"
		retry, err := f.authority.AcceptGrant(t.Context(), envelope)
		if err != nil || !retry.CreatedAt.Equal(first.CreatedAt) || *retry.GrantEnvelope != envelope {
			t.Fatalf("retry: %+v %v", retry, err)
		}
		head, err := quota.RegionalRuntimeQuotaHead(t.Context(), f.grant.Binding.RegionalAppID)
		if err != nil || head.Grant != f.grant || head.AdmissionEnabled {
			t.Fatalf("head: %+v %v", head, err)
		}
	})
	t.Run("retirement-before-grant", func(t *testing.T) {
		f := fixture(t)
		retirement, envelope := f.retirement(t, f.grant)
		first, err := f.authority.AcceptRetirement(t.Context(), envelope)
		if err != nil || first.GrantEnvelope != nil || first.Retirement == nil || *first.Retirement != retirement {
			t.Fatalf("tombstone: %+v %v", first, err)
		}
		first.Retirement.RetirementID = uuid.NewString()
		first.RetirementEnvelope.Payload = "caller-mutated"
		later, err := f.authority.AcceptGrant(t.Context(), f.grantEnvelope(t, f.grant))
		if err != nil || later.GrantEnvelope != nil || later.Retirement == nil || *later.Retirement != retirement || *later.RetirementEnvelope != envelope || !later.CreatedAt.Equal(first.CreatedAt) {
			t.Fatalf("reopened retirement: %+v %v", later, err)
		}
		changed := retirement
		changed.RetirementID = uuid.NewString()
		conflict, err := f.signer.SignRetirement(changed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.authority.AcceptRetirement(t.Context(), conflict); !errors.Is(err, ErrConflict) {
			t.Fatalf("retirement rebound: %v", err)
		}
	})
	t.Run("grant-before-retirement", func(t *testing.T) {
		f := fixture(t)
		grantEnvelope := f.grantEnvelope(t, f.grant)
		first, err := f.authority.AcceptGrant(t.Context(), grantEnvelope)
		if err != nil {
			t.Fatal(err)
		}
		retirement, envelope := f.retirement(t, f.grant)
		retired, err := f.authority.AcceptRetirement(t.Context(), envelope)
		if err != nil || retired.Retirement == nil || *retired.Retirement != retirement || *retired.GrantEnvelope != grantEnvelope || !retired.CreatedAt.Equal(first.CreatedAt) {
			t.Fatalf("retire: %+v %v", retired, err)
		}
		retry, err := f.authority.AcceptGrant(t.Context(), grantEnvelope)
		if err != nil || retry.Retirement == nil || *retry.Retirement != retirement {
			t.Fatalf("replay revived grant: %+v %v", retry, err)
		}
		changed := f.grant
		changed.Concurrency++
		if _, err = f.authority.AcceptGrant(t.Context(), f.grantEnvelope(t, changed)); !errors.Is(err, ErrConflict) {
			t.Fatalf("original grant mutated: %v", err)
		}
	})
	t.Run("older-tombstone-keeps-newer-head", func(t *testing.T) {
		f := fixture(t)
		newest := f.grant
		newest.Generation = 20
		if _, err := f.authority.AcceptGrant(t.Context(), f.grantEnvelope(t, newest)); err != nil {
			t.Fatal(err)
		}
		old := f.grant
		old.AllocationID = uuid.NewString()
		old.Generation = 2
		if _, err := f.authority.AcceptGrant(t.Context(), f.grantEnvelope(t, old)); !errors.Is(err, ErrRegionalRuntimeQuotaOrder) {
			t.Fatalf("old grant: %v", err)
		}
		retirement, envelope := f.retirement(t, old)
		if _, err := f.authority.AcceptRetirement(t.Context(), envelope); err != nil {
			t.Fatal(err)
		}
		receipt, err := f.authority.AcceptGrant(t.Context(), f.grantEnvelope(t, old))
		if err != nil || receipt.Retirement == nil || *receipt.Retirement != retirement {
			t.Fatalf("old tombstone: %+v %v", receipt, err)
		}
		head, err := quota.RegionalRuntimeQuotaHead(t.Context(), old.Binding.RegionalAppID)
		if err != nil || head.Grant != newest || head.AdmissionEnabled {
			t.Fatalf("head rolled back: %+v %v", head, err)
		}
		sameGeneration := old
		sameGeneration.AllocationID = uuid.NewString()
		_, duplicate := f.retirement(t, sameGeneration)
		if _, err = f.authority.AcceptRetirement(t.Context(), duplicate); !errors.Is(err, ErrRegionalRuntimeQuotaOrder) {
			t.Fatalf("duplicate old generation: %v", err)
		}
	})
	t.Run("stream-identity-and-registry-generation", func(t *testing.T) {
		f := fixture(t)
		if _, err := f.authority.AcceptGrant(t.Context(), f.grantEnvelope(t, f.grant)); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*runtimequota.Grant){
			func(g *runtimequota.Grant) { g.OwnerID = uuid.NewString() },
			func(g *runtimequota.Grant) { g.GlobalAppID = uuid.NewString() },
			func(g *runtimequota.Grant) { g.Binding.RegionalOrgID = uuid.NewString() },
			func(g *runtimequota.Grant) { g.Binding.APIBase = "https://changed.example.com" },
		} {
			changed := f.grant
			changed.AllocationID = uuid.NewString()
			changed.Generation++
			mutate(&changed)
			authority := f.authority
			authority.Binding = changed.Binding
			if _, err := authority.AcceptGrant(t.Context(), f.grantEnvelope(t, changed)); !errors.Is(err, ErrRegionalRuntimeQuotaOrder) {
				t.Fatalf("stream rebound: %v", err)
			}
		}
		next := f.grant
		next.AllocationID = uuid.NewString()
		next.Generation++
		next.Binding.CellGeneration++
		authority := f.authority
		authority.Binding = next.Binding
		if _, err := authority.AcceptGrant(t.Context(), f.grantEnvelope(t, next)); err != nil {
			t.Fatalf("registry re-verification: %v", err)
		}
		_, oldRetirement := f.retirement(t, f.grant)
		if _, err := f.authority.AcceptRetirement(t.Context(), oldRetirement); err != nil {
			t.Fatalf("original binding retirement: %v", err)
		}
	})
	t.Run("trust-and-audience-reject-before-storage", func(t *testing.T) {
		f := fixture(t)
		envelope := f.grantEnvelope(t, f.grant)
		untrusted := f.authority
		untrusted.Directories = runtimequota.Trust{}
		if _, err := untrusted.AcceptGrant(t.Context(), envelope); !errors.Is(err, runtimequota.ErrTrust) {
			t.Fatalf("untrusted: %v", err)
		}
		wrong := f.authority
		wrong.Binding.RegionalOrgID = uuid.NewString()
		if _, err := wrong.AcceptGrant(t.Context(), envelope); !errors.Is(err, runtimequota.ErrBinding) {
			t.Fatalf("audience: %v", err)
		}
		if _, err := quota.RegionalRuntimeQuotaHead(t.Context(), f.grant.Binding.RegionalAppID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("rejection wrote head: %v", err)
		}
	})
	t.Run("current-local-owner-before-new-grant", func(t *testing.T) {
		f := fixture(t)
		wrong := f.grant
		wrong.Binding.RegionalAccountID = uuid.NewString()
		authority := f.authority
		authority.Binding = wrong.Binding
		if _, err := authority.AcceptGrant(t.Context(), f.grantEnvelope(t, wrong)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("local owner mismatch: %v", err)
		}
		if _, err := quota.RegionalRuntimeQuotaHead(t.Context(), wrong.Binding.RegionalAppID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("partial head: %v", err)
		}
	})
	t.Run("shared-tenant-uses-app-creator-identity", func(t *testing.T) {
		app, org, admin := quotaSharedApp(t, s, account, cell)
		f := fixture(t)
		f.grant.Binding.RegionalAccountID = regionalQuotaCanonicalID(app.AccountID)
		f.grant.Binding.RegionalOrgID = regionalQuotaCanonicalID(org.ID)
		f.grant.Binding.RegionalAppID, f.grant.Binding.RegionalAppSlug = app.ID, app.Slug
		f.authority.Binding = f.grant.Binding
		accepted, err := f.authority.AcceptGrant(t.Context(), f.grantEnvelope(t, f.grant))
		if err != nil || accepted.Grant.Binding.RegionalOrgID != regionalQuotaCanonicalID(org.ID) || accepted.Grant.Binding.RegionalAccountID == regionalQuotaCanonicalID(admin.ID) {
			t.Fatalf("shared quota identity: %+v %v", accepted, err)
		}
	})
	t.Run("historical-retirement", func(t *testing.T) {
		f := fixture(t)
		old := f.grant
		old.Policy.Concurrency--
		old.Policy.PeakConcurrency--
		old.Policy.ID = api.RuntimeQuotaPolicyID(old.Policy.Plan, old.Policy.Concurrency, old.Policy.PeakConcurrency)
		if _, err := f.signer.SignGrant(old); !errors.Is(err, runtimequota.ErrInvalid) {
			t.Fatalf("historical grant issued: %v", err)
		}
		retirement, envelope := f.retirement(t, old)
		accepted, err := f.authority.AcceptRetirement(t.Context(), envelope)
		if err != nil || accepted.Retirement == nil || *accepted.Retirement != retirement {
			t.Fatalf("historical tombstone: %+v %v", accepted, err)
		}
	})
	t.Run("key-rotation-preserves-first-authority", func(t *testing.T) {
		f := fixture(t)
		original := f.grantEnvelope(t, f.grant)
		if _, err := f.authority.AcceptGrant(t.Context(), original); err != nil {
			t.Fatal(err)
		}
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		rotated, err := runtimequota.NewSigner(f.signer.IssuerID(), private)
		if err != nil {
			t.Fatal(err)
		}
		f.authority.Directories, err = runtimequota.NewTrust([]runtimequota.Anchor{{Role: runtimequota.Directory, IssuerID: rotated.IssuerID(), Public: public}})
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := rotated.SignGrant(f.grant)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := f.authority.AcceptGrant(t.Context(), envelope)
		if err != nil || receipt.GrantEnvelope == nil || *receipt.GrantEnvelope != original {
			t.Fatalf("rotation rewrote authority: %+v %v", receipt, err)
		}
		if _, err = f.authority.AcceptGrant(t.Context(), original); !errors.Is(err, runtimequota.ErrTrust) {
			t.Fatalf("removed key accepted: %v", err)
		}
	})
	t.Run("concurrent-grant-and-retirement", func(t *testing.T) {
		f := fixture(t)
		grantEnvelope := f.grantEnvelope(t, f.grant)
		retirement, envelope := f.retirement(t, f.grant)
		var wg sync.WaitGroup
		errs := make(chan error, 32)
		for range 16 {
			wg.Go(func() { _, err := f.authority.AcceptGrant(t.Context(), grantEnvelope); errs <- err })
			wg.Go(func() { _, err := f.authority.AcceptRetirement(t.Context(), envelope); errs <- err })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		final, err := quota.RegionalRuntimeQuota(t.Context(), f.grant.Binding.RegionalAppID, f.grant.AllocationID)
		if err != nil || final.Retirement == nil || *final.Retirement != retirement {
			t.Fatalf("concurrent final: %+v %v", final, err)
		}
	})
	t.Run("concurrent-generation-head", func(t *testing.T) {
		f := fixture(t)
		newest := f.grant
		newest.Generation = 16
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for i := range 16 {
			g := f.grant
			g.Generation = int64(i + 1)
			g.AllocationID = uuid.NewString()
			if i == 15 {
				g = newest
			}
			envelope := f.grantEnvelope(t, g)
			wg.Go(func() { _, err := f.authority.AcceptGrant(t.Context(), envelope); errs <- err })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil && !errors.Is(err, ErrRegionalRuntimeQuotaOrder) {
				t.Fatal(err)
			}
		}
		head, err := quota.RegionalRuntimeQuotaHead(t.Context(), f.grant.Binding.RegionalAppID)
		if err != nil || head.Grant != newest || head.AdmissionEnabled {
			t.Fatalf("concurrent head: %+v %v", head, err)
		}
	})
}

func TestMemRegionalRuntimeQuotaContract(t *testing.T) {
	s := NewMemStore()
	account, err := s.CreateAccount(t.Context(), "quota@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	runRegionalRuntimeQuotaContract(t, s, s, account, uuid.NewString())
}

func TestMemRegionalRuntimeQuotaCollection(t *testing.T) {
	s := NewMemStore()
	account, err := s.CreateAccount(t.Context(), "collected-quota@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f := newRegionalQuotaFixture(t, s, s, account, uuid.NewString())
	if _, err = f.authority.AcceptGrant(t.Context(), f.grantEnvelope(t, f.grant)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	delete(s.apps, f.grant.Binding.RegionalAppID)
	s.mu.Unlock()
	retirement, envelope := f.retirement(t, f.grant)
	if _, err = f.authority.AcceptRetirement(t.Context(), envelope); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.RegionalRuntimeQuota(t.Context(), f.grant.Binding.RegionalAppID, f.grant.AllocationID)
	if err != nil || receipt.Retirement == nil || *receipt.Retirement != retirement {
		t.Fatalf("collected authority lost: %+v %v", receipt, err)
	}
	unknown := f.grant
	unknown.AllocationID = uuid.NewString()
	unknown.Generation++
	_, envelope = f.retirement(t, unknown)
	if _, err = f.authority.AcceptRetirement(t.Context(), envelope); err != nil {
		t.Fatalf("delayed retirement after collection: %v", err)
	}
}

func quotaSharedApp(t *testing.T, s Store, account Account, cell string) (App, Org, Account) {
	t.Helper()
	_, org, admin, _ := sharedRegionalGrantFixture(t, s, account, cell)
	app, err := s.CreateApp(t.Context(), App{ID: uuid.NewString(), AccountID: account.ID, OrgID: org.ID,
		Slug: "shared-quota-" + uuid.NewString()[:8], Type: AppTypeApp, Status: AppActive, Runtime: "node22", RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	return app, org, admin
}
