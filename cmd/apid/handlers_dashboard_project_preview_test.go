// Tests for ADR-124 dashboard affected-workloads preview.
//
// Three behaviours pinned:
//
//  1. GET /dashboard/projects/{slug}/preview renders the empty
//     form with the CSRF token + faas_csrf cookie envelope. The
//     form's enctype is multipart/form-data so the POST round-trip
//     has the right Content-Type.
//
//  2. previewSlugOK (path-validator) accepts well-formed slugs
//     and rejects uppercase, slashes, double-dashes, empty strings,
//     and over-length values. Mirrors the G710 gosec gate so a
//     future open-redirect regression trips here, not at
//     /dashboard/account/delete.
//
//  3. toProjectPreviewAffected + actionAffordance map the wire
//     action vocabulary to the dashboard's per-action glyph +
//     label. Pure helper, no HTTP round-trip.
//
// The multipart POST → populated preview path is covered
// indirectly by the v1 surface's scan_service_test.go
// (--exclude + plan-token exclusion tests). Driving a real
// tarball through the multipart POST would require the spool
// + extract machinery scanService expects; that path is
// integration territory (Make test-metal / make leakcheck)
// rather than a unit test, so we leave it there.
package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// TestRenderProjectPreview_EmptyForm confirms GET
// /dashboard/projects/{slug}/preview renders the empty multipart
// form with the CSRF envelope. The slug is the URL path tail so
// any of "demo", "payments-api", or "my-org-2026" can drive the
// test.
func TestRenderProjectPreview_EmptyForm(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/projects/demo/preview", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	// The csrf cookie must be minted alongside the page so the
	// multipart POST round-trips successfully.
	var csrfCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.CookieNameAuthenticated {
			csrfCookie = c.Value
		}
	}
	if csrfCookie == "" {
		t.Fatalf("missing %s cookie in Set-Cookie", middleware.CookieNameAuthenticated)
	}
	body := rec.Body.String()
	for _, want := range []string{
		// Form action posts back to the preview URL with the
		// project slug embedded.
		`action="/dashboard/projects/demo/preview"`,
		`enctype="multipart/form-data"`,
		// CSRF envelope field.
		`name="csrf_token"`,
		// Tarball + exclude inputs.
		`name="tarball"`,
		`name="exclude"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestRenderProjectPreview_UnknownSlugReturnsEmptyForm confirms a
// slug that does not exist on the account still renders the form
// with an inline "Project not found" hint. We do NOT 404 — the
// preview form doubles as the "first deploy" entry point so a
// fresh operator can drop a tarball here to create the project.
func TestRenderProjectPreview_UnknownSlugReturnsEmptyForm(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/projects/ghost/preview", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Project not found") {
		t.Errorf("body missing 'Project not found' hint\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "ghost") {
		t.Errorf("body missing the slug echo\n--- body ---\n%s", body)
	}
}

// TestPreviewSlugOK_PinsIDORSafety pins the slug regex that's the
// gate against open-redirect / IDOR-via-path-param attacks (G710
// gosec rule). Anything outside the [a-z0-9-] alphabet, with
// consecutive dashes, or over 64 chars is rejected.
func TestPreviewSlugOK_PinsIDORSafety(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Accept
		{"demo", true},
		{"my-app", true},
		{"a", true},
		{"a1b2c3", true},
		{strings.Repeat("a", 64), true},
		// Reject
		{"", false},
		{strings.Repeat("a", 65), false},
		{"Demo", false},      // uppercase
		{"my_app", false},    // underscore
		{"my-app-2", true},   // digit is fine
		{"my--app", false},   // consecutive dashes (subdomain confusing)
		{"a/b", false},       // slash → open-redirect path component
		{"a..b", false},      // dot
		{"a b", false},       // space
		{"a:b", false},       // scheme separator
		{"%2e%2e", false},    // url-encoded dot-dot
		{"日本", false},        // non-ASCII
		{"app\tname", false}, // tab
		{"app\nname", false}, // newline
	}
	for _, tc := range cases {
		got := previewSlugOK(tc.in)
		if got != tc.want {
			t.Errorf("previewSlugOK(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestToProjectPreviewAffected_MapsActionVocabulary pins the
// view-shape adapter: every wire action ("create" / "update" /
// "remove" / "noop") maps to a non-empty glyph + label, and the
// Excluded flag flips the noop label from "unchanged" to
// "excluded" so the template can render the line-through style
// without a second switch.
func TestToProjectPreviewAffected_MapsActionVocabulary(t *testing.T) {
	t.Run("create-affordance", func(t *testing.T) {
		out := toProjectPreviewAffected([]api.PlanAffectedApp{{Slug: "x", Action: "create"}}, false)
		if len(out) != 1 {
			t.Fatalf("len(out) = %d, want 1", len(out))
		}
		if out[0].ActionGlyph == "" || out[0].ActionLabel == "" {
			t.Errorf("create: empty glyph/label: %+v", out[0])
		}
		if out[0].Excluded {
			t.Errorf("create: Excluded = true, want false")
		}
	})
	t.Run("update-affordance", func(t *testing.T) {
		out := toProjectPreviewAffected([]api.PlanAffectedApp{{Slug: "x", Action: "update", ID: "abc1234567"}}, false)
		if len(out) != 1 {
			t.Fatalf("len(out) = %d, want 1", len(out))
		}
		if out[0].ID != "abc1234567" {
			t.Errorf("ID = %q, want abc1234567", out[0].ID)
		}
	})
	t.Run("noop-unaffected-vs-skipped", func(t *testing.T) {
		unaff := toProjectPreviewAffected([]api.PlanAffectedApp{{Slug: "x", Action: "noop", ID: "u1"}}, false)
		if unaff[0].Excluded {
			t.Errorf("unaffected row must NOT have Excluded=true")
		}
		if unaff[0].ActionLabel == "excluded" {
			t.Errorf("unaffected label = %q, want non-excluded vocabulary", unaff[0].ActionLabel)
		}
		skip := toProjectPreviewAffected([]api.PlanAffectedApp{{Slug: "x", Action: "noop"}}, true)
		if !skip[0].Excluded {
			t.Errorf("skipped row must have Excluded=true")
		}
		if skip[0].ActionLabel != "excluded" {
			t.Errorf("skipped label = %q, want 'excluded'", skip[0].ActionLabel)
		}
	})
	t.Run("remove-affordance", func(t *testing.T) {
		out := toProjectPreviewAffected([]api.PlanAffectedApp{{Slug: "x", Action: "remove"}}, false)
		if out[0].ActionGlyph == "" || out[0].ActionLabel == "" {
			t.Errorf("remove: empty glyph/label: %+v", out[0])
		}
	})
	t.Run("empty-input-returns-nil", func(t *testing.T) {
		out := toProjectPreviewAffected(nil, false)
		if out != nil {
			t.Errorf("nil in: %+v, want nil out", out)
		}
	})
	t.Run("root-dir-drift-surfaces-existing-root", func(t *testing.T) {
		out := toProjectPreviewAffected([]api.PlanAffectedApp{{
			Slug:            "x",
			Action:          "update",
			ID:              "i1",
			ExistingRootDir: "old/root",
		}}, false)
		if out[0].ExistingRoot != "old/root" {
			t.Errorf("ExistingRoot = %q, want old/root", out[0].ExistingRoot)
		}
	})
}

// TestPreviewDispatch_BadSlugRejected confirms a malformed slug
// at the POST handler entrypoint returns 400 rather than
// dispatching into a (potentially redirect-target-injecting)
// handler body. Mirrors dashboard_cron_fire.go's
// dashboardFireCronIDRe regex on the {id} segment of the cron
// fire-now route.
func TestPreviewDispatch_BadSlugRejected(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	form := url.Values{}
	form.Set(middleware.FormFieldName, "anything")
	r := httptest.NewRequest(http.MethodPost, "/dashboard/projects/INVALID/preview",
		strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("uppercase slug on POST /preview: code = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}
