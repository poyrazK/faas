package state_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// openAPIImportSubjectRaceContract imports one document on each of six
// members' company apps at once, against an allowance of three. The company
// adopted billing, so all six spend its one allowance (ADR-202610171904):
// exactly three land, whichever members import them.
func openAPIImportSubjectRaceContract(t *testing.T, store state.Store, adopt func(orgID string)) {
	t.Helper()
	ctx, suffix := t.Context(), uuid.NewString()[:8]
	org, err := store.CreateOrg(ctx, state.Org{Slug: "imports-" + suffix, Name: "Imports Co", Plan: api.PlanFree})
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
		member, err := store.CreateAccount(ctx, fmt.Sprintf("importer-%d-%s@example.com", i, suffix), api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddOrgMember(ctx, org.ID, member.ID, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatal(err)
		}
		apps[i], err = store.CreateApp(ctx, state.App{AccountID: member.ID, OrgID: org.ID, Slug: fmt.Sprintf("imports-%d-%s", i, suffix),
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
	}
	var (
		mu       sync.Mutex
		imported int
		wg       sync.WaitGroup
	)
	errs := make([]error, members)
	for i, app := range apps {
		wg.Go(func() {
			errs[i] = store.UpsertAppOpenAPIDocIfUnderQuota(ctx, app.ID, app.AccountID, []byte(`{"openapi":"3.1.0","info":{"title":"quota"}}`), 1, "3.1.0", limit)
			if errs[i] == nil {
				mu.Lock()
				imported++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if imported != limit {
		t.Fatalf("%d members' imports landed against the company's allowance of %d: %v", imported, limit, errs)
	}
	for i, err := range errs {
		if err != nil && !openAPIImportAccountCap(err) {
			t.Fatalf("import %d: %v, want the company's import quota", i, err)
		}
	}
}

// adr: 202610171904
func TestMemOpenAPIImportsShareTheCompanyAllowance(t *testing.T) {
	openAPIImportSubjectRaceContract(t, state.NewMemStore(), nil)
}
