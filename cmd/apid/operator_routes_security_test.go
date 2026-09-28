package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// isOperatorRoute reports whether a route is a provider (platform operator)
// surface. These must never be reachable by a customer account.
func isOperatorRoute(path string) bool {
	return strings.HasPrefix(path, "/v1/admin/") || path == "/v1/audit-log/all" ||
		path == "/v1/compute-nodes" || strings.HasPrefix(path, "/v1/compute-nodes/")
}

var routeParamPattern = regexp.MustCompile(`\{[^}]+\}`)

// TestOperatorRoutesRefuseCustomers walks every provider route in server.go
// with a customer that holds everything a customer can hold: an MFA-complete
// dashboard session with a fresh step-up stamp, and an admin-scope API key.
// requireScope(ScopesAdminOnly) admits both, so only the operator allowlist
// separates a tenant from the fleet. force-park / force-restart /
// force-cold-boot, node drain, the operator-intent read and the cross-account
// audit log all shipped without it.
func TestOperatorRoutesRefuseCustomers(t *testing.T) {
	routes, err := scanServerRoutes(serverSrcPath)
	if err != nil {
		t.Fatalf("scan routes: %v", err)
	}
	ctx := context.Background()
	store := state.NewMemStore()
	customer, err := store.CreateAccount(ctx, "customer@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, customer.ID, hash, "customer-admin", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).
		WithAdminAllowlist("ops@gregale.dev")
	sid := uuid.NewString()
	if _, err := store.CreateSession(ctx, sid, customer.ID, "192.0.2.10", "operator-routes-test"); err != nil {
		t.Fatal(err)
	}
	token, err := srv.sessions.IssueWithSessionAndBindingHashAndStepUp(sid, customer.ID, "", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	h := srv.handler()

	checked := 0
	for _, route := range routes {
		method, path, _ := strings.Cut(route, " ")
		if !isOperatorRoute(path) {
			continue
		}
		checked++
		concrete := routeParamPattern.ReplaceAllString(path, uuid.NewString())
		for _, cred := range []string{"session", "admin-key"} {
			reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			req := httptest.NewRequest(method, concrete+"?confirm=true", strings.NewReader("{}")).WithContext(reqCtx)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "operator-routes-"+uuid.NewString())
			if cred == "session" {
				req.AddCookie(cookie)
			} else {
				req.Header.Set("Authorization", "Bearer "+pt)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			cancel()
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with a customer %s = %d, want 401/403: %.200s", method, path, cred, rec.Code, rec.Body.String())
			}
		}
	}
	if checked < 50 {
		t.Fatalf("only %d operator routes found; the scan is broken", checked)
	}
}
