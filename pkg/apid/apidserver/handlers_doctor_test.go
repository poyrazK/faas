package apidserver_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestGetDomainDoctor_DisabledFlagReturns503 (ADR-120 Tier A3)
// asserts that an operator opt-out (FAAS_DOMAIN_DOCTOR_ENABLED=false)
// makes the doctor route return 503 doctor_disabled even though
// the domain exists in the store. The handler's first-line guard
// at cmd/apid/handlers_ext.go:1892 short-circuits before any
// probe logic so a single env flip is the only knob needed to
// disable the doctor fleet-wide.
func TestGetDomainDoctor_DisabledFlagReturns503(t *testing.T) {
	apidserver.WithDomainDoctorDisabled(t)
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "doc-flag-app")
	if _, err := (*e.Store()).CreateCustomDomain(context.Background(), "flag.example.com", appID, "tok"); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	rec := e.Do(t, "GET", "/v1/domains/flag.example.com/doctor", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusServiceUnavailable, api.CodeDoctorDisabled)
}

// TestGetDomainDoctor_FlagEnabledServesReport (ADR-120 Tier A3)
// asserts the happy path: flag on, domain seeded, an observation
// row exists, the JSON response matches the wire DTO shape with
// the 5-check report. The 5-row invariant is load-bearing per
// pkg/api/dto.go — adding a new check there requires updating
// the dashboard rendering AND the Alertmanager runbook so the
// three surfaces stay in lockstep.
func TestGetDomainDoctor_FlagEnabledServesReport(t *testing.T) {
	apidserver.WithDomainDoctorEnabled(t)
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "doc-on-app")
	if _, err := (*e.Store()).CreateCustomDomain(context.Background(), "on.example.com", appID, "tok"); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	// Seed a fully-OK observation row so buildDoctorReport returns
	// without triggering a synchronous re-probe (which would dial
	// a real port-443 and break the unit test).
	obs := state.DomainDoctorObservation{
		Domain:          "on.example.com",
		ObservedAt:      time.Now().UTC(),
		DNSRecordFound:  true,
		PointsToGregale: true,
		IPv6Conflict:    false,
		CertState:       "issued",
		ObservedTarget:  "apps.gregale.dev",
		DNSCheckedAt:    time.Now().UTC(),
	}
	if err := (*e.Store()).UpsertDoctorObservation(context.Background(), obs); err != nil {
		t.Fatalf("upsert obs: %v", err)
	}
	rec := e.Do(t, "GET", "/v1/domains/on.example.com/doctor", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var report api.DomainDoctorReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report.Domain != "on.example.com" {
		t.Errorf("domain = %q; want on.example.com", report.Domain)
	}
	if len(report.Checks) != 5 {
		t.Errorf("checks len = %d; want 5 (DNS / points_to_gregale / TLS / CAA / IPv6)", len(report.Checks))
	}
	if !report.Healthy {
		t.Errorf("Healthy = false; want true (every check ok in the seeded obs)")
	}
}

func TestDoctorReportRemediatesToConfiguredTargetNotObservedTarget(t *testing.T) {
	previous := (*apidserver.AppsDomainFunc)
	(*apidserver.AppsDomainFunc) = func() string { return "apps.gregale.dev." }
	t.Cleanup(func() { (*apidserver.AppsDomainFunc) = previous })

	report := apidserver.DoctorReportFromObs(state.CustomDomain{Domain: "api.customer.test"}, state.DomainDoctorObservation{
		Domain:          "api.customer.test",
		ObservedAt:      time.Now().UTC(),
		DNSRecordFound:  true,
		PointsToGregale: false,
		ObservedTarget:  "api.customer.test",
		DNSCheckedAt:    time.Now().UTC(),
		CertState:       apidserver.CertStatusPending,
	}, false)
	for _, check := range report.Checks {
		if check.Name != "points_to_gregale" {
			continue
		}
		if want := "Set CNAME api.customer.test → apps.gregale.dev"; check.Remediation != want {
			t.Fatalf("remediation = %q, want %q", check.Remediation, want)
		}
		if strings.Contains(check.Remediation, "→ api.customer.test") {
			t.Fatalf("remediation creates a self-CNAME: %q", check.Remediation)
		}
		return
	}
	t.Fatal("points_to_gregale check missing")
}

func TestDoctorReportNeverSuggestsSelfCNAMEWhenPlatformTargetIsInvalid(t *testing.T) {
	previous := (*apidserver.AppsDomainFunc)
	(*apidserver.AppsDomainFunc) = func() string { return "api.customer.test" }
	t.Cleanup(func() { (*apidserver.AppsDomainFunc) = previous })

	report := apidserver.DoctorReportFromObs(state.CustomDomain{Domain: "api.customer.test"}, state.DomainDoctorObservation{
		Domain:          "api.customer.test",
		ObservedAt:      time.Now().UTC(),
		DNSRecordFound:  true,
		PointsToGregale: false,
		ObservedTarget:  "api.customer.test",
		DNSCheckedAt:    time.Now().UTC(),
		CertState:       apidserver.CertStatusPending,
	}, false)
	for _, check := range report.Checks {
		if check.Name == "points_to_gregale" && strings.HasPrefix(check.Remediation, "Set CNAME") {
			t.Fatalf("invalid configured target produced CNAME remediation: %q", check.Remediation)
		}
	}
}

// TestGetDomainDoctor_IDOR (ADR-120 Tier A2) asserts a cross-
// tenant probe returns 404 (not 403 — no leak that the row
// exists). The handler's loadDomain helper at
// cmd/apid/handlers_ext.go:1722 owns the ownership check and
// short-circuits with ok=false on cross-tenant; the wrapper
// at handlers_ext.go:1892 returns 404 not_found when loadDomain
// signals failure.
func TestGetDomainDoctor_IDOR(t *testing.T) {
	apidserver.WithDomainDoctorEnabled(t)
	// Account A creates the domain.
	a := apidserver.Setup(t, api.PlanPro)
	appIDA := apidserver.MustSeedApp(t, a, "idor-a")
	if _, err := (*a.Store()).CreateCustomDomain(context.Background(), "shared.example.com", appIDA, "tok"); err != nil {
		t.Fatalf("seed domain A: %v", err)
	}
	// Account B probes it.
	b := apidserver.Setup(t, api.PlanPro)
	// Account B's sessionAuth must NOT see account A's domain.
	rec := b.Do(t, "GET", "/v1/domains/shared.example.com/doctor", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}

// TestGetDomainDoctor_RefreshedObservationIsNotStale (ADR-120 Tier A3,
// issue #2659): an observation row older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS
// is re-probed synchronously, and a successful re-probe answers with the
// fresh observation and Stale=false. Stale (the dashboard's banner) means
// the refresh failed and the old row is all there is.
func TestGetDomainDoctor_RefreshedObservationIsNotStale(t *testing.T) {
	apidserver.WithDomainDoctorEnabled(t)
	// Force the TTL to 5s so we can build a stale row deterministically.
	t.Setenv("FAAS_DOMAIN_DOCTOR_TTL_SECONDS", "5")
	// Inject probe seams (review-fix #3) so the handler's
	// refreshDoctorObservation path doesn't dial real DNS on a
	// CI runner. Returning the same values the seeded row
	// carries keeps the re-probe write idempotent and the
	// assertion race-free.
	apidserver.WithSeams(t,
		func(_ context.Context, _ string) ([]string, error) { return []string{"apps.gregale.dev"}, nil },
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		func(_ context.Context, _ string) ([]string, error) {
			return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
		},
		"apps.gregale.dev")
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "stale-app")
	if _, err := (*e.Store()).CreateCustomDomain(context.Background(), "stale.example.com", appID, "tok"); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	// Seed an observation row 30s in the past — well past the 5s TTL.
	obs := state.DomainDoctorObservation{
		Domain:          "stale.example.com",
		ObservedAt:      time.Now().UTC().Add(-30 * time.Second),
		DNSRecordFound:  true,
		PointsToGregale: true,
		IPv6Conflict:    false,
		CertState:       "issued",
		ObservedTarget:  "apps.gregale.dev",
	}
	if err := (*e.Store()).UpsertDoctorObservation(context.Background(), obs); err != nil {
		t.Fatalf("upsert obs: %v", err)
	}
	rec := e.Do(t, "GET", "/v1/domains/stale.example.com/doctor", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var report api.DomainDoctorReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if report.Stale {
		t.Errorf("Stale = true after a successful re-probe; want false")
	}
	if observed, err := time.Parse(time.RFC3339, report.ObservedAt); err != nil || !observed.After(obs.ObservedAt.Add(15*time.Second)) {
		t.Errorf("ObservedAt = %q, want the re-probe's time, not the seeded row's %s", report.ObservedAt, obs.ObservedAt.Format(time.RFC3339))
	}
}

// TestParseDomainDoctorPath (ADR-120 Tier A2) covers the dashboard
// route parser's three observable states: matching
// {slug}/domains/{domain}/doctor → (slug, domain, true);
// non-matching suffix → ok=false (caller falls through to
// renderAppDetail); empty domain or empty slug → ok=false
// (defensive — same posture as parseDeployDetailPath).
func TestParseDomainDoctorPath(t *testing.T) {
	cases := []struct {
		in         string
		wantSlug   string
		wantDomain string
		wantOK     bool
	}{
		{"api-app/domains/api.example.com/doctor", "api-app", "api.example.com", true},
		{"app-1/domains/sub.example.com/doctor", "app-1", "sub.example.com", true},
		// No /domains/ prefix — falls through.
		{"app-1/deployments/dep-1", "", "", false},
		// Empty domain — defensive fail.
		{"app-1/domains//doctor", "", "", false},
		// Missing /doctor suffix — falls through.
		{"app-1/domains/api.example.com", "", "", false},
		// Empty slug — defensive fail.
		{"/domains/api.example.com/doctor", "", "", false},
		// Sub-tab shape (review-fix #4) — must NOT match so a
		// future /doctor/{tab} sibling parser can claim it
		// without touching this function.
		{"app-1/domains/api.example.com/doctor/logs", "", "", false},
		{"app-1/domains/api.example.com/doctor/", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			gotSlug, gotDomain, gotOK := apidserver.ParseDomainDoctorPath(tc.in)
			if gotSlug != tc.wantSlug || gotDomain != tc.wantDomain || gotOK != tc.wantOK {
				t.Fatalf("parseDomainDoctorPath(%q) = (%q, %q, %v); want (%q, %q, %v)",
					tc.in, gotSlug, gotDomain, gotOK, tc.wantSlug, tc.wantDomain, tc.wantOK)
			}
		})
	}
}

// TestRenderDomainDoctor_RouteRegistered (ADR-120 Tier A2)
// asserts the dashboard HTTP path
// /dashboard/apps/{slug}/domains/{domain}/doctor reaches the
// renderDomainDoctor handler and returns 200 with the doctor
// template body. Uses newAuthedDashboardServerFull so the
// sessionAuth middleware sees a real faas_sid cookie (the
// /dashboard/* mount at server.go:1670 short-circuits to
// /login otherwise).
//
// TestRenderDomainDoctor_FlagDisabledReturns503 (review-fix #2)
// covers the operator-escape-hatch posture: with the flag off,
// the dashboard route MUST mirror the JSON endpoint's 503
// doctor_disabled problem — otherwise the two surfaces disagree
// on the same operator choice (CLI gets a structured error,
// dashboard renders a stale 5-check report).
func TestRenderDomainDoctor_FlagDisabledReturns503(t *testing.T) {
	apidserver.WithDomainDoctorDisabled(t)
	h, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "render-doc-off-app",
	}); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/dashboard/apps/render-doc-off-app/domains/foo.example.com/doctor", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d; want 503 (flag off). body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q; want application/problem+json", ct)
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if code, _ := p["code"].(string); code != api.CodeDoctorDisabled {
		t.Errorf("problem.code = %q; want %q", code, api.CodeDoctorDisabled)
	}
}

func TestRenderDomainDoctor_RouteRegistered(t *testing.T) {
	apidserver.WithDomainDoctorEnabled(t)
	h, cookie, store, _ := apidserver.NewAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "render-doc-app",
	})
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := store.CreateCustomDomain(context.Background(), "render.example.com", app.ID, "tok"); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	obs := state.DomainDoctorObservation{
		Domain:          "render.example.com",
		ObservedAt:      time.Now().UTC(),
		DNSRecordFound:  true,
		PointsToGregale: true,
		IPv6Conflict:    false,
		CertState:       "issued",
	}
	if err := store.UpsertDoctorObservation(context.Background(), obs); err != nil {
		t.Fatalf("upsert obs: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/dashboard/apps/render-doc-app/domains/render.example.com/doctor", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "render.example.com") {
		t.Errorf("body should mention domain; got %q", body)
	}
	if !strings.Contains(body, "Checks") {
		t.Errorf("body should render the Checks h2; got %q", body)
	}
}

// TestDoctorReportMissingRecordMatchesAddInstructions — production-us
// reported a failing dns_record check as "A or AAAA records present" and
// asked for an A/AAAA record, while `domains add` and points_to_gregale both
// asked for the Gregale CNAME. The failing detail now describes the
// observation, and the fix is the same routing record.
func TestDoctorReportMissingRecordMatchesAddInstructions(t *testing.T) {
	previous := (*apidserver.AppsDomainFunc)
	(*apidserver.AppsDomainFunc) = func() string { return "gregale.dev." }
	t.Cleanup(func() { (*apidserver.AppsDomainFunc) = previous })
	t.Setenv("FAAS_CUSTOM_DOMAIN_ADDRESSES", "")

	report := apidserver.DoctorReportFromObs(state.CustomDomain{Domain: "h3-test-domain.example.com"}, state.DomainDoctorObservation{
		Domain: "h3-test-domain.example.com", ObservedAt: time.Now().UTC(), DNSCheckedAt: time.Now().UTC(),
		DNSRecordFound: false, PointsToGregale: false, CertState: apidserver.CertStatusPending,
	}, false)
	checks := map[string]api.DomainDoctorCheck{}
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	dns := checks["dns_record"]
	if dns.Status != string(apidserver.ProbeFail) || strings.Contains(dns.Detail, "present") || !strings.Contains(dns.Detail, "no DNS record") {
		t.Fatalf("dns_record = %+v, want a failing detail that says nothing resolves", dns)
	}
	if want := "Set CNAME h3-test-domain.example.com → gregale.dev"; dns.Remediation != want || checks["points_to_gregale"].Remediation != want {
		t.Fatalf("remediations dns=%q points=%q, want both %q", dns.Remediation, checks["points_to_gregale"].Remediation, want)
	}
}
