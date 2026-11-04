package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

type domainAuthorizationBody struct {
	before func()
	reader io.Reader
}

func (b *domainAuthorizationBody) Read(p []byte) (int, error) {
	if b.before != nil {
		f := b.before
		b.before = nil
		f()
	}
	return b.reader.Read(p)
}

func TestCustomDomainReassignmentPreservesOrganizationAuthorization(t *testing.T) {
	withIdempotencyHostKey(t)
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	firstOrg := apidserver.SeedSharedOrgWithOwner(t, e, "domain-first", "First", api.PlanPro)
	nextOrg := apidserver.SeedSharedOrgWithOwner(t, e, "domain-next", "Next", api.PlanPro)
	create := func(org state.Org, slug string) state.App {
		app, err := store.CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, OrgID: org.ID, Slug: slug, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	first, next := create(firstOrg, "domain-first-app"), create(nextOrg, "domain-next-app")
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateOrgAPIKey(t.Context(), firstOrg.ID, (*e.Acct()).ID, hash, "first-org-only", api.ScopesAdminOnly, nil); err != nil {
		t.Fatal(err)
	}
	const host = "reassignment.domains.example.com"
	domainRecoveryVerified(t, store, host, first.ID, "first-proof")
	var replaced bool
	body := &domainAuthorizationBody{reader: strings.NewReader("{}"), before: func() {
		replaced = true
		if err := store.DeleteCustomDomain(t.Context(), host); err != nil {
			t.Fatal(err)
		}
		domainRecoveryVerified(t, store, host, next.ID, "next-proof")
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/domains/"+host+"/default", body)
	req.Header.Set("Authorization", "Bearer "+plain)
	req.Header.Set("Idempotency-Key", "set-default")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if !replaced {
		t.Fatalf("body seam was not reached: %d %s", rec.Code, rec.Body)
	}
	control := e.Do(t, http.MethodPost, req.URL.Path, nil, map[string]string{"Authorization": "Bearer " + plain})
	if control.Code != 404 {
		t.Fatalf("invalid control: %d %s", control.Code, control.Body)
	}
	selected, err := store.DefaultCustomDomain(t.Context(), next.ID)
	if selected == host || rec.Code != 404 {
		t.Fatalf("org-A key modified org-B app: in-flight=%d %s fresh=%d default=%q err=%v", rec.Code, rec.Body, control.Code, selected, err)
	}
}

func TestCustomDomainDefaultRejectsReplacementOwner(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	first := apidserver.MustSeedApp(t, e, "domain-default-first")
	next := domainRecoveryApp(t, store, "domain-default-next@example.com", "domain-default-next")
	const host = "default.domains.example.com"
	domainRecoveryVerified(t, store, host, first, "first-proof")
	if err := store.SetDefaultCustomDomain(t.Context(), first, host); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkCustomDomainDNSDrifted(t.Context(), host, time.Now(), "routing changed"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TakeOverCustomDomainClaim(t.Context(), host, next.ID, "", "next-proof", 100, 500); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	selected, err := store.DefaultCustomDomain(t.Context(), first)
	if selected == host {
		t.Fatalf("old app default resolves to replacement owner's domain: selected=%q err=%v", selected, err)
	}
}

func TestCustomDomainVerificationMissPreservesIssuedCertificate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "domain-overlapping-verification")
	const host = "overlap.domains.example.com"
	d, err := store.CreateCustomDomain(t.Context(), host, app, "proof")
	if err != nil {
		t.Fatal(err)
	}
	prev := *apidserver.TxtLookupFunc
	t.Cleanup(func() { *apidserver.TxtLookupFunc = prev })
	*apidserver.TxtLookupFunc = func(ctx context.Context, _ string) ([]string, error) {
		// Another verifier completes this same claim while this lookup is in flight.
		if err := store.MarkDomainVerified(ctx, host); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateCustomDomainCertStatus(state.WithCustomDomainProbe(ctx, d), host, state.CustomDomainCertIssued, time.Now().Add(30*24*time.Hour), "", time.Now()); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	}
	(*e.S()).RunVerifyOnce(t.Context(), slog.New(slog.DiscardHandler))
	current, err := store.DomainByName(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	if !current.Verified() {
		t.Fatal("verification was not exercised")
	}
	if current.CertStatus != state.CustomDomainCertIssued || current.CertExpiresAt.IsZero() {
		t.Fatalf("stale DNS miss regressed issued cert: status=%s expires=%v", current.CertStatus, current.CertExpiresAt)
	}
}
