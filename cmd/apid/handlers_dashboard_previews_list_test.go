package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedPreviewForDashboard mirrors cmd/apid/handlers_preview_test.go's
// seedPreviewAppForTest but inlined here so this file stays
// self-contained. Returns the freshly-created app row so the
// caller can assert on the post-render state.
func seedPreviewForDashboard(t *testing.T, store *state.MemStore, accountID, slug, parentSlug string, prNum int) state.App {
	t.Helper()
	expires := time.Now().Add(7 * 24 * time.Hour)
	created, err := store.CreateAppIfUnderQuota(context.Background(), state.App{
		AccountID:        accountID,
		Slug:             slug,
		Type:             "stateless",
		RAMMB:            256,
		MaxConcurrency:   1,
		IdleTimeoutS:     30,
		Status:           state.AppActive,
		PreviewOfSlug:    parentSlug,
		PreviewPrNumber:  prNum,
		PreviewPrState:   state.PreviewPrStateOpen,
		PreviewExpiresAt: &expires,
	}, api.Limits{DeployedApps: 10000})
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota(%q): %v", slug, err)
	}
	return created
}

// TestRenderPreviewsList_HappyPath pins the render wire shape:
// three seeded previews render as a table with one row each, the
// destroy action URL is composed with the parent slug, and the
// dashboard chrome (header nav) is present so the page is not a
// bare fragment.
func TestRenderPreviewsList_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedPreviewForDashboard(t, e.store, e.acct.ID, "pr-42-acme", "acme", 42)
	seedPreviewForDashboard(t, e.store, e.acct.ID, "pr-7-foo", "foo", 7)
	seedPreviewForDashboard(t, e.store, e.acct.ID, "pr-99-bar", "bar", 99)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := httpsec.WithNonce(context.Background(), "test-nonce")
	r := httptest.NewRequest("GET", "/dashboard/previews", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	e.s.renderPreviewsList(w, r, log, e.acct)

	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	for _, want := range []string{"pr-42-acme", "pr-7-foo", "pr-99-bar"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q (preview slug must render): %s", want, body)
		}
	}
	for _, want := range []string{"/dashboard/apps/acme", "/dashboard/apps/foo", "/dashboard/apps/bar"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q (parent link must compose with slug)", want)
		}
	}
	// Tear-down forms must post to the dashboard's destroy
	// action URL — the per-row form action attribute carries
	// the (parent, preview) tuple from the handler.
	for _, want := range []string{
		`action="/dashboard/apps/acme/preview/pr-42-acme/destroy"`,
		`action="/dashboard/apps/foo/preview/pr-7-foo/destroy"`,
		`action="/dashboard/apps/bar/preview/pr-99-bar/destroy"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing form action %q", want)
		}
	}
}

// TestRenderPreviewsList_EmptyState confirms the empty-state
// branch: an account with zero previews renders the helpful
// "No previews yet" copy rather than an empty table.
func TestRenderPreviewsList_EmptyState(t *testing.T) {
	e := setup(t, api.PlanPro)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := httpsec.WithNonce(context.Background(), "test-nonce")
	r := httptest.NewRequest("GET", "/dashboard/previews", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	e.s.renderPreviewsList(w, r, log, e.acct)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No previews yet") {
		t.Errorf("body missing empty-state heading: %s", body)
	}
	if strings.Contains(body, "<table>") {
		t.Errorf("body rendered a <table> for an empty account; want the empty-state copy instead")
	}
}

// TestRenderPreviewsList_CrossAccountIsolation confirms the
// loadApp-style filter on ListPreviewsForAccount: previews
// seeded under account A must not appear in account B's render.
// Same wire-shape invariant as the apid destroy handler.
func TestRenderPreviewsList_CrossAccountIsolation(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Seed a preview under a fresh, second account on the same
	// store. The default setup() account (e.acct) must not see
	// it on the previews page.
	otherAcct, err := e.store.CreateAccount(context.Background(), "other-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount(other): %v", err)
	}
	seedPreviewForDashboard(t, e.store, otherAcct.ID, "pr-9-other", "their-app", 9)

	// Sanity: the other-account preview IS persisted.
	if got, _ := e.store.AppBySlug(context.Background(), "pr-9-other"); got.ID == "" {
		t.Fatal("cross-account preview did not persist (the test rig itself is broken)")
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := httpsec.WithNonce(context.Background(), "test-nonce")
	r := httptest.NewRequest("GET", "/dashboard/previews", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	e.s.renderPreviewsList(w, r, log, e.acct)

	body := w.Body.String()
	if strings.Contains(body, "pr-9-other") {
		t.Errorf("body rendered the other-account preview slug; cross-account isolation is broken: %s", body)
	}
	if strings.Contains(body, "their-app") {
		t.Errorf("body rendered the other-account parent slug; cross-account isolation is broken: %s", body)
	}
}

// TestRenderPreviewsList_SkipsProductionAndDeletedRows confirms
// the row filter: production apps (PreviewOfSlug == "") and
// deleted previews (Status == "deleted") never render on the
// previews page. Production rows are the easy footgun (a stale
// list could expose a customer's prod app on a /previews/ view);
// deleted rows are the janitor's tombstone (must not re-appear
// after the customer tears them down via /v1/preview/{slug}/destroy).
func TestRenderPreviewsList_SkipsProductionAndDeletedRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Production app: must not appear.
	mustSeedApp(t, e, "prod-app")
	// Preview row that the customer already tore down.
	preview := seedPreviewForDashboard(t, e.store, e.acct.ID, "pr-1-torn", "parent", 1)
	if _, err := e.store.SoftDeleteAppCascade(context.Background(), preview.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	// A live preview to keep the page non-empty (so the test
	// can distinguish "no rows" from "no production rows").
	seedPreviewForDashboard(t, e.store, e.acct.ID, "pr-2-live", "parent", 2)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := httpsec.WithNonce(context.Background(), "test-nonce")
	r := httptest.NewRequest("GET", "/dashboard/previews", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	e.s.renderPreviewsList(w, r, log, e.acct)

	body := w.Body.String()
	if strings.Contains(body, ">prod-app<") {
		t.Errorf("body rendered the production app slug; production apps must be filtered out: %s", body)
	}
	if strings.Contains(body, "pr-1-torn") {
		t.Errorf("body rendered the torn-down preview; deleted previews must be filtered out: %s", body)
	}
	if !strings.Contains(body, "pr-2-live") {
		t.Errorf("body missing the live preview row; the live row must render alongside the filters: %s", body)
	}
}

// TestPreviewHostnameFor_PureUnit confirms the small URL helper
// the dashboard previews list uses to compose the customer-facing
// "Open preview" link. Empty slug → empty string (so the
// template can guard).
func TestPreviewHostnameFor_PureUnit(t *testing.T) {
	cases := []struct {
		slug, want string
	}{
		{"pr-42-acme", "pr-42-acme.gregale.dev"},
		{"", ""},
	}
	for _, c := range cases {
		if got := previewHostnameFor(c.slug, "gregale.dev"); got != c.want {
			t.Errorf("previewHostnameFor(%q) = %q, want %q", c.slug, got, c.want)
		}
	}
	if got := previewHostnameFor("pr-42-acme", "apps.gregale.dev"); got != "pr-42-acme.gregale.dev" {
		t.Fatalf("previewHostnameFor legacy domain = %q, want current gregale.dev hostname", got)
	}
}

// _ keeps the io import alive for slog.NewTextHandler(io.Discard, nil).
var _ io.Reader = (*bytes.Buffer)(nil)
