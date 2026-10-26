package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The account-wide dead-letter routes read rows keyed by the creating
// account. After a creator leaves a company, that company's app is hidden
// from every account view (appaccess.VisibleApps): its dead letters, which
// carry request payloads and headers, must not be listed, read, replayed
// or purged through /v1/account/dlq either.
func TestAccountUnifiedDeadLetter_HidesCompanyAppsAfterOffboarding(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, creator := *e.Store(), *e.Acct()
	ctx := t.Context()
	owner, err := store.CreateAccount(ctx, "acme-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, owner.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	company, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "acme-worker",
		Type: state.AppTypeApp, Runtime: "node22", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	own := apidserver.MustSeedApp(t, e, "own-app")
	companySource := apidserver.SeedDeadLetterRow(t, e, company.ID, "company-payload")
	apidserver.SeedDeadLetterRow(t, e, own, "own-payload")
	companyEvent := ""
	for _, ev := range accountDeadLetters(t, e) {
		if ev.SourceID == companySource {
			companyEvent = ev.ID
		}
	}
	if companyEvent == "" {
		t.Fatal("a member does not see the company app's dead letter")
	}

	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	for _, ev := range accountDeadLetters(t, e) {
		if ev.AppID == company.ID {
			t.Fatalf("an offboarded creator lists the company app's dead letter %s", ev.ID)
		}
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/account/dlq/" + companyEvent},
		{http.MethodPost, "/v1/account/dlq/" + companyEvent + "/replay"},
		{http.MethodDelete, "/v1/account/dlq/" + companyEvent},
	} {
		rec := e.Do(t, tc.method, tc.path, nil, map[string]string{"Idempotency-Key": tc.method + "-" + companyEvent})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404: %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
	replayAll := e.Do(t, http.MethodPost, "/v1/account/dlq:replay_all?limit=10", nil, map[string]string{"Idempotency-Key": "replay-all"})
	purgeAll := e.Do(t, http.MethodDelete, "/v1/account/dlq?limit=10", nil, map[string]string{"Idempotency-Key": "purge-all"})
	if replayAll.Code != http.StatusAccepted || purgeAll.Code != http.StatusOK {
		t.Fatalf("batch replay %d / purge %d: %s %s", replayAll.Code, purgeAll.Code, replayAll.Body, purgeAll.Body)
	}
	inv, err := store.InvocationByID(ctx, companySource)
	if err != nil {
		t.Fatal(err)
	}
	if inv.State != state.InvocationDeadLetter {
		t.Fatalf("an offboarded creator's batch replay moved the company dead letter to %q", inv.State)
	}
	left, err := store.ListDeadLetterEvents(ctx, company.ID, 10, "")
	if err != nil || len(left) != 1 {
		t.Fatalf("company dead letters after the creator's batch purge = %d, %v; want 1", len(left), err)
	}
}

func accountDeadLetters(t *testing.T, e apidserver.TestEnv) []api.DeadLetterEvent {
	t.Helper()
	rec := e.Do(t, http.MethodGet, "/v1/account/dlq?limit=50", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("account dlq list = %d: %s", rec.Code, rec.Body)
	}
	var page api.AccountDeadLetterEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page.Events
}
