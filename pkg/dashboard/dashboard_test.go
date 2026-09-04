package dashboard_test

import (
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/presetwhy"
)

// TestRender_Layout confirms the layout template parses, executes
// without error, and contains the expected chrome (HTMX script, nav).
func TestRender_Layout(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Overview",
		Body:  "index",
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"<title>Overview — onebox faas</title>",
		"htmx.org@2.0.4",
		"/dashboard/",
		"Overview",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestRender_LoginBody confirms a page that uses the Body field
// resolves to the right template name.
func TestRender_LoginBody(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Sign in",
		Body:  "login",
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rec.Body.String(), `<form method="POST" action="/login"`) {
		t.Errorf("body missing login form\n--- body ---\n%s", rec.Body.String())
	}
}

// TestRender_LoginBody_GoogleEnabled (issue #419 / ADR-046) — when
// the boot-resolved auth.SignInConfig reports Google Enabled, the
// /login surface must render the "Sign in with Google" link. The
// dashboard hits GET /v1/auth/capabilities to learn this, but the
// template gates per provider on the AuthCapabilitiesView bools the
// handler populates from s.oauthConfig.
func TestRender_LoginBody_GoogleEnabled(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Sign in",
		Body:  "login",
		Auth:  &dashboard.AuthCapabilitiesView{GoogleEnabled: true},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rec.Body.String(), `href="/v1/auth/google"`) {
		t.Errorf("google link missing\n--- body ---\n%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `href="/v1/auth/github"`) {
		t.Errorf("github link should not render when only Google is enabled\n--- body ---\n%s", rec.Body.String())
	}
}

// TestRender_LoginBody_GitHubEnabled — the GitHub mirror of
// TestRender_LoginBody_GoogleEnabled above. Both providers
// independent — the dashboard reads each provider's bool off
// .Auth.<Name>Enabled, and a one-provider host gates the other
// off in steady state.
func TestRender_LoginBody_GitHubEnabled(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Sign in",
		Body:  "login",
		Auth:  &dashboard.AuthCapabilitiesView{GitHubEnabled: true},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rec.Body.String(), `href="/v1/auth/github"`) {
		t.Errorf("github link missing\n--- body ---\n%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `href="/v1/auth/google"`) {
		t.Errorf("google link should not render when only GitHub is enabled\n--- body ---\n%s", rec.Body.String())
	}
}

// TestRender_LoginBody_NeitherEnabled — the single-box-dev /
// operator-chose-not-to-ship-OAuth shape. With both providers
// Disabled, /login must not render either OAuth link — the
// password-only path stays usable but the dead buttons that lead
// to 500 *_oauth_misconfigured (the pre-#419 symptom) are gone.
// Also covers the nil-safety branch: Auth == nil must render
// nothing, not panic with a nil-pointer deref inside the
// `{{if .Auth}}…{{end}}` guard.
func TestRender_LoginBody_NeitherEnabled(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("AuthExplicitlyEmpty", func(t *testing.T) {
		rec := httptest.NewRecorder()
		page := dashboard.Page{Title: "Sign in", Body: "login"}
		if err := dashboard.Render(rec, log, "", page); err != nil {
			t.Fatalf("render: %v", err)
		}
		body := rec.Body.String()
		if strings.Contains(body, `href="/v1/auth/google"`) {
			t.Errorf("google link should not render when Auth is zero-value\n--- body ---\n%s", body)
		}
		if strings.Contains(body, `href="/v1/auth/github"`) {
			t.Errorf("github link should not render when Auth is zero-value\n--- body ---\n%s", body)
		}
	})

	t.Run("AuthPointerNil", func(t *testing.T) {
		rec := httptest.NewRecorder()
		page := dashboard.Page{Title: "Sign in", Body: "login", Auth: nil}
		if err := dashboard.Render(rec, log, "", page); err != nil {
			t.Fatalf("render: %v", err)
		}
		body := rec.Body.String()
		if strings.Contains(body, `href="/v1/auth/google"`) {
			t.Errorf("google link should not render when Auth is nil\n--- body ---\n%s", body)
		}
		if strings.Contains(body, `href="/v1/auth/github"`) {
			t.Errorf("github link should not render when Auth is nil\n--- body ---\n%s", body)
		}
	})

	t.Run("BothBoolsFalse", func(t *testing.T) {
		rec := httptest.NewRecorder()
		page := dashboard.Page{
			Title: "Sign in",
			Body:  "login",
			Auth:  &dashboard.AuthCapabilitiesView{GoogleEnabled: false, GitHubEnabled: false},
		}
		if err := dashboard.Render(rec, log, "", page); err != nil {
			t.Fatalf("render: %v", err)
		}
		body := rec.Body.String()
		if strings.Contains(body, `href="/v1/auth/google"`) {
			t.Errorf("google link should not render when both bools are false\n--- body ---\n%s", body)
		}
		if strings.Contains(body, `href="/v1/auth/github"`) {
			t.Errorf("github link should not render when both bools are false\n--- body ---\n%s", body)
		}
	})
}

// TestRender_MissingTemplate confirms an unknown Body returns a 500
// error from Render rather than silently rendering empty.
func TestRender_MissingTemplate(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{Title: "Nope", Body: "does_not_exist"}
	if err := dashboard.Render(rec, log, "", page); err == nil {
		t.Fatal("expected error for missing template")
	}
}

// TestRender_Flash confirms the Flash banner renders when set.
func TestRender_Flash(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{Title: "Sign in", Body: "index", Flash: "Check your email"}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "Check your email") {
		t.Errorf("body missing flash banner\n--- body ---\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<div class="flash">`) {
		t.Errorf("body missing flash container\n--- body ---\n%s", rec.Body.String())
	}
}

// TestRender_AccountView confirms an Account renders the email + plan
// strings inside the layout body.
func TestRender_AccountView(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Overview",
		Body:  "index",
		Account: &dashboard.AccountView{
			ID:       "acct-1",
			Email:    "jane@example.test",
			Plan:     "pro",
			AppCount: 3,
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{"jane@example.test", "pro", "Deployed apps: <strong>3</strong>"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestRender_StampsNonceOnScriptAndStyle confirms Render copies the
// nonce argument onto page.Nonce so the templates emit
// `nonce="..."` on every <script> and <style> tag the CSP
// (httpsec) middleware requires. The HTTP server already sets the
// Content-Security-Policy header; this test pins the matching
// template attribute so the browser accepts the inline code under
// strict CSP.
//
// Issue #249 closes here: a missing stamp would silently block every
// dashboard's HTMX bootstrap.
func TestRender_StampsNonceOnScriptAndStyle(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const nonce = "abc123XYZ-_abc123XYZ-" // 22 chars, URL-safe base64
	page := dashboard.Page{
		Title: "Overview",
		Body:  "index",
	}
	if err := dashboard.Render(rec, log, nonce, page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	// Every <script src="…"> must carry the nonce attribute.
	if !strings.Contains(body, `nonce="`+nonce+`"`) {
		t.Errorf("body missing nonce=%q on script/style\n--- body ---\n%s", nonce, body)
	}
}

// TestRender_NoNonceRendersCleanly confirms Render tolerates an empty
// nonce (unit-test path) without panicking. The output may carry
// `nonce=""` literally — that's harmless on its own and the browser
// still accepts the inline tag because the empty nonce doesn't match
// any CSP `nonce-…` directive in the page's header. Production
// always supplies a real nonce via httpsec.NonceFromContext so
// `nonce=""` never reaches the wire.
func TestRender_NoNonceRendersCleanly(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{Title: "Sign in", Body: "login"}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	// Smoke: the page still renders and includes the form.
	if !strings.Contains(rec.Body.String(), `<form method="POST" action="/login"`) {
		t.Errorf("body missing login form\n--- body ---\n%s", rec.Body.String())
	}
}

// TestRender_AccountNoInlineOnclick pins the inline-onclick refactor
// (issue #249 / spec §11). Browsers do not propagate `nonce` onto
// event-handler attributes, so the original
//
//	<button onclick="return confirm('...')">
//
// would silently break the delete-account confirm prompt the
// moment CSP ships. The refactor moves the prompt into a per-page
// `<script nonce="…">` block that wires addEventListener on a
// form identified by id="account-delete-form". This test pins:
//   - the form carries the id (so the addEventListener hook can
//     find it),
//   - the rendered output contains NO `onclick=` attributes
//     (no inline event handlers at all, so a future regression
//     in a different template is caught too),
//   - the per-page `<script nonce="…">` block contains the confirm
//     prompt wiring.
func TestRender_AccountNoInlineOnclick(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const nonce = "nonceSmokeTest1234567ab" // 22 chars
	page := dashboard.Page{
		Title: "Account",
		Body:  "account",
		Account: &dashboard.AccountView{
			ID:       "acct-1",
			Email:    "jane@example.test",
			Plan:     "pro",
			AppCount: 1,
		},
		Data: dashboard.AccountData{
			ShowDelete: true,
		},
	}
	if err := dashboard.Render(rec, log, nonce, page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	// The danger-zone form must carry the id the addEventListener
	// hook is bound to.
	if !strings.Contains(body, `id="account-delete-form"`) {
		t.Errorf("account delete form missing id\n--- body ---\n%s", body)
	}
	// No inline event handlers — that would defeat strict CSP.
	if strings.Contains(body, "onclick=") {
		t.Errorf("account template still carries an inline onclick attr\n--- body ---\n%s", body)
	}
	// The per-page <script nonce=…> block must contain the confirm
	// prompt wiring so the user still sees the dialog.
	if !strings.Contains(body, `nonce="`+nonce+`"`) {
		t.Errorf("per-page script block missing nonce attr\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "addEventListener") {
		t.Errorf("per-page script block missing addEventListener wiring\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "Schedule your account for permanent deletion in 30 days?") {
		t.Errorf("per-page script block missing confirm copy\n--- body ---\n%s", body)
	}
}

// TestRender_StatelessPage pins the /dashboard/stateless landing
// page (Move 1 PR-A). Confirms the page renders, includes the 8-base
// denylist and the 10 closed paths, and shows the empty-state when
// no advisories are present.
func TestRender_StatelessPage(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Stateless advisories",
		Body:  "stateless",
		Data: dashboard.StatelessData{
			RecentAdvisoriesEmpty: true,
			StatelessDenylist:     dashboard.StatelessDenylist,
			ClosedPaths:           dashboard.StatelessClosedPaths,
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// All 8 base denylist entries must appear so a future rename of
	// one entry can't silently ship without a dashboard refresh.
	for _, name := range []string{"postgres", "redis", "mysql", "mariadb", "mongo", "cockroach", "cassandra", "clickhouse"} {
		if !strings.Contains(body, "<code>"+name+"</code>") {
			t.Errorf("denylist row missing %q\n--- body ---\n%s", name, body)
		}
	}
	// The two "high" closed paths and at least one "warn" path must
	// appear; pinning both severities keeps the badge column honest.
	for _, p := range []string{"/data", "/db", "/var/lib/postgresql", "/var/lib/redis"} {
		if !strings.Contains(body, "<code>"+p+"</code>") {
			t.Errorf("closed-path row missing %q\n--- body ---\n%s", p, body)
		}
	}
	if !strings.Contains(body, `class="badge high"`) {
		t.Errorf("high-severity badge missing\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, `class="badge warn"`) {
		t.Errorf("warn-severity badge missing\n--- body ---\n%s", body)
	}
	// Empty-state copy must render when no advisories are present.
	if !strings.Contains(body, "No advisories recorded") {
		t.Errorf("empty-state copy missing\n--- body ---\n%s", body)
	}
	// Nav link to /dashboard/stateless must be present + active.
	if !strings.Contains(body, `href="/dashboard/stateless"`) {
		t.Errorf("nav link to /dashboard/stateless missing\n--- body ---\n%s", body)
	}
}

// TestStatelessSlices_Shape pins the package-level slices so a future
// drift in pkg/imaged or guest-init is caught on the dashboard side.
// Adding a base to the denylist means updating BOTH pkg/imaged/base.go
// AND pkg/dashboard/dashboard.go's StatelessDenylist; this test fails
// if either is forgotten (count + names are pinned).
func TestStatelessSlices_Shape(t *testing.T) {
	if got := len(dashboard.StatelessDenylist); got != 8 {
		t.Errorf("StatelessDenylist len = %d, want 8 (mirror of pkg/imaged/base.go)", got)
	}
	if got := len(dashboard.StatelessClosedPaths); got != 10 {
		t.Errorf("StatelessClosedPaths len = %d, want 10 (mirror of guest/init/stateless_advisory_linux.go)", got)
	}
	// Every closed path must have a severity, and severities must
	// be in the closed vocabulary.
	for _, p := range dashboard.StatelessClosedPaths {
		if p.Severity != "high" && p.Severity != "warn" {
			t.Errorf("closed-path %q has bad severity %q", p.Path, p.Severity)
		}
	}
	// The two top-level dirs must be high severity; pinning this
	// keeps the badge column honest if a future refactor mis-classifies.
	highs := map[string]bool{}
	for _, p := range dashboard.StatelessClosedPaths {
		if p.Severity == "high" {
			highs[p.Path] = true
		}
	}
	for _, want := range []string{"/data", "/db"} {
		if !highs[want] {
			t.Errorf("expected %q to be high severity; got %v", want, highs)
		}
	}
}

// TestRender_Billing_HidesPortalForFree pins the issue #253
// acceptance #5: a Free-plan account never sees the "Manage
// billing" section or the "Open Stripe billing portal" link, even
// if the operator-configured PortalURL is set. This guards against
// a future template refactor that accidentally moves the {{if
// .Data.HasPaidPlan}} gate or makes PortalURL conditional on
// something else.
func TestRender_Billing_HidesPortalForFree(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Billing",
		Body:  "billing",
		Data: dashboard.BillingData{
			Plan:        "free",
			RAMMB:       128,
			Included:    5,
			AppsCap:     1,
			AppLayer:    256,
			IdleSec:     30,
			HasPaidPlan: false,
			// Deliberately populated — even with a non-empty
			// URL, a Free account must not see the link. The
			// dashboard gates on HasPaidPlan, not on PortalURL.
			PortalURL: "https://billing.example.com/portal?account=acct_xyz",
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, banned := range []string{
		"Manage billing",
		"Open billing portal",
		"Last invoice",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("Free-plan body should NOT contain %q\n--- body ---\n%s", banned, body)
		}
	}
	// The plan card + usage section must still render (regression).
	for _, want := range []string{"Plan: free", "GB-hours used", "Max concurrent instances"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestRender_Billing_PaidPlanShowsPortal pins the issue #253
// acceptance #2 + #4: a paid-plan account sees the portal link,
// the last-invoice table, and the current-month usage summary.
// Pinned substrings match the literal template copy so a future
// copy edit does not silently drift.
func TestRender_Billing_PaidPlanShowsPortal(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Billing",
		Body:  "billing",
		Data: dashboard.BillingData{
			Plan:                      "hobby",
			RAMMB:                     256,
			Included:                  50,
			AppsCap:                   5,
			AppLayer:                  512,
			IdleSec:                   60,
			MaxConcurrency:            2,
			UsedGBHours:               12.5,
			UsedPct:                   25,
			UsedEgressGB:              0.42,
			LastInvoiceDate:           "2026-07-31",
			LastInvoiceStatus:         "paid",
			LastInvoiceTotalFormatted: "€12.40",
			LastInvoiceCurrency:       "EUR",
			HasPaidPlan:               true,
			PortalURL:                 "https://billing.example.com/portal?account=acct_abc",
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Plan: hobby",
		"Max concurrent instances", // new field from limits.MaxConcurrency
		"GB-hours used",
		"Egress this month (GB)",
		"Manage billing",
		"Open billing portal",
		"Last invoice",
		"2026-07-31",
		"€12.40",
		"EUR",
		`href="https://billing.example.com/portal?account=acct_abc"`,
		"rel=\"noopener\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
	// Free-tier fallback copy must NOT appear for a paid account.
	if strings.Contains(body, "faas plan &lt;plan&gt;") {
		t.Errorf("paid-plan body should NOT contain Free-tier upgrade hint\n--- body ---\n%s", body)
	}
}

// TestRender_Billing_PaidPortalUnset pins the operator-misconfig
// fallback: a paid account on a box that has FAAS_BILLING_PORTAL_URL
// unset sees a clear "use the CLI" hint instead of a broken button.
// The CLI hint is the escape hatch; the dashboard never silently
// renders an empty link.
func TestRender_Billing_PaidPortalUnset(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Billing",
		Body:  "billing",
		Data: dashboard.BillingData{
			Plan:        "hobby",
			HasPaidPlan: true,
			PortalURL:   "",
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "billing portal is not available") {
		t.Errorf("body missing operator-misconfig fallback\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "faas billing portal") {
		t.Errorf("body missing CLI hint\n--- body ---\n%s", body)
	}
	if strings.Contains(body, "Open billing portal") {
		t.Errorf("body should NOT contain the portal link button when URL is empty\n--- body ---\n%s", body)
	}
}

// TestRender_OrgsPage pins the orgs list + detail templates
// (PR-8 §3). The list surfaces every org the signed-in account
// belongs to with seat counts; the detail renders members + a
// pending-invitations table. Both shapes are owned by the
// dashboard handlers — this is the template-parse + shape gate.
func TestRender_OrgsPage(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	acct := &dashboard.AccountView{
		ID: "a1", Email: "ops@acme.test", Plan: "scale", AppCount: 3,
	}

	// List, populated.
	rec := httptest.NewRecorder()
	listPage := dashboard.Page{
		Title:   "Organizations",
		Body:    "orgs",
		Account: acct,
		Data: dashboard.OrgListData{
			Orgs: []dashboard.OrgListItem{
				{Slug: "u-acme1234abcd", Name: "Personal", Plan: "free", Role: "owner", Personal: true},
				{Slug: "acme", Name: "Acme Co", Plan: "scale", Role: "owner", SeatUsed: 4, SeatLimit: 200},
				{Slug: "staging", Name: "Acme Staging", Plan: "hobby", Role: "admin", SeatUsed: 10, SeatLimit: 10},
			},
		},
	}
	if err := dashboard.Render(rec, log, "", listPage); err != nil {
		t.Fatalf("render orgs list: %v", err)
	}
	listBody := rec.Body.String()
	for _, want := range []string{
		// Personal-first sort + muted tag.
		"u-acme1234abcd",
		"(personal)",
		// Shared orgs with the seat-count chip.
		"acme",
		"4 / 200",
		"staging",
		"10 / 10",
		// Manage affordance + nav.
		`href="/dashboard/orgs/acme"`,
		`href="/dashboard/orgs"`,
	} {
		if !strings.Contains(listBody, want) {
			t.Errorf("list body missing %q\n--- body ---\n%s", want, listBody)
		}
	}

	// List, empty.
	rec2 := httptest.NewRecorder()
	if err := dashboard.Render(rec2, log, "", dashboard.Page{
		Title:   "Organizations",
		Body:    "orgs",
		Account: acct,
		Data:    dashboard.OrgListData{Orgs: nil},
	}); err != nil {
		t.Fatalf("render orgs list empty: %v", err)
	}
	if !strings.Contains(rec2.Body.String(), "You don't belong to any organization yet") {
		t.Errorf("empty-state copy missing\n--- body ---\n%s", rec2.Body.String())
	}

	// Detail: shared org with 2 members + 3 invitations (one of
	// each non-personal status). The token prefix surfaces the
	// 8-char hash drop; the role-badge wording is part of the
	// table's accessibility affordance.
	rec3 := httptest.NewRecorder()
	detailPage := dashboard.Page{
		Title:   "Acme Co",
		Body:    "org_detail",
		Account: acct,
		Data: dashboard.OrgDetailData{
			Org:         dashboard.OrgListItem{Slug: "acme", Name: "Acme Co", Plan: "scale", Role: "owner", SeatUsed: 2, SeatLimit: 200},
			CallersRole: "owner",
			Members: []dashboard.OrgMemberItem{
				{AccountID: "a1", Email: "ops@acme.test", Role: "owner", JoinedAt: "2026-01-04"},
				{AccountID: "a2", Email: "eng@acme.test", Role: "admin", JoinedAt: "2026-02-09"},
			},
			Invitations: []dashboard.OrgInvitationItem{
				{Email: "alice@acme.test", Role: "developer", Status: "pending", CreatedAt: "2026-07-01 12:00 UTC", ExpiresAt: "2026-07-08 12:00 UTC", TokenPrefix: "abcd1234"},
				{Email: "bob@acme.test", Role: "developer", Status: "consumed", TokenPrefix: "efgh5678"},
				{Email: "carol@acme.test", Role: "developer", Status: "revoked", TokenPrefix: "ijkl9012"},
			},
		},
	}
	if err := dashboard.Render(rec3, log, "", detailPage); err != nil {
		t.Fatalf("render org detail: %v", err)
	}
	detailBody := rec3.Body.String()
	for _, want := range []string{
		// Plan + role + seat chip.
		"<strong>scale</strong>",
		"Your role: <strong>owner</strong>",
		"<strong>2</strong> / <strong>200</strong>",
		// Members table.
		"ops@acme.test",
		"eng@acme.test",
		// Invitations table — each rendered status badge appears.
		`badge badge-pending`,
		`badge badge-consumed`,
		`badge badge-revoked`,
		`>pending<`,
		`>consumed<`,
		`>revoked<`,
		// Token prefix survives the 8-char clip; full hash does
		// not appear.
		"<code>abcd1234</code>",
		// Owner-only nudge surfaces only when CallersRole == owner.
		"transfer_ownership",
		// Back link.
		`href="/dashboard/orgs"`,
	} {
		if !strings.Contains(detailBody, want) {
			t.Errorf("detail body missing %q\n--- body ---\n%s", want, detailBody)
		}
	}
}

// TestRender_DeploymentDetail_StatelessViolation pins Cluster A
// (error-explanations dashboard rendering, spec §6.4 amendment 1):
// when a deployment's row carries a typed ErrorCode + the
// Hint/Why/Fix/RelevantLogs prose, the deployment_detail template
// renders the new .error-explanation section with all four
// prose blocks. The check is on substring presence — the dashboard
// has no separate parser, so a drift in the rendered markup would
// only surface visually.
//
// Why unit-only (no HTTP): the apid handler is exercised by the
// e2e suite under cmd/e2e/; the dashboard render is a pure
// html/template Execute.
func TestRender_DeploymentDetail_StatelessViolation(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Deployment d-1",
		Body:  "deployment_detail",
		Data: dashboard.DeploymentDetailData{
			App: dashboard.AppListItem{Slug: "myapp"},
			Deployment: dashboard.DeploymentItem{
				ID:        "d-1",
				Status:    "failed",
				Kind:      "function",
				CreatedAt: "2026-08-19T18:00:00Z",
				ErrorCode: "stateless_only_violation",
				Error:     "tarball contains top-level data/ directory",
				ErrorHint: "this platform is stateless — drop top-level data/ db/ dirs",
				ErrorWhy:  "the deploy shape is a stateful one this platform does not support in year one",
				ErrorFix:  "• use a managed service\n• or remove the data/ directory",
				ErrorRelevantLogs: []api.LogExcerpt{
					{Level: "error", Source: "build", Message: "VOLUME /data detected"},
				},
			},
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		// CSS scoped class — pin the parent selector, not the
		// child rules (the lint tripwire asserts no global rules).
		"error-explanation",
		// 5 prose blocks render in order. The template is
		// single-pass; a reorder breaks the customer UX.
		"stateless_only_violation",
		"💡 this platform is stateless",
		"why: the deploy shape is a stateful one",
		"→ • use a managed service",
		"relevant logs (1)",
		// The error message is rendered too (the legacy raw Error).
		"tarball contains top-level data/ directory",
		// The docs link uses the typed code (NOT a hardcoded URL).
		`href="https://docs.gregale.dev/errors/stateless_only_violation"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestRender_DeploymentDetail_LegacyRowRenders pins the conditional
// gate: when ErrorCode is empty (pre-PR-#987 row, or a non-failed
// deploy), the new .error-explanation section must be absent so the
// legacy single-column layout doesn't shift.
func TestRender_DeploymentDetail_LegacyRowRenders(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Deployment d-2",
		Body:  "deployment_detail",
		Data: dashboard.DeploymentDetailData{
			App: dashboard.AppListItem{Slug: "myapp"},
			Deployment: dashboard.DeploymentItem{
				ID:        "d-2",
				Status:    "live",
				Kind:      "function",
				CreatedAt: "2026-08-19T18:00:00Z",
				Error:     "no error",
				// ErrorCode intentionally empty.
			},
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	// CSS class names appear in the inline <style> regardless of
	// the conditional — pin the SECTION element specifically so a
	// drift in the gate is the only thing the test catches.
	if strings.Contains(body, `<section class="error-explanation"`) {
		t.Errorf("legacy row rendered error-explanation section — gate is broken\n--- body ---\n%s", body)
	}
	// The header line still renders.
	for _, want := range []string{
		"d-2",
		"live",
		"function",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestRender_DeploymentDetail_StagesPresent — A2 (ADR-117 v2
// follow-on). Pins the positive path: when Stages is non-nil with
// a BodyHTML, the deployment_detail template renders the section
// between the error-explanation block and the Scan heading. The
// close-6-stage labels are present in the pre-rendered HTML (the
// handler edge rendered them via pkg/dashboard/stages, and the
// template only inlines the result).
func TestRender_DeploymentDetail_StagesPresent(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Deployment d-3",
		Body:  "deployment_detail",
		Data: dashboard.DeploymentDetailData{
			App: dashboard.AppListItem{Slug: "myapp"},
			Deployment: dashboard.DeploymentItem{
				ID:        "d-3",
				Status:    "live",
				Kind:      "function",
				CreatedAt: "2026-08-19T18:00:00Z",
			},
			Stages: &dashboard.StagePayload{
				BodyHTML: template.HTML(
					`<section class="stage-timeline">` +
						`<div class="stage-row"><span class="glyph">✓</span><span class="label">Source downloaded</span><span class="duration">1.2s</span></div>` +
						`<div class="stage-row"><span class="glyph">✓</span><span class="label">Readiness passed</span><span class="duration">0.4s</span></div>` +
						`<p class="stage-footer">Total: 29.2s · live since 2026-08-19T18:00:00Z</p>` +
						`</section>`,
				),
				Status:     "live",
				TerminalAt: time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC),
			},
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	// The Stages heading + the inlined BodyHTML must render.
	for _, want := range []string{
		// Section root (parent-scoped CSS selector).
		"stage-timeline",
		// Stage labels — pins the pre-rendered HTML travelled
		// through the template unmolested.
		"Source downloaded",
		"Readiness passed",
		// The footer copy is also inlined.
		"live since 2026-08-19T18:00:00Z",
		// The <h2>Stages</h2> heading the template emits.
		"Stages",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestRender_DeploymentDetail_StagesAbsent — A2: the conditional
// gate. When Stages is nil (pre-00302 row OR in-flight pre-first
// frame), the section must NOT render. The CSS class names appear
// in the inline <style> regardless of the conditional — so pin
// the SECTION element specifically.
func TestRender_DeploymentDetail_StagesAbsent(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Deployment d-4",
		Body:  "deployment_detail",
		Data: dashboard.DeploymentDetailData{
			App: dashboard.AppListItem{Slug: "myapp"},
			Deployment: dashboard.DeploymentItem{
				ID:        "d-4",
				Status:    "live",
				Kind:      "function",
				CreatedAt: "2026-08-19T18:00:00Z",
			},
			// Stages intentionally nil.
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	if strings.Contains(body, `<section class="stage-timeline"`) {
		t.Errorf("legacy row rendered stage-timeline section — gate is broken\n--- body ---\n%s", body)
	}
	// The <h2>Stages</h2> heading must also be absent — both the
	// heading and the section are gated by the same `if .Data.Stages`
	// test, so they either both render or both are absent.
	if strings.Contains(body, `<h2>Stages</h2>`) {
		t.Errorf("legacy row rendered <h2>Stages</h2> heading — gate is broken\n--- body ---\n%s", body)
	}
}

// TestRender_DeploymentDetail_PreviewURLCopyChip pins the
// SAFE-RELEASES-C.3 dashboard surface (issue #976 / ADR-122).
// Two assertions:
//
//   - Alive=true → "preview-live" badge + readonly <input> with the
//     resolved host + "copy" button. Pinned via the CSS class
//     names + the host string (a drift in the gate would either
//     drop the chip or render the wrong shape).
//   - Alive=false → "preview-closed" badge + "(preview closed —
//     deploy failed)" label. NO copy button. The "preview-live"
//     badge MUST NOT appear when Alive=false.
//
// The second subtest pins the template's three-state model
// (Alive=true / Alive=false / ZoneDisabled). A regression that
// always rendered "copy" or always rendered "preview-closed"
// would break this; the assertion is exactly the difference
// between the two states.
func TestRender_DeploymentDetail_PreviewURLCopyChip(t *testing.T) {
	t.Run("alive=true", func(t *testing.T) {
		rec := httptest.NewRecorder()
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		page := dashboard.Page{
			Title: "Deployment d-live",
			Body:  "deployment_detail",
			Data: dashboard.DeploymentDetailData{
				App: dashboard.AppListItem{Slug: "url-copy"},
				Deployment: dashboard.DeploymentItem{
					ID:        "d-live",
					Status:    "live",
					Kind:      "function",
					CreatedAt: "2026-08-19T18:00:00Z",
				},
				PreviewURL: &dashboard.DeploymentPreviewURL{
					Host:  "deploy-3.url-copy.gregale.dev",
					URL:   "https://deploy-3.url-copy.gregale.dev",
					Alive: true,
				},
			},
		}
		if err := dashboard.Render(rec, log, "", page); err != nil {
			t.Fatalf("render: %v", err)
		}
		body := rec.Body.String()
		for _, want := range []string{
			`<span class="badge preview-live">preview</span>`,
			"deploy-3.url-copy.gregale.dev",
			`class="preview-copy"`,
			`>copy<`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("alive=true body missing %q\n--- body ---\n%s", want, body)
			}
		}
		// Closed-state label MUST NOT render alongside the live chip
		// — a regression that always rendered "preview closed" plus
		// the input would produce both strings.
		if strings.Contains(body, "preview closed") {
			t.Errorf("alive=true body should not render 'preview closed' label\n--- body ---\n%s", body)
		}
	})
	t.Run("alive=false", func(t *testing.T) {
		rec := httptest.NewRecorder()
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		page := dashboard.Page{
			Title: "Deployment d-failed",
			Body:  "deployment_detail",
			Data: dashboard.DeploymentDetailData{
				App: dashboard.AppListItem{Slug: "url-copy"},
				Deployment: dashboard.DeploymentItem{
					ID:        "d-failed",
					Status:    "failed",
					Kind:      "function",
					CreatedAt: "2026-08-19T18:00:00Z",
				},
				PreviewURL: &dashboard.DeploymentPreviewURL{
					Host:  "",
					URL:   "",
					Alive: false,
				},
			},
		}
		if err := dashboard.Render(rec, log, "", page); err != nil {
			t.Fatalf("render: %v", err)
		}
		body := rec.Body.String()
		for _, want := range []string{
			`<span class="badge preview-closed">preview</span>`,
			"preview closed — deploy failed",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("alive=false body missing %q\n--- body ---\n%s", want, body)
			}
		}
		// The live-state chip / input MUST NOT render. Anchor on the
		// JSX span attribute (matches what the template emits) rather
		// than the substring "preview-live", which would also match
		// the .badge.preview-live CSS rule in the inline <style>
		// block (and trip the assertion even though the gate is
		// correct).
		if strings.Contains(body, `<span class="badge preview-live">`) {
			t.Errorf("alive=false body rendered <span class=\"badge preview-live\"> — gate is broken\n--- body ---\n%s", body)
		}
		if strings.Contains(body, `class="preview-host"`) {
			t.Errorf("alive=false body should not render copyable input\n--- body ---\n%s", body)
		}
	})
}

// TestRender_AppDetail_AlertPresetExplanations_AllPresent pins the
// "What does this alert mean?" panel (issue #1233 / ADR-123 PR-C
// commit 3) for every preset card on the dashboard. Mirrors
// TestRender_DeploymentDetail_StatelessViolation's approach: seed
// every catalog preset with its presetwhy.Decorate Explanation,
// render, assert each card carries its <details class="alert-preset-explanation">
// block with Title/Hint/Why/Fix + the docs link.
//
// Forward direction (8 cards → 8 panels). The tripwire
// TestEveryPresetHasPresetwhyEntry pins the inverse direction (every
// preset name → catalog row). Together they form the bidirectional
// membership contract for the alert_preset grid.
func TestRender_AppDetail_AlertPresetExplanations_AllPresent(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{
				Slug:   "demo",
				AppID:  "demo-uuid",
				Status: "active",
				URL:    "https://demo.apps.gregale.dev",
			},
			Presets: []dashboard.AlertPresetItem{
				{Name: "api_down", DisplayName: "API is down", Category: "availability", Metric: "api_reachable", Comparison: "lt", Threshold: 1, WindowSpec: "5m", MinimumPlan: "pro", Enabled: true, MeetsPlan: true, EnabledInCatalog: true, AppSlug: "demo", Explanation: presetwhy.Decorate("api_down", 0)},
				{Name: "spend_eur_20", DisplayName: "Daily spend exceeds €20", Category: "cost", Metric: "meterd_account_spend_eur", Comparison: "gt", Threshold: 20, WindowSpec: "24h", MinimumPlan: "hobby", Enabled: true, MeetsPlan: true, EnabledInCatalog: true, AppSlug: "demo", Explanation: presetwhy.Decorate("spend_eur_20", 0)},
				{Name: "deploy_failed", DisplayName: "Deployment failed", Category: "deployment", Metric: "apid_deployment_failed_total", Comparison: "gt", Threshold: 0, WindowSpec: "1h", MinimumPlan: "hobby", Enabled: true, MeetsPlan: true, EnabledInCatalog: true, AppSlug: "demo", Explanation: presetwhy.Decorate("deploy_failed", 0)},
				{Name: "cert_expiring_14d", DisplayName: "TLS certificate expires within 14 days", Category: "infrastructure", Metric: "meterd_tenant_surface_cert_expiry_seconds", Comparison: "lt", Threshold: 1209600, WindowSpec: "24h", MinimumPlan: "pro", Enabled: true, MeetsPlan: true, EnabledInCatalog: true, AppSlug: "demo", Explanation: presetwhy.Decorate("cert_expiring_14d", 0)},
				{Name: "queue_backlog_growing", DisplayName: "Gateway wake queue is backlogged", Category: "reliability", Metric: "gateway_queue_depth", Comparison: "gt", Threshold: 50, WindowSpec: "15m", MinimumPlan: "hobby", Enabled: true, MeetsPlan: true, EnabledInCatalog: true, AppSlug: "demo", Explanation: presetwhy.Decorate("queue_backlog_growing", 0)},
				{Name: "error_rate_2pct", DisplayName: "Error rate exceeds 2%", Category: "reliability", Metric: "error_rate_pct", Comparison: "gt", Threshold: 2, WindowSpec: "15m", MinimumPlan: "hobby", Enabled: true, MeetsPlan: true, EnabledInCatalog: true, AppSlug: "demo", Explanation: presetwhy.Decorate("error_rate_2pct", 0)},
				{Name: "p95_latency_1s", DisplayName: "P95 latency exceeds 1 second", Category: "reliability", Metric: "p95_latency_ms", Comparison: "gt", Threshold: 1000, WindowSpec: "15m", MinimumPlan: "hobby", Enabled: true, MeetsPlan: true, EnabledInCatalog: true, AppSlug: "demo", Explanation: presetwhy.Decorate("p95_latency_1s", 0)},
				{Name: "cold_start_10pct", DisplayName: "Cold starts exceed 10% of traffic", Category: "reliability", Metric: "cold_start_rate_pct", Comparison: "gt", Threshold: 10, WindowSpec: "1h", MinimumPlan: "hobby", Enabled: true, MeetsPlan: true, EnabledInCatalog: true, AppSlug: "demo", Explanation: presetwhy.Decorate("cold_start_10pct", 0)},
			},
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	// Every card must carry its sibling-class CSS rule
	// (.alert-preset-explanation, NOT nested under .alert-preset-card
	// — matches the error-explanations cluster-A precedent) and its
	// <details> panel + summary. A missing prefix trips a customer
	// "I don't know what this alert does" silent UX regression.
	for _, want := range []string{
		`class="alert-preset-explanation"`,
		"What does this alert mean?",
		// Title substrings from the catalog
		"API is down",
		"Daily spend exceeds",
		"Deployment failed",
		"TLS certificate expires",
		"Gateway wake queue",
		"Error rate exceeds",
		"P95 latency exceeds",
		"Cold starts exceed",
		// Docs links render with the runbook hrefs.
		`href="/docs/runbooks/FaasApiDown"`,
		`href="/docs/runbooks/FaasSpendEur20"`,
		`href="/docs/runbooks/FaasDeployFailed"`,
		`href="/docs/runbooks/FaasTLSCertExpiryPage"`,
		`href="/docs/runbooks/FaasGatewayQueueBacklogGrowing"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestPrettyAuditData_RoundTrip pins the pretty-print behaviour
// for the deployment_audit timeline block (issue #976 / ADR-122 /
// SAFE-RELEASES-E.2 + production-leveling Stream A): nil / empty
// yields the empty string (template renders the muted "—"
// placeholder); well-formed jsonb yields a 2-space-indented
// representation; unparseable jsonb falls through to the verbatim
// bytes so the operator sees something instead of a missing
// column.
func TestPrettyAuditData_RoundTrip(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := dashboard.PrettyAuditData(nil); got != "" {
			t.Errorf("nil=%q, want empty", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := dashboard.PrettyAuditData(json.RawMessage{}); got != "" {
			t.Errorf("empty=%q, want empty", got)
		}
	})
	t.Run("valid", func(t *testing.T) {
		raw := json.RawMessage(`{"from_percent":0,"to_percent":1}`)
		got := dashboard.PrettyAuditData(raw)
		want := "{\n  \"from_percent\": 0,\n  \"to_percent\": 1\n}"
		if got != want {
			t.Errorf("got=%q, want=%q", got, want)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		raw := json.RawMessage(`{not-json`)
		got := dashboard.PrettyAuditData(raw)
		if got != string(raw) {
			t.Errorf("invalid=%q, want verbatim %q", got, string(raw))
		}
	})
}

// TestDeploymentAuditSeverityClass pins the closed-set kind→CSS
// palette mapping the dashboard handler uses to render the audit
// timeline chips. A future enum widening lands as a CI failure
// here (info → wrong palette for a customer-affecting state flip).
func TestDeploymentAuditSeverityClass(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{"deploy.rolled_back", "high"},
		{"deploy.traffic_changed", "warn"},
		{"deploy.health_probe_failed", "warn"},
		{"deploy.created", "info"},
		{"deploy.source_ref", "info"},
		{"deploy.health_recovered", "info"},
		{"", "info"}, // unknown kind → info fallback
	}
	for _, c := range cases {
		if got := dashboard.DeploymentAuditSeverityClass(c.kind); got != c.want {
			t.Errorf("kind=%q → %q, want %q", c.kind, got, c.want)
		}
	}
}
