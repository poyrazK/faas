package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardHandler_AppDomainsSummaryAndDoctor(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{
		AccountID: acct.ID, Slug: "domains-app", Type: state.AppTypeApp,
		Runtime: "node22", Status: state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := store.CreateCustomDomain(t.Context(), "example.com", app.ID, "txt-token"); err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if err := store.MarkDomainVerified(t.Context(), "example.com"); err != nil {
		t.Fatalf("MarkDomainVerified: %v", err)
	}
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	if err := store.UpdateCustomDomainCertStatus(t.Context(), "example.com", state.CustomDomainCertIssued, expires, "", time.Now().UTC()); err != nil {
		t.Fatalf("UpdateCustomDomainCertStatus: %v", err)
	}
	if err := store.UpsertDoctorObservation(t.Context(), state.DomainDoctorObservation{
		Domain: "example.com", ObservedAt: time.Now().UTC(), DNSRecordFound: true,
		PointsToGregale: true, CertState: string(state.CustomDomainCertIssued),
		DNSCheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertDoctorObservation: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/domains-app/domains", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary code = %d\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Domains: <code>domains-app</code>", "example.com", "verified", "issued", "Domain doctor", "healthy", "tls_certificate", "www.example.com"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("summary body missing %q\n%s", want, rec.Body.String())
		}
	}
}

func TestParseAppDomainsPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
		ok   bool
	}{
		{path: "domains-app/domains", want: "domains-app", ok: true},
		{path: "domains-app/domains/", want: "domains-app", ok: true},
		{path: "domains-app", ok: false},
		{path: "domains-app/domains/example.com", ok: false},
		{path: "/domains", ok: false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, ok := parseAppDomainsPath(tc.path)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseAppDomainsPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
			}
		})
	}
}
