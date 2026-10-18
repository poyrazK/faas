package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlAccountRecoveryRouteAndGate(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "recovery-routing@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "recovery-routing"})
	if err != nil {
		t.Fatal(err)
	}
	authApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "recovery-routing-auth"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PutProductCatalog(ctx, account.ID, app.ID, api.ProductCatalogSpec{AuthApp: authApp.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10}}}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{})
	lookups := 0
	srv.ProductControl().AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
		lookups++
		return authApp, "https://recovery-routing-auth.example.test", nil
	}
	path := "/v1/product-control/customer/" + app.Slug + "/account-recovery"
	call := func(method string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest(method, "https://api.example.test"+path, nil))
		return w
	}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		w := call(method)
		if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" || w.Header().Get("Cache-Control") != "no-store" || lookups != 0 {
			t.Fatalf("disabled recovery %s: %d", method, w.Code)
		}
	}
	srv.WithProductControlEnabled(true)
	for _, method := range []string{"GET", "HEAD"} {
		w := call(method)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://recovery-routing-auth.example.test/ui/forgot-password#" || w.Header().Get("Referrer-Policy") != "no-referrer" || len(w.Result().Cookies()) != 0 {
			t.Fatalf("enabled recovery %s: %d %s", method, w.Code, w.Body.String())
		}
	}
	if w := call("POST"); w.Code != http.StatusMethodNotAllowed || !strings.Contains(w.Header().Get("Allow"), "GET") || lookups != 2 {
		t.Fatal("recovery route accepted an unsupported mutation")
	}
}
