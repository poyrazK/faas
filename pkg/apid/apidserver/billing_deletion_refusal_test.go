package apidserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A deletion the store refuses must leave the subscription alone: cancelling
// first ended a paid plan at period end for an organization (or account)
// that stayed in use.
func TestRefusedOrganizationDeletionKeepsTheSubscription(t *testing.T) {
	fake := &deletionPolar{status: "active"}
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).WithBillingProvider(deletionProvider(t, fake))
	org := apidserver.SeedSharedOrgWithOwner(t, e, "acme-b3", "Acme B3", api.PlanPro)
	store := *e.Store()
	if err := store.UpsertBillingIdentity(t.Context(), state.BillingIdentity{AccountID: org.ID, Provider: "polar", CustomerID: "polar-org-" + org.ID, SubscriptionID: "sub-org-b3"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: e.Acct().ID, OrgID: org.ID, Slug: "b3-app", Type: state.AppTypeFunction,
		Runtime: "node22", RAMMB: 256, MaxConcurrency: 1, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveObjectBucket(t.Context(), state.ObjectBucket{
		ID: uuid.NewString(), AccountID: e.Acct().ID, AppID: app.ID, Name: "assets",
		Scope: "default", Region: "us-east-1", PhysicalName: "b3-assets",
	}, 10); err != nil {
		t.Fatal(err)
	}

	rec := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug, nil, map[string]string{"X-Active-Org": org.Slug})
	if rec.Code != http.StatusConflict {
		t.Fatalf("DELETE org with a bucket = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if got := fake.cancellations(); len(got) != 0 {
		t.Fatalf("cancellations after a refused deletion = %v, want none", got)
	}
}

// Restoring a paid account says the cancelled subscription was not resumed;
// the customer otherwise dropped to Free at period end without warning.
func TestRestoreSaysTheSubscriptionWasNotResumed(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if rec := e.Do(t, http.MethodDelete, "/v1/account", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("DELETE /v1/account = %d %s", rec.Code, rec.Body.String())
	}
	rec := e.Do(t, http.MethodPost, "/v1/account/restore", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Warning"); !strings.Contains(got, "Re-subscribe") {
		t.Fatalf("restore Warning = %q, want the re-subscribe notice", got)
	}
}
