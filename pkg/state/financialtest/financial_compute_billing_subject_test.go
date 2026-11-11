package financialtest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

type orgComputeTestStore interface {
	computeTestStore
	CreateOrg(context.Context, state.Org) (state.Org, error)
	AddOrgMember(ctx context.Context, orgID, accountID string, role state.OrgRole, invitedBy *string) error
}

// adr: 20261107160000 — strict compute follows the billing subject. A member's
// strict account budget neither funds, stops nor waits for an app an
// organization that owns its resources pays for.
func TestFinancialComputeFollowsBillingSubject(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store orgComputeTestStore = state.NewMemStore()
			adopt := func(string) {} // MemStore organizations own their resources.
			if backend == "postgres" {
				pg, pool, ctx := financialPostgres(t)
				store = pg
				adopt = func(org string) {
					if _, err := pool.Exec(ctx, `update orgs set resource_ownership_enabled = true where id = $1`, org); err != nil {
						t.Fatal(err)
					}
				}
			}
			financialComputeBillingSubjectSuite(t, store, adopt)
		})
	}
}

func financialComputeBillingSubjectSuite(t *testing.T, store orgComputeTestStore, adopt func(string)) {
	t.Helper()
	ctx := t.Context()
	f := newAccountComputeFixture(t, store, 153600, 1, false)
	member, personal := f.account.ID, f.apps[0]
	org, err := store.CreateOrg(ctx, state.Org{Slug: "subject-" + uuid.NewString()[:8], Name: "Subject Co", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	adopt(org.ID)
	if err := store.AddOrgMember(ctx, org.ID, member, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: member, OrgID: org.ID, Slug: "subject-co-" + member[:8], RAMMB: 128, MaxConcurrency: 20})
	if err != nil {
		t.Fatal(err)
	}
	companyRequest := func() financial.ComputeRequest {
		r := f.request(0)
		r.AppID = company.ID
		return r
	}

	// The company's app runs before the member caps their own spending: its
	// unbudgeted attempt does not keep the member's account from activating.
	running, err := store.AcquireFinancialCompute(ctx, companyRequest())
	if err != nil || running.Strict || running.AccountFunded {
		t.Fatalf("company attempt before activation = %+v, %v; want an unbudgeted grant", running, err)
	}
	if f.policy, err = store.CreateFinancialBudget(ctx, member, uuid.NewString(), "test", f.policy.Spec); err != nil {
		t.Fatalf("member account cap with a company app running: %v", err)
	}

	policies, err := store.FinancialComputePolicies(ctx, member, company.ID)
	if err != nil || len(policies) != 0 {
		t.Fatalf("company app policies = %d, %v; want none", len(policies), err)
	}
	if policies, err = store.FinancialComputePolicies(ctx, member, personal.ID); err != nil || len(policies) != 1 {
		t.Fatalf("personal app policies = %d, %v; want the account cap", len(policies), err)
	}
	for _, tc := range []struct {
		app  state.App
		want bool
	}{{company, false}, {personal, true}} {
		managed, err := store.FinancialComputeAppManaged(ctx, member, tc.app.ID)
		if err != nil || managed != tc.want {
			t.Fatalf("%s managed = %v, %v; want %v", tc.app.Slug, managed, err, tc.want)
		}
		covered, err := store.FinancialComputeFloorCovered(ctx, member, tc.app.ID, time.Now())
		if err != nil || covered != tc.want {
			t.Fatalf("%s floor covered = %v, %v; want %v", tc.app.Slug, covered, err, tc.want)
		}
	}

	// The account book funds the member's own app while the company's
	// unfunded attempts are still live, and never funds the company's.
	again, err := store.AcquireFinancialCompute(ctx, companyRequest())
	if err != nil || again.Strict || again.AccountFunded {
		t.Fatalf("company attempt under the member's cap = %+v, %v; want an unbudgeted grant", again, err)
	}
	own, err := store.AcquireFinancialCompute(ctx, f.request(0))
	if err != nil || !own.Strict || !own.AccountFunded {
		t.Fatalf("personal attempt = %+v, %v; want account funding", own, err)
	}
	if b := f.balance(t); b.Held || b.AllowanceReserved+b.ReservedMillicents <= 0 {
		t.Fatalf("account balance = %+v; want the personal reserve", b)
	}
	f.settle(t, own)
	if b := f.balance(t); b.AllowanceReserved != 0 || b.ReservedMillicents != 0 {
		t.Fatalf("account balance after settling the personal attempt = %+v; company attempts must not reserve", b)
	}
}
