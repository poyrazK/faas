package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	basemw "github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRequireSession_DeployTokenIsAppBound(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "mw-deploy-token@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "mw-app", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "mw-other", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	plain, hash, _ := api.GenerateDeployToken()
	if _, err := store.CreateDeployToken(context.Background(), acct.ID, app.ID, hash, "ci", []string{api.ScopeDeployWrite}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	mw := authmw.New(store, nil, nil, nil, nil, basemw.NewLimiter(basemw.AuthLimitConfig{}), nil)

	accepted := mw.RequireSession(func(w http.ResponseWriter, r *http.Request, got state.Account) {
		_, key, ok := authmw.AccountFromContext(r)
		if !ok || key == nil || key.AppID != app.ID || got.ID != acct.ID {
			t.Errorf("principal = account %v key %+v ok=%v", got.ID, key, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/mw-app/deployments", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	accepted.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("app-bound route status = %d, want 204", rec.Code)
	}

	forbidden := httptest.NewRecorder()
	badPath := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	badPath.Header.Set("Authorization", "Bearer "+plain)
	mw.RequireSession(func(http.ResponseWriter, *http.Request, state.Account) {})(forbidden, badPath)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("account route status = %d, want 403", forbidden.Code)
	}

	wrongApp := httptest.NewRecorder()
	wrongReq := httptest.NewRequest(http.MethodPost, "/v1/apps/mw-other/deployments", nil)
	wrongReq.Header.Set("Authorization", "Bearer "+plain)
	mw.RequireSession(func(w http.ResponseWriter, r *http.Request, got state.Account) {
		if _, ok := mw.LoadApp(w, r, got, other.Slug); ok {
			t.Error("LoadApp accepted deploy token for a different app")
		}
	})(wrongApp, wrongReq)
	if wrongApp.Code != http.StatusNotFound {
		t.Fatalf("wrong-app status = %d, want 404", wrongApp.Code)
	}
}
