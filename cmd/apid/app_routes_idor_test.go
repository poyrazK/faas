package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestAppRoutesRefuseAnotherAccountsSlug walks every /v1/apps/{slug}
// route with an admin key of one account against another account's app.
// Slugs are public (they are the app's hostname), so every route must
// refuse without reflecting the other account's data.
func TestAppRoutesRefuseAnotherAccountsSlug(t *testing.T) {
	e := setupWithScopes(t, api.ScopesAdminOnly)
	if err := e.store.MarkAccountEmailVerified(context.Background(), e.acct.ID); err != nil {
		t.Fatal(err)
	}
	victim, err := e.store.CreateAccount(context.Background(), "victim@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateApp(context.Background(), state.App{AccountID: victim.ID, Slug: "victim-app", Status: state.AppActive, RAMMB: 256}); err != nil {
		t.Fatal(err)
	}
	routes := scanSessionAuthRoutes(t)
	checked := 0
	for i, route := range routes {
		method, path, _ := strings.Cut(route, " ")
		if !strings.Contains(path, "{slug}") || !strings.HasPrefix(path, "/v1/apps/") {
			continue
		}
		checked++
		concrete := strings.ReplaceAll(path, "{slug}", "victim-app")
		concrete = routeParamPattern.ReplaceAllString(concrete, uuid.NewString())
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req := httptest.NewRequest(method, concrete, strings.NewReader("{}")).WithContext(ctx)
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", i%250+1)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.key)
		req.Header.Set("Idempotency-Key", uuid.NewString())
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		cancel()
		if rec.Code < 400 || strings.Contains(rec.Body.String(), victim.ID) {
			t.Errorf("%s against another account's app = %d: %.200s", route, rec.Code, rec.Body.String())
		}
	}
	if checked < 200 {
		t.Fatalf("only %d app routes found; the scan is broken", checked)
	}
}

// TestIDRoutesRefuseAnotherAccountsResources substitutes another account's
// deployment, cron and app IDs into every ID-keyed cookie/bearer route.
// None may succeed or reflect the other account's data.
func TestIDRoutesRefuseAnotherAccountsResources(t *testing.T) {
	e := setupWithScopes(t, api.ScopesAdminOnly)
	ctx := context.Background()
	_ = e.store.MarkAccountEmailVerified(ctx, e.acct.ID)
	victim, _ := e.store.CreateAccount(ctx, "victim@example.com", api.PlanPro)
	app, _ := e.store.CreateApp(ctx, state.App{AccountID: victim.ID, Slug: "victim-app", Status: state.AppActive, RAMMB: 256})
	dep, _ := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending, SourceSHA256: "x"})
	cron, _ := e.store.CreateCron(ctx, app.ID, "*/5 * * * *", "/tick", true)
	ids := map[string]string{"deployment": dep.ID, "cron": cron.ID, "app": app.ID}
	routes := scanSessionAuthRoutes(t)
	for i, route := range routes {
		method, path, _ := strings.Cut(route, " ")
		// Dev sessions are keyed by a caller-chosen project name and
		// create the caller's own app; they hold no foreign resource.
		if strings.Contains(path, "{slug}") || !strings.Contains(path, "{") || strings.HasPrefix(path, "/v1/dev/sessions/") {
			continue
		}
		for kind, id := range ids {
			concrete := routeParamPattern.ReplaceAllString(path, id)
			rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			req := httptest.NewRequest(method, concrete, strings.NewReader("{}")).WithContext(rctx)
			req.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", i%250+1)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+e.key)
			req.Header.Set("Idempotency-Key", uuid.NewString())
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, req)
			cancel()
			if rec.Code < 400 || strings.Contains(rec.Body.String(), victim.ID) || strings.Contains(rec.Body.String(), "victim-app") {
				t.Errorf("%s with another account's %s ID = %d: %.200s", route, kind, rec.Code, rec.Body.String())
			}
		}
	}
}
