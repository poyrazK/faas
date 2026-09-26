// Tests for the dashboard cron runs panel + fire-now POST
// (issue #791 PR-E / ADR-090 §"Sub-decision 7"). Reuses
// handlers_dashboard_test.go::newAuthedDashboardServerFull —
// the harness already exposes (handler, cookie, store, *session.Manager)
// so CSRF envelope minting via middleware.IssueForAuthenticated
// works without bespoke helpers. Seeds an app + cron +
// invocations directly through the MemStore, then table-drives:
//
//  1. App-detail renders the new per-cron section (cron-section
//     class, "Last N runs" details, glyph colour classes).
//  2. Empty-state: a brand-new cron with no invocations renders
//     "No runs yet." rather than a blank box.
//  3. Fire-now POST without CSRF → 400 problem (VerifyAuthenticated
//     fails before any ownership probe runs).
//  4. Fire-now POST cross-account → 302 with ?fired=error (the IDOR
//     safe two-step short-circuits to redirect-with-flash).
//  5. ?fired=1 renders the success banner.
//  6. ?fired=error renders the error banner.
//
// Happy-path 302 + plan-tier gates are covered by the existing
// handlers_cron_run_test.go (v1 surface); the dashboard path's
// own CSRF envelope binding is the new surface that needs its
// own regression.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedCronFixture wires the dashboard fixture for the panel +
// flash + cross-account tests. Returns two harness halves — A
// owns the app + cron, B never sees the cron.
//
// Layout:
//
//	account A (free, alice@example.com)
//	└── app "cronapp"
//	    └── cron enabled "*/5 * * * *" /cleanup
//	         ├── success 1200ms
//	         ├── timeout 60s
//	         └── success 980ms
//
//	account B (free, alice@example.com) — fresh MemStore, no apps
//
// The "free" plan is intentional: the dashboard's POST surface
// short-circuits on the plan gate, so the cross-account test
// asserts that the redirect-with-flash wins over a 402 (the
// IDOR-safe short-circuit). v1's plan-tier gate test in
// handlers_cron_run_test.go covers the success branch.
func seedCronFixture(t *testing.T) (
	hA http.Handler, cookieA *http.Cookie, storeA *state.MemStore, mgrA *session.Manager,
	slug, cronID string,
	hB http.Handler, cookieB *http.Cookie, mgrB *session.Manager,
) {
	t.Helper()
	hA, cookieA, storeA, mgrA = newAuthedDashboardServerFull(t)
	acctA, err := storeA.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("seed account A lookup: %v", err)
	}
	app, err := storeA.CreateApp(t.Context(), state.App{Slug: "cronapp", AccountID: acctA.ID})
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	ce, err := storeA.CreateCron(t.Context(), app.ID, "*/5 * * * *", "/cleanup", true)
	if err != nil {
		t.Fatalf("seed enabled cron: %v", err)
	}
	now := time.Now().UTC()
	for i, spec := range []struct {
		outcome   state.InvocationOutcome
		duration  time.Duration
		isFailure bool
	}{
		{state.OutcomeSuccess, 1200 * time.Millisecond, false},
		{state.OutcomeTimeout, 60 * time.Second, true},
		{state.OutcomeSuccess, 980 * time.Millisecond, false},
	} {
		cid := ce.ID
		inv := state.Invocation{
			AppID:     app.ID,
			AccountID: acctA.ID,
			Source:    state.InvocationCron,
			State:     state.InvocationPending,
			Method:    "POST",
			Path:      "/cleanup",
			CreatedAt: now.Add(time.Duration(-i) * time.Hour),
			DueAt:     now.Add(time.Duration(-i) * time.Hour),
			Attempts:  1,
			CronID:    &cid,
		}
		created, err := storeA.EnqueueInvocation(t.Context(), inv)
		if err != nil {
			t.Fatalf("seed invocation %d: %v", i, err)
		}
		// Move the row from pending → dispatching via ClaimInvocation,
		// then complete or fail it so the dashboard's ListCronRunsForCron
		// reads a terminal-state row with a real CompletedAt + Outcome.
		if _, err := storeA.ClaimInvocation(t.Context(), created.ID, "i_"+created.ID, 30); err != nil {
			t.Fatalf("claim invocation %d: %v", i, err)
		}
		completedAt := created.CreatedAt.Add(spec.duration)
		created.CompletedAt = &completedAt
		if spec.isFailure {
			if err := storeA.FailInvocation(t.Context(), created.ID, "deadline exceeded", 0, 0, state.WithOutcome(spec.outcome)); err != nil {
				t.Fatalf("fail invocation %d: %v", i, err)
			}
		} else {
			if err := storeA.CompleteInvocation(t.Context(), created.ID, json.RawMessage(`{"ok":true}`)); err != nil {
				t.Fatalf("complete invocation %d: %v", i, err)
			}
		}
	}

	hB, cookieB, _, mgrB = newAuthedDashboardServerFull(t)
	return hA, cookieA, storeA, mgrA, app.Slug, ce.ID, hB, cookieB, mgrB
}

func TestDashboard_AppDetail_CronSection_RendersRuns(t *testing.T) {
	h, cookie, _, _, slug, _, _, _, _ := seedCronFixture(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/"+slug, nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET app detail = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<code>*/5 * * * *</code>`,
		`<code>/cleanup</code>`,
		`cron-section`,
		`id="cron-`,
		`Fire now`,
		`Last 3 runs`,
		`glyph-ok`,
		`glyph-fail`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; got:\n%s", want, body)
		}
	}
}

// TestDashboard_AppDetail_CronSection_EmptyRuns pins the
// empty-state: a brand-new cron with no invocations yet renders
// "No runs yet." inside the <details>. A blank box would be
// worse than useless — it would make a freshly-deployed cron
// indistinguishable from a broken one.
func TestDashboard_AppDetail_CronSection_EmptyRuns(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFullFull(t, "free", "alice@example.com")
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{Slug: "emptycronapp", AccountID: acct.ID}); err != nil {
		t.Fatalf("app: %v", err)
	}
	// Note: no cron invocations seeded — the whole point.
	if _, err := store.CreateCron(t.Context(), mustAppIDFromSlug(t, store, "emptycronapp"), "0 * * * *", "/nudge", true); err != nil {
		t.Fatalf("cron: %v", err)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/emptycronapp", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET empty-cron app = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "No runs yet.") {
		t.Errorf("empty runs hint missing; got:\n%s", rec.Body.String())
	}
}

// TestDashboard_FireCronNow_MissingCSRF pins the CSRF posture:
// a POST without the form token AND without the matching
// faas_csrf cookie must 400 with "Invalid CSRF token", NOT
// redirect to ?fired=1. The CSRF gate is the line of defence
// against a cross-site form submission; a successful fire-now
// without the token must not be reachable.
func TestDashboard_FireCronNow_MissingCSRF(t *testing.T) {
	h, cookie, _, _, slug, cronID, _, _, _ := seedCronFixture(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/dashboard/apps/"+slug+"/crons/"+cronID+"/fire-now",
		strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST fire-now no-csrf = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Invalid CSRF token") {
		t.Errorf("CSRF message missing; got: %s", rec.Body.String())
	}
}

// TestDashboard_FireCronNow_CrossAccount pins the IDOR contract:
// even with a valid CSRF envelope minted by account B for B's
// own account, the POST against A's slug + cron id must
// redirect with ?fired=error — never 302 happy, never 404
// oracle. The redirect-with-flash hides the existence oracle
// behind a single inert URL parameter.
func TestDashboard_FireCronNow_CrossAccount(t *testing.T) {
	_, _, _, _, slug, cronID, hB, cookieB, mgrB := seedCronFixture(t)
	env, err := mgrB.Verify(cookieB.Value)
	if err != nil {
		t.Fatalf("verify B's session cookie: %v", err)
	}
	tok, err := middleware.IssueForAuthenticated(mgrB, "fire_cron", env.AccountID)
	if err != nil {
		t.Fatalf("issue csrf B: %v", err)
	}
	form := url.Values{}
	form.Set("csrf_token", tok)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/dashboard/apps/"+slug+"/crons/"+cronID+"/fire-now",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookieB)
	r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok})
	hB.ServeHTTP(rec, r)
	if rec.Code != http.StatusFound {
		t.Fatalf("POST fire-now cross-account = %d, want 302; body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "?fired=error") {
		t.Errorf("Location = %q, want ?fired=error (IDOR must not leak 200 vs 404)", loc)
	}
}

// TestDashboard_AppDetail_FlashBanner_OK checks the
// post-redirect banner is rendered on ?fired=1.
func TestDashboard_AppDetail_FlashBanner_OK(t *testing.T) {
	h, cookie, _, _, slug, _, _, _, _ := seedCronFixture(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/"+slug+"?fired=1", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET app detail ?fired=1 = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Fire-now enqueued.") {
		t.Errorf("success banner missing on ?fired=1; got:\n%s", rec.Body.String())
	}
}

// TestDashboard_AppDetail_FlashBanner_Error checks the
// post-redirect error banner on ?fired=error.
func TestDashboard_AppDetail_FlashBanner_Error(t *testing.T) {
	h, cookie, _, _, slug, _, _, _, _ := seedCronFixture(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps/"+slug+"?fired=error", nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET app detail ?fired=error = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Fire-now failed.") {
		t.Errorf("error banner missing on ?fired=error; got:\n%s", rec.Body.String())
	}
}

// --- helpers ----------------------------------------------------------

// newAuthedDashboardServerFullFull is a parameterised variant of
// newAuthedDashboardServerFull — needed by the empty-state test
// which spans a different app. Mirrors the harness signature.
func newAuthedDashboardServerFullFull(t *testing.T, plan, email string) (http.Handler, *http.Cookie, *state.MemStore, *session.Manager) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), email, api.Plan(plan))
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
	log := discardLogger()
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*60_000_000_000, "")
	return srv.handler(), &http.Cookie{Name: sessionCookie, Value: cookie}, store, mgr
}

// mustAppIDFromSlug resolves an app's id from its slug, failing
// the test if the lookup fails. Mirrors the helpers in other
// apid test files (see handlers_apps_test.go).
func mustAppIDFromSlug(t *testing.T, store *state.MemStore, slug string) string {
	t.Helper()
	app, err := store.AppBySlug(t.Context(), slug)
	if err != nil {
		t.Fatalf("AppBySlug(%q): %v", slug, err)
	}
	return app.ID
}
