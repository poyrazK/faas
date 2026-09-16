package main

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
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedDeveloperEnvironmentForDashboard(t *testing.T, e testEnv, slug, parent string) state.App {
	t.Helper()
	expires := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID:        e.acct.ID,
		Slug:             slug,
		Type:             state.AppTypeApp,
		Runtime:          "node22",
		Status:           state.AppActive,
		PreviewOfSlug:    parent,
		PreviewPrNumber:  0,
		PreviewPrState:   state.PreviewPrStateOpen,
		PreviewExpiresAt: &expires,
		MaxConcurrency:   1,
		RAMMB:            256,
		IdleTimeoutS:     30,
	})
	if err != nil {
		t.Fatalf("CreateApp(%q): %v", slug, err)
	}
	return app
}

func TestRenderDeveloperEnvironments_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := seedDeveloperEnvironmentForDashboard(t, e, "dev-acme", "acme")
	createdAt := time.Date(2026, 9, 17, 9, 30, 0, 0, time.UTC)
	dep, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:     app.ID,
		Kind:      state.DeploymentKindImage,
		Status:    state.DeployLive,
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if _, err := e.store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateRunning), 256, "node-1", "wake-1"); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := httptest.NewRequest(http.MethodGet, "/dashboard/developers", nil)
	r = r.WithContext(httpsec.WithNonce(r.Context(), "test-nonce"))
	w := httptest.NewRecorder()
	e.s.renderDeveloperEnvironments(w, r, log, e.acct)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"Developer environments",
		"dev-acme",
		"https://dev-acme.gregale.dev",
		"node22",
		"● running",
		"live",
		dep.ID,
		"/dashboard/apps/dev-acme/logs",
		"/dashboard/apps/dev-acme#analytics",
		"2026-09-24 12:00 UTC",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
}

func TestRenderDeveloperEnvironments_FiltersPreviewAndOtherAccounts(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedDeveloperEnvironmentForDashboard(t, e, "dev-visible", "visible")
	if _, err := e.store.CreateApp(context.Background(), state.App{
		AccountID:       e.acct.ID,
		Slug:            "pr-preview",
		Type:            state.AppTypeApp,
		Status:          state.AppActive,
		PreviewOfSlug:   "visible",
		PreviewPrNumber: 42,
	}); err != nil {
		t.Fatalf("CreateApp(PR preview): %v", err)
	}
	other, err := e.store.CreateAccount(context.Background(), "other-dashboard@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount(other): %v", err)
	}
	if _, err := e.store.CreateApp(context.Background(), state.App{
		AccountID:       other.ID,
		Slug:            "dev-other",
		Type:            state.AppTypeApp,
		Status:          state.AppActive,
		PreviewOfSlug:   "other-parent",
		PreviewPrNumber: 0,
	}); err != nil {
		t.Fatalf("CreateApp(other developer): %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := httptest.NewRequest(http.MethodGet, "/dashboard/developers", nil)
	r = r.WithContext(httpsec.WithNonce(r.Context(), "test-nonce"))
	w := httptest.NewRecorder()
	e.s.renderDeveloperEnvironments(w, r, log, e.acct)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "dev-visible") {
		t.Fatalf("body missing visible developer environment: %s", body)
	}
	for _, forbidden := range []string{"pr-preview", "dev-other", "other-parent"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body rendered %q outside the developer account filter: %s", forbidden, body)
		}
	}
}

func TestRenderDeveloperEnvironments_EmptyState(t *testing.T) {
	e := setup(t, api.PlanPro)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := httptest.NewRequest(http.MethodGet, "/dashboard/developers", nil)
	r = r.WithContext(httpsec.WithNonce(r.Context(), "test-nonce"))
	w := httptest.NewRecorder()
	e.s.renderDeveloperEnvironments(w, r, log, e.acct)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No developer environments yet") || !strings.Contains(body, "gregale dev --open") {
		t.Fatalf("body missing developer empty state: %s", body)
	}
	if strings.Contains(body, "<table>") {
		t.Fatalf("empty developer account rendered a table: %s", body)
	}
}
