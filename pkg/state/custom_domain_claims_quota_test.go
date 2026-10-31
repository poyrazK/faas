package state_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pendingDomainSubjectRaceContract claims one domain on each of six members'
// company apps at once, against a pending-domain budget of three. The
// company adopted billing, so all six spend its one budget
// (ADR-202610171904): exactly three claims land, whichever members make them.
func pendingDomainSubjectRaceContract(t *testing.T, store state.Store, adopt func(orgID string)) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	org, err := store.CreateOrg(ctx, state.Org{Slug: "domains-" + suffix, Name: "Domains Co", Plan: api.PlanFree})
	if err != nil {
		t.Fatal(err)
	}
	if adopt != nil {
		adopt(org.ID)
	}
	if err := store.UpdateOrgPlan(ctx, org.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	const limit, members = 3, 6
	apps := make([]state.App, members)
	for i := range apps {
		member, err := store.CreateAccount(ctx, fmt.Sprintf("claimer-%d-%s@example.com", i, suffix), api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatal(err)
		}
		apps[i], err = store.CreateApp(ctx, state.App{AccountID: member.ID, OrgID: org.ID, Slug: fmt.Sprintf("domains-%d-%s", i, suffix),
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
	}
	claimer := store.(pendingDomainClaimer)
	var (
		mu      sync.Mutex
		claimed int
		wg      sync.WaitGroup
	)
	errs := make([]error, members)
	for i, app := range apps {
		wg.Go(func() {
			_, errs[i] = claimer.CreateCustomDomainIfUnderQuota(ctx, fmt.Sprintf("claim-%d-%s.example.test", i, suffix), app.ID,
				"token-"+uuid.NewString(), 10, limit)
			if errs[i] == nil {
				mu.Lock()
				claimed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if claimed != limit {
		t.Fatalf("%d members' domain claims landed against the company's pending budget of %d: %v", claimed, limit, errs)
	}
	for i, err := range errs {
		if err != nil && !pendingDomainAccountCap(err) {
			t.Fatalf("claim %d: %v, want the company's pending-domain budget", i, err)
		}
	}
}

// adr: 202610171904
func TestMemPendingDomainsShareTheCompanyBudget(t *testing.T) {
	pendingDomainSubjectRaceContract(t, state.NewMemStore(), nil)
}
