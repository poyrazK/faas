// preview_panel_test.go — ADR-095 PR-C (issue #272) dashboard
// preview-environments panel tests.
//
// Pins the structural contract:
//
//  1. The panel renders the parent slug, PR number, state chip,
//     preview host label, and a Copy URL button per row.
//  2. The panel is suppressed when the page is itself a preview
//     row (apps_list uses the IsPreview bit to indent + chip the
//     row, but the app_detail page never recursively surfaces
//     a preview-of-preview pane).
//  3. The empty-state line is rendered when the production app
//     has no previews yet.
//  4. The Copy URL inline script carries the page CSP nonce
//     (test stamps a synthetic nonce; the body MUST contain it).
//  5. Each row's preview host label follows the canonical
//     `pr-{N}.{parent}` shape regardless of preview row slug.
package dashboard_test

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dashboard"
)

// TestRender_AppDetail_PreviewPanel_Shape pins the rendered
// HTML's panel structure. Mirrors the structural pins
// TestRender_AppDetail_Manifest uses (manifest table) so a future
// template refactor that drops a column fails loudly.
func TestRender_AppDetail_PreviewPanel_Shape(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const nonce = "preview-nonce-22chars-12"

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
			Previews: []dashboard.PreviewItem{
				{
					Slug:      "demo-pr-42",
					URL:       "https://pr-42.demo.apps.gregale.dev",
					PrNumber:  42,
					PrState:   "open",
					ExpiresAt: "2026-08-20T12:00:00Z",
				},
				{
					Slug:      "demo-pr-43",
					URL:       "https://pr-43.demo.apps.gregale.dev",
					PrNumber:  43,
					PrState:   "closed",
					ExpiresAt: "2026-08-21T12:00:00Z",
				},
			},
		},
	}
	if err := dashboard.Render(rec, log, nonce, page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Preview environments",
		"PR #42",
		"PR #43",
		"demo-pr-42",
		"demo-pr-43",
		"https://pr-42.demo.apps.gregale.dev",
		"https://pr-43.demo.apps.gregale.dev",
		"preview-state-open",
		"preview-state-closed",
		"preview-copy-btn",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestRender_AppDetail_PreviewPanel_SuppressedForPreviewRow
// pins the recursive-suppression contract: a preview row never
// renders its own Preview environments section (the production
// parent already surfaces the preview).
func TestRender_AppDetail_PreviewPanel_SuppressedForPreviewRow(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo-pr-42",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{
				Slug:      "demo-pr-42",
				AppID:     "demo-pr-42-uuid",
				Status:    "active",
				URL:       "https://pr-42.demo.apps.gregale.dev",
				IsPreview: true,
				Scope:     "pr-42.demo",
			},
			// Even if a bug elsewhere populates Previews on a
			// preview row, the template must suppress the section.
			Previews: []dashboard.PreviewItem{
				{Slug: "demo-pr-99", URL: "https://pr-99.demo.apps.gregale.dev", PrNumber: 99, PrState: "open"},
			},
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Preview environments") {
		t.Errorf("preview row rendered a Preview environments section; must be suppressed")
	}
	if strings.Contains(body, "demo-pr-99") {
		t.Errorf("preview row leaked a nested preview row into the rendered HTML")
	}
}

// TestRender_AppDetail_PreviewPanel_EmptyState pins the
// empty-state line when a production app has no previews yet.
// Without this, a missing template branch renders as a heading
// with zero body content — visually identical to "panel failed
// to render", and worse than the explicit hint.
func TestRender_AppDetail_PreviewPanel_EmptyState(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App:      dashboard.AppListItem{Slug: "demo", AppID: "demo-uuid", Status: "active"},
			Previews: nil,
		},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Preview environments") {
		t.Errorf("body missing the section heading even on the empty path")
	}
	if !strings.Contains(body, "No preview environments yet") {
		t.Errorf("body missing the empty-state hint\n--- body ---\n%s", body)
	}
	if strings.Contains(body, `class="preview-copy-btn"`) {
		t.Errorf("empty state leaked a Copy URL button\n--- body ---\n%s", body)
	}
}

// TestRender_AppDetail_PreviewPanel_CarriesNonce pins that the
// inline <script> wiring the Copy URL buttons carries the page
// CSP nonce. Without this, CSP delivery turns the copy button
// into a silent no-op and a future refactor that drops the
// nonce breaks the customer experience without surfacing in any
// unit test.
func TestRender_AppDetail_PreviewPanel_CarriesNonce(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const nonce = "copy-nonce-22-chars-99"
	page := dashboard.Page{
		Title: "demo",
		Body:  "app_detail",
		Data: dashboard.AppDetailData{
			App: dashboard.AppListItem{Slug: "demo", AppID: "demo-uuid", Status: "active"},
			Previews: []dashboard.PreviewItem{
				{Slug: "demo-pr-1", URL: "https://pr-1.demo.apps.gregale.dev", PrNumber: 1, PrState: "open"},
			},
		},
	}
	if err := dashboard.Render(rec, log, nonce, page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	// The inline <script> tag wiring the copy buttons MUST
	// carry the nonce, otherwise CSP blocks it.
	if !strings.Contains(body, `<script nonce="`+nonce+`">`) {
		t.Errorf("preview copy-script missing nonce=%q\n--- body ---\n%s", nonce, body)
	}
	if !strings.Contains(body, "navigator.clipboard.writeText") {
		t.Errorf("preview copy-script missing clipboard.writeText call (template refactor dropped the handler)")
	}
}

// TestRender_AppDetail_PreviewPanel_AppsListChip pins that the
// apps-list table renders the "preview" chip + indentation for
// any row with IsPreview=true. Same shape as the structural pins
// the cron / alert panels use.
func TestRender_AppDetail_AppsList_PreviewChip(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	page := dashboard.Page{
		Title: "Apps",
		Body:  "apps_list",
		Data: dashboard.AppsListData{Items: []dashboard.AppListItem{
			{Slug: "demo", Status: "active"},
			{
				Slug:      "demo-pr-42",
				Status:    "active",
				URL:       "https://pr-42.demo.apps.gregale.dev",
				IsPreview: true,
				Scope:     "pr-42.demo",
			},
		}},
	}
	if err := dashboard.Render(rec, log, "", page); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, ">preview</span>") {
		t.Errorf("body missing the preview chip span\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "demo-pr-42") {
		t.Errorf("body missing preview row slug")
	}
}
