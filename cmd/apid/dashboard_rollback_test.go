package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedDashboardRollback(t *testing.T) (http.Handler, *http.Cookie, *state.MemStore, state.App, state.Deployment, *http.Cookie, string) {
	t.Helper()
	h, sid, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "rollbackapp", Status: state.AppActive})
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	now := time.Now().UTC()
	prior, err := store.CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:prior", Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("prior deployment: %v", err)
	}
	if err := store.MarkDeploymentLive(t.Context(), prior.ID); err != nil {
		t.Fatalf("prior live: %v", err)
	}
	current, err := store.CreateDeployment(t.Context(), state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:current", Kind: state.DeploymentKindImage,
		Status: state.DeployBuilding, CreatedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("current deployment: %v", err)
	}
	if err := store.MarkDeploymentLive(t.Context(), current.ID); err != nil {
		t.Fatalf("current live: %v", err)
	}
	if err := store.MarkDeploymentSuperseded(t.Context(), prior.ID); err != nil {
		t.Fatalf("prior superseded: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/rollbackapp", nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET app detail: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var csrfCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == dashboardRollbackCSRFCookie {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("app detail did not set rollback CSRF cookie")
	}
	token := extractInputValue(rec.Body.String(), middleware.FormFieldName, "/dashboard/apps/rollbackapp/rollback")
	if token == "" || token != csrfCookie.Value {
		t.Fatalf("rollback form token/cookie mismatch: token=%q cookie=%q", token, csrfCookie.Value)
	}
	return h, sid, store, app, prior, csrfCookie, token
}

func TestDashboardRollback_HappyPath(t *testing.T) {
	h, sid, store, app, prior, csrfCookie, token := seedDashboardRollback(t)
	rec := dashboardPOST(t, h, sid, "/dashboard/apps/rollbackapp/rollback", map[string]string{
		middleware.FormFieldName: token,
		"deployment_id":          prior.ID,
	}, csrfCookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST rollback: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/dashboard/apps/rollbackapp?rollback=1" {
		t.Fatalf("Location=%q, want success flash", got)
	}
	target, err := store.DeploymentByID(t.Context(), prior.ID)
	if err != nil {
		t.Fatalf("rollback target: %v", err)
	}
	if target.Status != state.DeployLive {
		t.Fatalf("rollback target = %s/%s, want %s/live", target.ID, target.Status, prior.ID)
	}
	current, err := store.LatestDeployment(t.Context(), app.ID)
	if err != nil || current.Status != state.DeploySuperseded {
		t.Fatalf("previous current deployment = %#v, want superseded", current)
	}
	rows, err := store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0)
	if err != nil {
		t.Fatalf("deployments: %v", err)
	}
	var supersededID string
	for _, row := range rows {
		if row.Status == state.DeploySuperseded && row.ID != prior.ID {
			supersededID = row.ID
			break
		}
	}
	audits, err := store.ListDeploymentAudit(t.Context(), supersededID, 10)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(audits) != 1 || audits[0].Kind != state.DeployRolledBack {
		t.Fatalf("rollback audit = %#v, want one deploy.rolled_back row", audits)
	}
}

func TestDashboardRollback_RejectsInvalidCSRF(t *testing.T) {
	h, sid, _, _, prior, csrfCookie, _ := seedDashboardRollback(t)
	rec := dashboardPOST(t, h, sid, "/dashboard/apps/rollbackapp/rollback", map[string]string{
		middleware.FormFieldName: "wrong-token",
		"deployment_id":          prior.ID,
	}, csrfCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST rollback invalid csrf: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Invalid CSRF token") {
		t.Fatalf("missing csrf problem: %s", rec.Body.String())
	}
}
