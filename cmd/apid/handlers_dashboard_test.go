package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// newAuthedDashboardServer seeds an account into a MemStore, builds a
// server with a real session.Manager, mints a cookie for that
// account, and returns (handler, cookie) so the authed tests can
// hit the gated /dashboard/* routes.
//
// Tests that intentionally probe the unauthenticated surface
// (TestDashboardHandler_LoginPage + TestDashboardHandler_RecoversFromPanic
// use the raw chain) call newServer() directly.
//
// The pre-PR-077 cookie carries env.StepUpAt = time.Time{} (the
// envelope's omitempty wire form means a cookie minted without
// the step-up variant never carries the field). After the
// IAM-hardening-mega-PR (logical change 6, ADR-077) lands, the
// dashboard's requireStepUpHandler gate reads env.StepUpAt via
// StepUpFrom(r); a pre-PR cookie trips reason="missing" and 403s.
// Tests that drive step-up-gated routes (set-password, delete,
// restore) must use newSteppedUpDashboardServer instead, which
// re-issues the cookie via IssueWithSessionAndBindingHashAndStepUp
// with StepUpAt = time.Now().
func newAuthedDashboardServer(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	h, cookie, _, _ := newAuthedDashboardServerFull(t)
	return h, cookie
}

// newAuthedDashboardServerFull returns the handler, the authed
// faas_sid cookie, the underlying MemStore, and the session.Manager
// so tests that need to re-issue the cookie (e.g. to add a step-up
// stamp, or to bind a sessions row to a new IP+UA) can do so without
// re-seeding the account.
func newAuthedDashboardServerFull(t *testing.T) (http.Handler, *http.Cookie, *state.MemStore, *session.Manager) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*60_000_000_000, "")
	return srv.handler(), &http.Cookie{Name: sessionCookie, Value: cookie}, store, mgr
}

// newSteppedUpDashboardServer re-issues the authed cookie with
// StepUpAt = time.Now() so the dashboard's requireStepUpHandler
// gate passes. Also creates a sessions row so
// IssueWithSessionAndBindingHashAndStepUp has a real sid to bind.
// Returns the same (handler, cookie) shape as newAuthedDashboardServer
// so the existing test bodies drop in unchanged.
func newSteppedUpDashboardServer(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	h, _, store, mgr := newAuthedDashboardServerFull(t)
	// Find the just-issued account's only row.
	accts, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	// IssueWithSessionAndBindingHashAndStepUp requires a real
	// sid + accountID + binding hash. Create the session row
	// first; the binding hash is empty (unix-socket / no-UA
	// path) so the cross-check at RequireSessionCookie step 3.5
	// is a no-op.
	sid := "stepped-up-sid"
	if _, err := store.CreateSession(t.Context(), sid, accts.ID, "192.0.2.10", "stepped-up-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cookie, err := mgr.IssueWithSessionAndBindingHashAndStepUp(sid, accts.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("issue stepped-up cookie: %v", err)
	}
	return h, &http.Cookie{Name: sessionCookie, Value: cookie}
}

// TestDashboardHandler_RendersIndex confirms an authenticated
// GET /dashboard/ returns 200 + the layout chrome (HTMX script, nav
// links) and the body from the index template.
func TestDashboardHandler_RendersIndex(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"htmx.org@2.0.4",
		"/dashboard/",
		"Signed in as",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestDashboardHandler_LoginPage confirms GET /login renders the
// magic-link form (slice-3 wires the real flow).
func TestDashboardHandler_LoginPage(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).handler()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<form method="POST" action="/login"`) {
		t.Errorf("body missing login form\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, `name="email"`) {
		t.Errorf("body missing email input\n--- body ---\n%s", body)
	}
}

// TestDashboardHandler_GeneratesRequestID confirms every dashboard
// response carries an x-faas-request-id header. The middleware
// generates one if the client didn't supply it.
func TestDashboardHandler_GeneratesRequestID(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	rid := rec.Header().Get("x-faas-request-id")
	if rid == "" {
		t.Fatal("missing x-faas-request-id on dashboard response")
	}
	if len(rid) != 32 {
		t.Errorf("request id length = %d, want 32 (16-byte hex)", len(rid))
	}
}

// TestDashboardHandler_PropagatesInboundRequestID confirms an inbound
// x-faas-request-id round-trips on the response.
func TestDashboardHandler_PropagatesInboundRequestID(t *testing.T) {
	const inbound = "deadbeefdeadbeefdeadbeefdeadbeef"
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	r.Header.Set("x-faas-request-id", inbound)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if got := rec.Header().Get("x-faas-request-id"); got != inbound {
		t.Errorf("response id = %q, want = %q", got, inbound)
	}
}

// TestDashboardHandler_AppsList confirms GET /dashboard/apps renders
// 200 for an authed user, even when there are no apps yet (the
// empty-state CTA). Slice 4 wires the page; this is a smoke-level
// guard against regressions where the dashboardChain silently drops
// the route. Wave 0 PR-B replaced the old "No apps yet" copy with
// the §8 contract: one primary `faas deploy` quickstart + a
// "Bring your own storage" link to the external-storage docs page.
func TestDashboardHandler_AppsList(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Apps") {
		t.Errorf("body missing apps-list header\n%s", body)
	}
	// §8 contract: the empty-state CTA surfaces the deploy quickstart
	// and the storage docs link. We don't pin "faas apps create" —
	// that was the old §8 contradiction this PR fixes.
	if !strings.Contains(body, "faas deploy --template=hello-node") {
		t.Errorf("body missing deploy quickstart; got:\n%s", body)
	}
	if !strings.Contains(body, "https://docs.gregale.dev/storage") {
		t.Errorf("body missing storage docs URL; got:\n%s", body)
	}
}

// TestDashboardHandler_UsageAndBillingAndAccount probe the three
// remaining dashboard routes — usage, billing, account. Slice 4 only
// requires these to render the layout (no data assertions beyond
// "header text is there" + 200); slice 6 wires SSE live updates.
func TestDashboardHandler_OtherPages(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	for _, path := range []string{"/dashboard/usage", "/dashboard/billing", "/dashboard/account"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.AddCookie(cookie)
			srv.ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestDashboardHandler_Billing_PaidPlan is the issue #253 dashboard
// integration pin. Seeds an account that:
//   - has StripeSubscriptionItem set (so HasPaidPlan = true)
//   - has a previously issued invoice (so the "Last invoice" section renders)
//   - has current-month usage (so the "GB-hours used" panel renders)
//   - the server is wired with FAAS_BILLING_PORTAL_URL
//
// Then asserts the rendered HTML contains all four acceptance items
// in one body: the portal anchor with the substituted account_id,
// the "Last invoice" table with the formatted total, the current-month
// usage line, and the "Manage billing" heading.
//
// A parallel test, TestDashboardHandler_Billing_FreePlan, asserts the
// inverse: a Free plan must NOT render the portal section even when
// a portal URL is configured. This is the issue #253 acceptance #5.
func TestDashboardHandler_Billing_PaidPlan(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "hobby@example.com", "hobby")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(t.Context(), acct.ID, "si_test"); err != nil {
		t.Fatalf("stripe item: %v", err)
	}
	store.SeedInvoiceForTest(state.Invoice{
		AccountID:         acct.ID,
		ProviderInvoiceID: "in_test_001",
		Provider:          "stripe",
		Status:            "paid",
		TotalCents:        1240,
		Currency:          "eur",
		PeriodStart:       time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:         time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	})
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil,
		15*60_000_000_000, "")
	srv.WithBillingPortalURL(billingPortalURL)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/billing", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	srv.handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Plan: hobby",
		"Manage billing",
		"Open billing portal",
		// The {account_id} placeholder must be substituted; if a
		// future refactor breaks the template helper, this fails
		// before the click goes out.
		"https://billing.example.com/portal?account=" + acct.ID,
		"Last invoice",
		"2026-07-31",
		"€12.40",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("paid-plan body missing %q\n--- body ---\n%s", want, body)
		}
	}
	// The literal unsubstituted template MUST NOT appear — that's the
	// classic unencoded-template bug we'd otherwise ship a clickable
	// link with `{account_id}` in it.
	if strings.Contains(body, "{account_id}") {
		t.Errorf("body leaked unsubstituted {account_id} template placeholder\n--- body ---\n%s", body)
	}
}

func TestDashboardHandler_Billing_FreePlan(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "free@example.com", "free")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil,
		15*60_000_000_000, "")
	// Inject a portal URL even though the account is Free; the
	// dashboard must still gate on HasPaidPlan (issue #253
	// acceptance #5).
	srv.WithBillingPortalURL(billingPortalURL)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/billing", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	srv.handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Plan: free") {
		t.Errorf("body missing Plan: free marker\n--- body ---\n%s", body)
	}
	for _, banned := range []string{
		"Manage billing",
		"Open billing portal",
		"Last invoice",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("Free-plan body should NOT contain %q\n--- body ---\n%s", banned, body)
		}
	}
}

// TestDashboardAccountDPA_RendersMarkdown confirms the new
// session-authed DPA route (PR follow-up) renders the configured
// template inside the dashboard chrome. Sets a tmp DPA file via
// dpaPath on the server so the test does NOT depend on the production
// /etc/faas layout. The dashboard must surface the markdown text
// body (between <pre class="dpa">…</pre>) and the back-link to
// /dashboard/account. This regresses the "support@gregale.dev" gap —
// the dashboard now has a real link, not a placeholder.
func TestDashboardAccountDPA_RendersMarkdown(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "dpa@example.com", "free")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	tmp := t.TempDir()
	dpaPath := filepath.Join(tmp, "DPA.md")
	if err := os.WriteFile(dpaPath, []byte("# DPA\n\nThe operator processes your data for X."), 0o644); err != nil {
		t.Fatalf("write DPA: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*60_000_000_000, dpaPath)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/account/dpa", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	srv.handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<pre class=\"dpa\">") {
		t.Errorf("body missing <pre class=\"dpa\">; got %s", body)
	}
	if !strings.Contains(body, "# DPA") {
		t.Errorf("body missing the markdown text\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "/dashboard/account") {
		t.Errorf("body missing back-link to /dashboard/account\n%s", body)
	}
}

// is caught by Recovery middleware and rendered as a 500 RFC 7807
// problem. This intentionally uses a raw panicHandler — it does NOT
// hit the dashboard route, so no session cookie is required; it
// validates the middleware chain itself.
func TestDashboardHandler_RecoversFromPanic(t *testing.T) {
	panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("template boom")
	})
	var h http.Handler = panicHandler
	h = middleware.RequestID(h)
	h = middleware.Recovery(slog.New(slog.NewTextHandler(io.Discard, nil)))(h)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/panic", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"status":500`) {
		t.Errorf("body = %q, want 500 in RFC 7807", rec.Body.String())
	}
}

// --- pure-unit tests for the dashboard helpers -----------------------

// TestAppListItem_WithLatestInstance covers the live-instance badge path:
// the helper should consult the latest map and render the matching
// state badge. Without the map entry the default badge is used.
func TestAppListItem_WithLatestInstance(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	app := state.App{ID: "a1", Slug: "my-api", Status: state.AppActive}
	latest := map[string]state.Instance{
		"a1": {ID: "i1", AppID: "a1", State: string(state.StateRunning)},
	}
	item := srv.appListItem(t.Context(), app, latest, time.Now())
	if item.StateBadgeLabel == "" {
		t.Errorf("StateBadgeLabel empty, want a label for state=running")
	}
	if item.URL != "https://my-api.gregale.dev" {
		t.Errorf("URL = %q, want gregale.dev hostname", item.URL)
	}
}

// TestAppListItem_DefaultBadge: no latest instance → default badge.
func TestAppListItem_DefaultBadge(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{})
	app := state.App{ID: "a1", Slug: "ghost", Status: state.AppActive}
	item := srv.appListItem(t.Context(), app, map[string]state.Instance{}, time.Time{})
	if item.StateBadgeLabel == "" {
		t.Error("default badge label should not be empty")
	}
	if item.LastDeployed != "" {
		t.Errorf("LastDeployed = %q, want empty for zero time", item.LastDeployed)
	}
}

// TestRenderProblem_PureUnit exercises the standalone helper. Already
// covered by the dashboard route tests above, but pinning the wire
// shape here keeps it stable when the route wiring changes.
func TestRenderProblem_PureUnit(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	renderProblem(rec, log, errors.New("boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rec.Code)
	}
}

// TestDashboardManifestView confirms the state→dashboard adapter.
func TestDashboardManifestView(t *testing.T) {
	app := state.App{
		Manifest: state.AppManifest{
			Entrypoint: []string{"node", "server.js"},
			Env:        map[string]string{"FOO": "bar"},
			WorkingDir: "/app",
			Port:       8080,
			Healthz:    "/healthz",
			User:       "node",
		},
	}
	v := dashboardManifestView(app)
	if len(v.Entrypoint) != 2 || v.Entrypoint[0] != "node" || v.Port != 8080 || v.Healthz != "/healthz" {
		t.Errorf("got %+v", v)
	}
	if v.Env["FOO"] != "bar" {
		t.Errorf("env not propagated: %+v", v.Env)
	}
}

// TestHexPrefix covers both branches: short hash returns the zero
// sentinel; long hash renders the first 6 bytes as 12 hex chars.
func TestHexPrefix(t *testing.T) {
	// short hash
	if got := hexPrefix([]byte{1, 2, 3}); got != "000000000000" {
		t.Errorf("short hash = %q, want 000000000000", got)
	}
	// exactly 6 bytes
	got := hexPrefix([]byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45})
	if got != "abcdef012345" {
		t.Errorf("6-byte hash = %q, want abcdef012345", got)
	}
}

// TestDashboardAccountView_NegativeCountClampedToZero: review finding #5
// path — a missing count (caller passes -1) must render as 0, never
// leak the sentinel.
func TestDashboardAccountView_NegativeCountClampedToZero(t *testing.T) {
	v := dashboardAccountView(state.Account{ID: "a1", Email: "x@example.com", Plan: "pro"}, -5)
	if v.AppCount != 0 {
		t.Errorf("AppCount = %d, want 0 (negative clamped)", v.AppCount)
	}
}

// TestDashboardScanPayload_TopNCap pins the AC #3 dashboard cap
// (issue #464 / extension). The full list flows in via the
// wire DTO; the handler-edge cap is dashboardScanTopN = 10.
// Three behaviours are pinned:
//
//  1. Capacity overflow: a scan with 15 findings renders the
//     top 10 sorted CRITICAL → UNKNOWN (stable on ID for ties),
//     and TotalCount == 15.
//
//  2. Capacity unused: a scan with 5 findings renders the full
//     5, and TotalCount == 5 (no truncation copy).
//
//  3. Zero findings: a scan with 0 vulnerabilities renders an
//     empty slice with TotalCount == 0 (the template branch on
//     .Vulnerabilities falsey fires the "No vulnerabilities
//     matched." copy).
//
// Regression catches: an unstable sort that puts HIGH before
// CRITICAL (the stable-sort tie-break), a missing TotalCount that
// would hide the "Showing N of M" template branch, and an
// unconditional cap that drops rows on a scan with < 10 findings.
func TestDashboardScanPayload_TopNCap(t *testing.T) {
	mkRow := func(id, sev string) api.Vulnerability {
		return api.Vulnerability{
			ID:       id,
			Severity: sev,
			Package:  "p",
			Version:  "v",
			FixedIn:  "",
		}
	}

	t.Run("overflow-capped-at-10-sorted-by-severity", func(t *testing.T) {
		// 15 rows: 5×CRITICAL with stable-sort tie IDs (we
		// want the test to be deterministic), 4×HIGH, 3×MEDIUM,
		// 2×LOW, 1×UNKNOWN. The handler must return only 10
		// rows: all 5 CRITICAL + all 4 HIGH + first MEDIUM;
		// TotalCount == 15.
		//
		// Severity strings are pulled from severityOrdinalTable
		// in N-to-0 ordinal order (CRITICAL first, UNKNOWN
		// last) so the test doesn't reintroduce goconst
		// duplicate-string violations.
		counts := []int{5, 4, 3, 2, 1} // CRITICAL..UNKNOWN
		var vs []api.Vulnerability
		idx := 0
		for sev, ord := range severityOrdinalTable {
			c := counts[ord]
			for i := 0; i < c; i++ {
				vs = append(vs, mkRow(fmt.Sprintf("%s-%02d", sev, i), sev))
			}
			idx++
		}

		out := dashboardScanPayload(&api.ScanResult{
			Status:          "complete",
			Vulnerabilities: vs,
		})
		if len(out.Vulnerabilities) != 10 {
			t.Fatalf("len(Vulnerabilities) = %d, want 10 (cap)", len(out.Vulnerabilities))
		}
		if out.TotalCount != 15 {
			t.Errorf("TotalCount = %d, want 15 (pre-truncation)", out.TotalCount)
		}
		// Walk the same order: first N rows = CRITICAL
		// (5), next = HIGH (4), 10th = first MEDIUM.
		want := []struct {
			count int
		}{
			{5}, // 5 CRITICAL
			{4}, // 4 HIGH
			{1}, // 1 MEDIUM (truncated from 3)
		}
		off := 0
		for ord := 0; ord < len(want) && off < len(out.Vulnerabilities); ord++ {
			var sev string
			for s, o := range severityOrdinalTable {
				if o == ord {
					sev = s
					break
				}
			}
			for i := 0; i < want[ord].count && off < len(out.Vulnerabilities); i++ {
				if out.Vulnerabilities[off].Severity != sev {
					t.Errorf("Vulns[%d].Severity = %q, want %s", off, out.Vulnerabilities[off].Severity, sev)
				}
				off++
			}
		}
	})

	t.Run("no-cap-when-under-N", func(t *testing.T) {
		var vs []api.Vulnerability
		for i := 0; i < 5; i++ {
			vs = append(vs, mkRow(fmt.Sprintf("ROW-%02d", i), "HIGH"))
		}
		out := dashboardScanPayload(&api.ScanResult{
			Status:          "complete",
			Vulnerabilities: vs,
		})
		if len(out.Vulnerabilities) != 5 {
			t.Errorf("len(Vulnerabilities) = %d, want 5 (no cap when under N)", len(out.Vulnerabilities))
		}
		if out.TotalCount != 5 {
			t.Errorf("TotalCount = %d, want 5 (==len when under cap)", out.TotalCount)
		}
	})

	t.Run("zero-findings-empty-payload", func(t *testing.T) {
		out := dashboardScanPayload(&api.ScanResult{
			Status:          "complete",
			Vulnerabilities: nil,
		})
		if len(out.Vulnerabilities) != 0 {
			t.Errorf("len(Vulnerabilities) = %d, want 0", len(out.Vulnerabilities))
		}
		if out.TotalCount != 0 {
			t.Errorf("TotalCount = %d, want 0", out.TotalCount)
		}
	})

	t.Run("nil-scan-returns-zero-ScanPayload", func(t *testing.T) {
		out := dashboardScanPayload(nil)
		if out.TotalCount != 0 {
			t.Errorf("TotalCount = %d, want 0 for nil scan", out.TotalCount)
		}
		if len(out.Vulnerabilities) != 0 {
			t.Errorf("len(Vulnerabilities) = %d, want 0 for nil scan", len(out.Vulnerabilities))
		}
	})
}

// TestSeverityOrdinal pins the order the dashboard's stable sort
// uses for the top-N cap. A regression that changes the ordinal
// (e.g. swapping HIGH/MEDIUM) surfaces here as a sort-order test
// failure on TestDashboardScanPayload_TopNCap.
//
// Iterates over the package-level severityOrdinalTable directly
// so the test doesn't reintroduce the goconst trip on
// duplicate literal strings.
func TestSeverityOrdinal(t *testing.T) {
	for sev, want := range severityOrdinalTable {
		if got := severityOrdinal(sev); got != want {
			t.Errorf("severityOrdinal(%q) = %d, want %d", sev, got, want)
		}
	}
	// Unknown / empty values sort one past the last known
	// severity.
	if got := severityOrdinal(""); got != len(severityOrdinalTable) {
		t.Errorf("severityOrdinal(\"\") = %d, want %d (past last known)", got, len(severityOrdinalTable))
	}
	if got := severityOrdinal("bogus"); got != len(severityOrdinalTable) {
		t.Errorf("severityOrdinal(\"bogus\") = %d, want %d (past last known)", got, len(severityOrdinalTable))
	}
}

// TestSessionAuth_StampsStepUpOnRequestContext is the regression
// pin for review finding #3 (PR #653 mega-PR, ADR-077):
// sessionAuth (cmd/apid/handlers_auth.go) MUST stamp env.StepUpAt
// onto r.Context() so the dashboard's requireStepUpHandler(5m)
// gate can read it via StepUpFrom(r). Without this stamp the gate
// sees has=false at pkg/auth/middleware/middleware.go:889 and
// silently bypasses step-up on POST /dashboard/account/{delete,
// set-password} — the same "stolen browser, post-MFA-clear"
// threat change 6 exists to close.
//
// Drives the full auth chain: render /dashboard/account to mint a
// fresh faas_csrf cookie + delete csrf_token (per
// dashboard_delete_test.go's renderDashboardAccount helper), then
// POSTs the delete form. A pre-PR-077 cookie (env.StepUpAt zero,
// which is what newAuthedDashboardServer's mgr.Issue emits) MUST
// trip the gate with reason="missing" → 403 CodeStepUpRequired.
// (PR-8 §1 split the step-up gate's wire code from the
// requireMFA gate; requireMFA still returns CodeMFARequired,
// requireStepUp returns CodeStepUpRequired — same TwoFactorGate
// property but distinguishable in the dashboard's failure
// banner. See docs/adr/077-step-up-mfa.md §"Routes".)
//
// Without the WithStepUp call in sessionAuth the gate's `!has`
// bypass branch would forward the request — a 302 success that
// lets the attacker delete the account.
func TestSessionAuth_StampsStepUpOnRequestContext(t *testing.T) {
	srv, sid := newAuthedDashboardServer(t)
	csrfCookie, deleteToken, _ := renderDashboardAccount(t, srv, sid)
	if deleteToken == "" {
		t.Fatal("rendered account page is missing the delete csrf_token")
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/dashboard/account/delete",
		strings.NewReader("csrf_token="+deleteToken))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(sid)
	r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("pre-PR cookie on /dashboard/account/delete: code = %d, want 403 (step-up gate)\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), api.CodeStepUpRequired) {
		t.Errorf("body missing code %q; got %s", api.CodeStepUpRequired, rec.Body.String())
	}
}

// --- raiseOverageCap dashboard (issue #561) ----------------------------------
//
// POST /dashboard/raise-overage-cap is the dashboard mirror of the
// v1 API endpoint. The CSRF envelope is the same one Issue pattern
// (CookieNameAuthenticated sidecar + csrf_token field, both bound
// to action="raise_overage_cap" by middleware.IssueForAuthenticated).
// The renderBilling helper mints the pair on GET, so the test
// drives a real GET → POST pair to mirror the customer's
// browser-side flow.

// renderDashboardBilling GETs /dashboard/billing with the session
// cookie and returns the rendered form's csrf_token value + the
// matching faas_csrf sidecar cookie. Mirrors renderDashboardAccount
// (dashboard_delete_test.go:57) but reaches the billing route
// because the raise-cap form lives on /dashboard/billing, not
// /dashboard/account.
func renderDashboardBilling(t *testing.T, h http.Handler, sid *http.Cookie) (csrfCookie, raiseToken string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/billing", nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/billing: status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.CookieNameAuthenticated {
			csrfCookie = c.Value
		}
	}
	if csrfCookie == "" {
		t.Fatalf("GET /dashboard/billing: missing %s cookie in Set-Cookie: %v",
			middleware.CookieNameAuthenticated, rec.Header().Get("Set-Cookie"))
	}
	raiseToken = extractInputValue(rec.Body.String(), "csrf_token", "/dashboard/raise-overage-cap")
	if raiseToken == "" {
		t.Fatalf("GET /dashboard/billing: missing raise-cap csrf_token form field\nbody = %s", rec.Body.String())
	}
	return csrfCookie, raiseToken
}

// TestDashboardBilling_RendersOverageCap confirms the billing page
// renders the spend-cap section + the raise-cap form. The test
// seeds a non-zero cap so it can pin the configured-cap row's
// value (the "no cap" branch is covered by the unset fixture in
// TestDashboardRaiseOverageCap_PostsForm). The render must include
// the CSRF token AND the form's POST action so the next request
// lands on the right handler.
func TestDashboardBilling_RendersOverageCap(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "billing-cap@example.com", "pro")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := store.UpdateAccountOverageCapCents(t.Context(), acct.ID, ptrInt64(2500)); err != nil {
		t.Fatalf("seed cap: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	sid, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil,
		15*60_000_000_000, "").handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/billing", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		// Spend-cap section header.
		"Spend cap",
		// Configured-cap row.
		"2500 cents / month",
		// Form's POST action.
		`action="/dashboard/raise-overage-cap"`,
		// CSRF token field.
		`name="csrf_token"`,
		// Number input — the only field besides csrf_token.
		`name="overage_cap_cents"`,
		// Helper text explains the cap=0 path (review finding #4).
		"Set 0 to allow no overage at all",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
	// The `clear` checkbox was removed (review finding #4); an
	// accidental re-introduction would surface here.
	if strings.Contains(body, `name="clear"`) {
		t.Errorf("body still contains `name=\"clear\"` checkbox; review finding #4 wants this removed\nbody:\n%s", body)
	}
}

// TestDashboardRaiseOverageCap_PostsForm drives a real GET → POST
// pair against the dashboard billing page. The renderer mints the
// faas_csrf cookie + raise-cap csrf_token field; the POST verifies
// the envelope and 302s to /dashboard/billing?cap=updated. The
// pivot is the new account's overage_cap_cents column: it must
// reflect the form value (2500) after the redirect.
//
// Step-up gated: requires a fresh StepUpAt stamp on the cookie
// envelope so the requireStepUpHandler(5m) gate passes (review
// finding #10 — the route matches dashboardDelete's threat model).
func TestDashboardRaiseOverageCap_PostsForm(t *testing.T) {
	// newAuthedDashboardServerFull wires the real session.Manager +
	// MemStore AND seeds the alice@example.com account — we reuse
	// that account so the session.Issue path has a valid row.
	srv, _, store, mgr := newAuthedDashboardServerFull(t)
	accts, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	sid := "raised-cap-sid"
	if _, err := store.CreateSession(t.Context(), sid, accts.ID, "192.0.2.10", "raised-cap-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	steppedCookie, err := mgr.IssueWithSessionAndBindingHashAndStepUp(sid, accts.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("issue stepped-up cookie: %v", err)
	}
	sessionCookieVal := &http.Cookie{Name: sessionCookie, Value: steppedCookie}

	csrfCookie, raiseToken := renderDashboardBilling(t, srv, sessionCookieVal)
	if raiseToken == "" {
		t.Fatal("rendered billing page is missing the raise-cap csrf_token")
	}
	rec := dashboardPOST(t, srv, sessionCookieVal,
		"/dashboard/raise-overage-cap",
		map[string]string{
			middleware.FormFieldName: raiseToken,
			"overage_cap_cents":      "2500",
		},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\nbody = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "cap=updated") {
		t.Errorf("Location = %q, want cap=updated", loc)
	}
	// Storage: cap is now 2500.
	cents, found, err := store.GetAccountOverageCapCents(t.Context(), accts.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents: %v", err)
	}
	if !found || cents != 2500 {
		t.Errorf("cents = %d, found = %v, want 2500/true", cents, found)
	}
	// Audit row: overage.cap_changed emitted by the shared svc.
	rows, err := store.ListEvents(t.Context(), accts.ID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var hit *state.Event
	for i := range rows {
		if rows[i].Kind == "overage.cap_changed" {
			hit = &rows[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("overage.cap_changed audit row not found; events: %+v", rows)
	}
}

// TestDashboardRaiseOverageCap_RejectsPreStepUpCookie pins the
// step-up gate (review finding #10). A cookie with env.StepUpAt =
// zero (the pre-PR-#653 envelope, what newAuthedDashboardServer's
// mgr.Issue emits) must 403 at the requireStepUpHandler gate
// before the CSRF check runs. Mirrors TestSessionAuth_StampsStepUpOnRequestContext
// for this route.
//
// We piggyback on newAuthedDashboardServer's alice@example.com
// account (the helper seeds it for us) — the route under test
// requires the same login envelope, and the step-up gate fires
// regardless of which account is involved.
func TestDashboardRaiseOverageCap_RejectsPreStepUpCookie(t *testing.T) {
	srv, sid := newAuthedDashboardServer(t)
	csrfCookie, raiseToken := renderDashboardBilling(t, srv, sid)
	if raiseToken == "" {
		t.Fatal("rendered billing page is missing the raise-cap csrf_token")
	}
	rec := dashboardPOST(t, srv, sid,
		"/dashboard/raise-overage-cap",
		map[string]string{
			middleware.FormFieldName: raiseToken,
			"overage_cap_cents":      "2500",
		},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	if rec.Code != http.StatusForbidden {
		t.Errorf("pre-step-up cookie on /dashboard/raise-overage-cap: code = %d, want 403 (step-up gate)\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestAppsListSLOLoop_PointersAreDistinct pins the per-row
// SLO badge pointer wiring at handlers_dashboard.go
// (attachSLOBadges). The loop takes &badge inside the body
// where `badge` is declared inside the loop body via the
// `if badge, ok := badges[...]; ok` short-form — on Go ≥ 1.22
// that produces a fresh stack slot per iteration (the classic
// for-range loop-variable aliasing gotcha does NOT apply here
// because `badge` is NOT a for-clause binding).
//
// The test asserts the post-conditions the template cares
// about regardless of the in-body/for-clause distinction:
//
//  1. items[i].SLO addresses are pairwise distinct (no
//     aliasing, whether from a future refactor that flips
//     the binding shape into the for-clause form, or any
//     other subtle bug).
//  2. The value at items[i].SLO round-trips the badge keyed
//     on items[i].Slug (not some other row's badge).
//
// The helper is extracted (attachSLOBadges) so this test
// drives the real production code — an inlined copy would
// not bind against a future regression.
func TestAppsListSLOLoop_PointersAreDistinct(t *testing.T) {
	items := []dashboard.AppListItem{
		{Slug: "app-a"},
		{Slug: "app-b"},
		{Slug: "app-c"},
	}
	badges := map[string]views.SLOBadge{
		"app-a": {Label: "p95: 87.3ms", Glyph: "ok"},
		"app-b": {Label: "err: 4.12%", Glyph: "warn"},
		"app-c": {Label: "p95: 312.4ms", Glyph: "warn"},
	}
	attachSLOBadges(items, badges)
	seen := make(map[*views.SLOBadge]int, len(items))
	for i := range items {
		seen[items[i].SLO]++
	}
	if len(seen) != len(items) {
		t.Errorf("SLO pointers alias: got %d distinct addresses for %d rows", len(seen), len(items))
	}
	for _, it := range items {
		if it.SLO == nil {
			t.Errorf("row %q: nil SLO pointer", it.Slug)
			continue
		}
		want := badges[it.Slug].Label
		if it.SLO.Label != want {
			t.Errorf("row %q: SLO.Label = %q, want %q", it.Slug, it.SLO.Label, want)
		}
	}
}

// TestDashboardStagePayload — A2 (ADR-117 v2 follow-on). Pins the
// dashboard handler's projection of state.Deployment → dashboard.StagePayload.
//
// Four branches:
//
//   - empty stage_state (pre-00302 OR in-flight pre-first-frame)
//     returns StagePayload{}, nil — the template omits the section.
//   - non-empty stage_state with all 6 stages completed returns
//     a populated BodyHTML containing the closed-6-stage labels
//   - the "live since <ts>" footer.
//   - non-empty stage_state with a failed row returns the failed
//     row's footer.
//   - non-empty stage_state with status="superseded" anchors the
//     footer on d.CreatedAt (review finding C1; the pre-fix code
//     returned time.Time{} for superseded, silently dropping the
//     footer).
//
// IDOR posture is owned by the renderDeploymentDetail caller
// (AppBySlug + AccountID + DeploymentByID + AppID checks); the
// projection helper only reads the already-authorized row.
func TestDashboardStagePayload(t *testing.T) {
	t.Run("empty-stage-state-returns-zero-value", func(t *testing.T) {
		d := state.Deployment{ID: "d-empty", Status: state.DeployLive}
		got, err := dashboardStagePayload(d)
		if err != nil {
			t.Fatalf("dashboardStagePayload empty: %v", err)
		}
		if got.BodyHTML != "" {
			t.Errorf("empty stage_state: BodyHTML = %q, want empty", got.BodyHTML)
		}
		if got.Status != "" {
			t.Errorf("empty stage_state: Status = %q, want empty", got.Status)
		}
	})

	t.Run("all-completed-returns-rendered-html", func(t *testing.T) {
		now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
		ss := state.StageState{
			History: []state.StageStateItem{
				{Name: state.StageSourceDownload, StartedAt: &now, EndedAt: tPtr(now.Add(1 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageDependencyRestore, StartedAt: &now, EndedAt: tPtr(now.Add(2 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageImageBuild, StartedAt: &now, EndedAt: tPtr(now.Add(3 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageSecurityScan, StartedAt: &now, EndedAt: tPtr(now.Add(4 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageSnapshotPrepare, StartedAt: &now, EndedAt: tPtr(now.Add(5 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageReadiness, StartedAt: &now, EndedAt: tPtr(now.Add(6 * time.Second)), DurationMs: 1000, Status: "completed"},
			},
		}
		raw, _ := json.Marshal(ss)
		d := state.Deployment{ID: "d-1", Status: state.DeployLive, StageState: raw}
		got, err := dashboardStagePayload(d)
		if err != nil {
			t.Fatalf("dashboardStagePayload all-completed: %v", err)
		}
		// The pre-rendered HTML must contain the closed-6-stage
		// labels and the footer.
		for _, want := range []string{
			`<section class="stage-timeline">`,
			"Source downloaded",
			"Dependencies restored",
			"Image built",
			"Security scan",
			"Snapshot prepared",
			"Readiness passed",
			"live since",
		} {
			if !strings.Contains(string(got.BodyHTML), want) {
				t.Errorf("BodyHTML missing %q\nfull: %s", want, got.BodyHTML)
			}
		}
		if got.Status != "live" {
			t.Errorf("Status = %q, want %q", got.Status, "live")
		}
	})

	t.Run("failed-row-renders-failed-footer", func(t *testing.T) {
		now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
		ss := state.StageState{
			History: []state.StageStateItem{
				{Name: state.StageSourceDownload, StartedAt: &now, EndedAt: tPtr(now.Add(1 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageDependencyRestore, StartedAt: &now, EndedAt: tPtr(now.Add(2 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageImageBuild, StartedAt: &now, EndedAt: tPtr(now.Add(3 * time.Second)), DurationMs: 1000, Status: "failed", Reason: "out of memory"},
			},
		}
		raw, _ := json.Marshal(ss)
		d := state.Deployment{ID: "d-2", Status: state.DeployFailed, StageState: raw, ErrorCode: api.CodeStageImageBuildOOM}
		got, err := dashboardStagePayload(d)
		if err != nil {
			t.Fatalf("dashboardStagePayload failed: %v", err)
		}
		if !strings.Contains(string(got.BodyHTML), "failed: out of memory") {
			t.Errorf("expected 'failed: out of memory' in BodyHTML, got %q", got.BodyHTML)
		}
		if !strings.Contains(string(got.BodyHTML), "failed at") {
			t.Errorf("expected 'failed at' footer in BodyHTML, got %q", got.BodyHTML)
		}
		if got.Status != "failed" {
			t.Errorf("Status = %q, want %q", got.Status, "failed")
		}
		// C4 (ADR-117 §Production-ready follow-on): the
		// FailureExplanation block must populate from
		// pkg/whycopy.Decorate against the typed
		// CodeStage* set. Pins the wiring that connects the
		// cluster-A hint/why/fix prose to the dashboard
		// widget for failed rows.
		if got.FailureExplanation == nil {
			t.Fatalf("FailureExplanation is nil; expected a populated block for CodeStageImageBuildOOM")
		}
		if !strings.Contains(got.FailureExplanation.Title, "Image build") {
			t.Errorf("FailureExplanation.Title = %q, want it to mention 'Image build'", got.FailureExplanation.Title)
		}
		if got.FailureExplanation.Hint == "" || got.FailureExplanation.Fix == "" || got.FailureExplanation.Why == "" {
			t.Errorf("FailureExplanation fields not all populated: %+v", got.FailureExplanation)
		}
	})

	// C4 — failed-row payload MUST NOT populate the
	// explanation when ErrorCode is outside the CodeStage*
	// set (the catalog returns "no row" → no decoration).
	// Mirrors the cluster-A pattern: every code has a whycopy
	// row OR the dashboard renders a bare "Failed" pill.
	t.Run("failed-no-code-no-explanation", func(t *testing.T) {
		now := time.Now()
		ss := state.StageState{
			History: []state.StageStateItem{
				{Name: state.StageImageBuild, StartedAt: &now, EndedAt: tPtr(now.Add(3 * time.Second)), DurationMs: 1000, Status: "failed", Reason: "out of memory"},
			},
		}
		raw, _ := json.Marshal(ss)
		d := state.Deployment{ID: "d-2b", Status: state.DeployFailed, StageState: raw}
		got, err := dashboardStagePayload(d)
		if err != nil {
			t.Fatalf("dashboardStagePayload failed: %v", err)
		}
		if got.FailureExplanation != nil {
			t.Errorf("FailureExplanation must be nil when ErrorCode empty; got %+v", got.FailureExplanation)
		}
	})

	// Review finding C1 (mirrors cmd/gregale/deploys_show_test.go):
	// dashboardStageTerminalAt handles the "superseded" branch by
	// anchoring on d.CreatedAt. Pre-fix code returned time.Time{}
	// for any status other than "live"/"failed", leaving the
	// operator looking at a stage table with no terminal anchor
	// even though deployments.status said "superseded".
	t.Run("superseded-anchors-on-created-at", func(t *testing.T) {
		now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
		ss := state.StageState{
			History: []state.StageStateItem{
				{Name: state.StageSourceDownload, StartedAt: &now, EndedAt: tPtr(now.Add(1 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageDependencyRestore, StartedAt: &now, EndedAt: tPtr(now.Add(2 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageImageBuild, StartedAt: &now, EndedAt: tPtr(now.Add(3 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageSecurityScan, StartedAt: &now, EndedAt: tPtr(now.Add(4 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageSnapshotPrepare, StartedAt: &now, EndedAt: tPtr(now.Add(5 * time.Second)), DurationMs: 1000, Status: "completed"},
				{Name: state.StageReadiness, StartedAt: &now, EndedAt: tPtr(now.Add(6 * time.Second)), DurationMs: 1000, Status: "completed"},
			},
		}
		raw, _ := json.Marshal(ss)
		d := state.Deployment{
			ID: "d-3", Status: state.DeploySuperseded,
			StageState: raw, CreatedAt: now,
		}
		got, err := dashboardStagePayload(d)
		if err != nil {
			t.Fatalf("dashboardStagePayload superseded: %v", err)
		}
		wantTs := now.UTC().Format(time.RFC3339)
		if !strings.Contains(string(got.BodyHTML), "superseded at "+wantTs) {
			t.Errorf("expected 'superseded at %s' footer\nfull: %s", wantTs, got.BodyHTML)
		}
		if strings.Contains(string(got.BodyHTML), "live since") {
			t.Errorf("superseded render must NOT contain 'live since'\nfull: %s", got.BodyHTML)
		}
		if strings.Contains(string(got.BodyHTML), "failed at") {
			t.Errorf("superseded render must NOT contain 'failed at'\nfull: %s", got.BodyHTML)
		}
		if got.Status != "superseded" {
			t.Errorf("Status = %q, want %q", got.Status, "superseded")
		}
	})
}

// tPtr is a small helper to make the fixture rows less noisy.
// (Named to avoid colliding with the existing helpers.go::ptrTime
// which has the inverse signature.)
func tPtr(t time.Time) *time.Time { return &t }

// TestRepoFullNameFromSourceURL (issue #977 / ADR-116 review fix
// CRIT-1) pins the SourceURL → RepoFullName parsing. The list-view
// template uses RepoFullName to build the PR link target so a
// clickable #N chip lands on GitHub. A regression that drops the
// owner/name extraction (or accepts a malformed URL) would render
// 404 links on every PR-annotated deployment row.
func TestRepoFullNameFromSourceURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"typical githubd embed", "github://onebox-faas/hello@0123456789abcdef0123456789abcdef01234567", "onebox-faas/hello"},
		{"single-char owner", "github://a/b@deadbeef", "a/b"},
		{"image-deploy (oci://)", "oci://registry.example.com/foo@sha256:abc", ""},
		{"local tarball (empty)", "", ""},
		{"local tarball (tarball://)", "tarball://local", ""},
		{"truncated github:// no @", "github://onebox-faas/hello", ""},
		{"github:// with empty owner", "github:///hello@abc", ""},
		{"github:// with empty repo", "github://hello/@abc", ""},
		{"unknown scheme", "gitlab://owner/repo@abc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := repoFullNameFromSourceURL(tc.in)
			if got != tc.want {
				t.Errorf("repoFullNameFromSourceURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDashboardDeploymentItem_PopulatesRepoFullName verifies the
// projection thread through dashboardDeploymentItem (issue #977 /
// ADR-116 review fix CRIT-1). The handler edge is the single seam
// — every DeploymentItem flows through it. A regression that
// forgot to populate RepoFullName would surface here as a missing
// PR link on the dashboard list view.
func TestDashboardDeploymentItem_PopulatesRepoFullName(t *testing.T) {
	dep := state.Deployment{
		ID:        "d-1",
		Status:    state.DeployLive,
		Kind:      state.DeploymentKindGitHub,
		SourceURL: "github://acme-co/payments@0123456789abcdef0123456789abcdef01234567",
		PRNumber:  4242,
	}
	item := dashboardDeploymentItem(dep)
	if item.RepoFullName != "acme-co/payments" {
		t.Errorf("RepoFullName = %q, want %q", item.RepoFullName, "acme-co/payments")
	}
	if item.PRNumber != 4242 {
		t.Errorf("PRNumber = %d, want 4242", item.PRNumber)
	}

	// Image-deploy: empty SourceURL → empty RepoFullName (template
	// drops the PR link rather than rendering a 404).
	imgDep := state.Deployment{
		ID:        "d-2",
		Status:    state.DeployLive,
		Kind:      state.DeploymentKindImage,
		SourceURL: "oci://registry.example.com/foo@sha256:abc",
		PRNumber:  0,
	}
	imgItem := dashboardDeploymentItem(imgDep)
	if imgItem.RepoFullName != "" {
		t.Errorf("image-deploy RepoFullName = %q, want empty", imgItem.RepoFullName)
	}
}

func TestDashboardHostingReceipt_ProjectsReadinessEvidence(t *testing.T) {
	want := apihostingreceipt.Receipt{
		SchemaVersion: apihostingreceipt.SchemaVersion,
		DeploymentID:  "d-receipt",
		AppID:         "app-receipt",
		AppURL:        "https://receipt-app.apps.gregale.dev",
		Source: apihostingreceipt.Source{
			Kind:      "github",
			URL:       "github://acme/receipt-app@deadbeef",
			CommitSHA: "deadbeef",
		},
		Profile: frameworkprofile.Profile{
			Version:      frameworkprofile.Version,
			Framework:    "fastapi",
			FrameworkVer: "0.115",
			Port:         8080,
			HealthPath:   "/healthz",
		},
		Smoke: apihostingreceipt.SmokeResult{
			Status:     apihostingreceipt.SmokeVerified,
			Path:       "/healthz",
			StatusCode: 200,
			LatencyMS:  42,
		},
	}
	raw, err := apihostingreceipt.Encode(want)
	if err != nil {
		t.Fatalf("encode receipt: %v", err)
	}
	got, err := dashboardHostingReceipt(raw)
	if err != nil {
		t.Fatalf("dashboardHostingReceipt: %v", err)
	}
	if got == nil {
		t.Fatal("dashboardHostingReceipt returned nil view")
	}
	if got.AppURL != want.AppURL || got.Framework != want.Profile.Framework || got.HealthPath != want.Profile.HealthPath || got.SmokeStatus != want.Smoke.Status {
		t.Fatalf("projection = %+v, want app=%q framework=%q path=%q status=%q", got, want.AppURL, want.Profile.Framework, want.Profile.HealthPath, want.Smoke.Status)
	}
	if empty, err := dashboardHostingReceipt(json.RawMessage(`{}`)); err != nil || empty != nil {
		t.Fatalf("empty receipt = (%v, %v), want (nil, nil)", empty, err)
	}
}
