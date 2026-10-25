package apidserver_test

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/durableentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// scanSessionAuthRoutes returns every server.go route whose handler chain
// authenticates through s.auth or s.authLimited (and so accepts the
// dashboard session cookie).
func scanSessionAuthRoutes(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, serverSrcPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", serverSrcPath, err)
	}
	var routes []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		route, err := strconvUnquote(lit.Value)
		if err != nil || !strings.Contains(route, " /v1/") {
			return true
		}
		var chain bytes.Buffer
		if err := printer.Fprint(&chain, fset, call.Args[1]); err != nil {
			t.Fatalf("print %s: %v", route, err)
		}
		if strings.Contains(chain.String(), "s.auth(") || strings.Contains(chain.String(), "s.authLimited(") {
			routes = append(routes, route)
		}
		return true
	})
	return routes
}

// TestSessionRoutesRefuseMFAPendingCookies walks every cookie-authenticated
// /v1 route with an mfa_pending session (password verified, TOTP not yet).
// requireMFA used to be opt-in per route; the ones that forgot it let a
// stolen password alone rewrite an app's env (DATABASE_URL), upstreams and
// custom metrics, and read builds, errors and analytics.
func TestSessionRoutesRefuseMFAPendingCookies(t *testing.T) {
	routes := scanSessionAuthRoutes(t)
	if len(routes) < 300 {
		t.Fatalf("only %d session routes found; the scan is broken", len(routes))
	}
	// With its runtime flag off, the internal hosting namespace answers the
	// missing-route 404 before auth (ADR 202610171453). Enable it before the
	// server reads its runtime config so the hosting routes' MFA enforcement
	// stays covered.
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "true")
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, true)
	// Outside the live-query rollout those routes answer the missing-route 404
	// before auth (ADR-20261016151947). Open it so their MFA enforcement stays
	// covered.
	if err := (*e.Srv()).ConfigureLiveQueriesForTest(func(key string) string {
		if key == "FAAS_LIVE_QUERIES_ACCOUNTS" {
			return "*"
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
	// Outside the realtime preview rollout those routes answer the
	// missing-route 404 before auth (ADR-20261019102657). Open it so their MFA
	// enforcement stays covered.
	(*e.Srv()).WithRealtimePreview(apidserver.RealtimePreviewEveryone())
	// With the preview off, durable entity routes answer 404 before auth.
	// Enable it so their MFA enforcement stays covered.
	(*e.Srv()).Durable().Engine = &durableentity.Manager{}
	if _, err := (*e.Store()).CreateApp(context.Background(), state.App{
		AccountID: (*e.Acct()).ID, Slug: "mfa-pending-app", Status: state.AppActive, RAMMB: 256,
	}); err != nil {
		t.Fatal(err)
	}
	pending := e.MfaIssueWithPending(t, true)
	for i, route := range routes {
		method, path, _ := strings.Cut(route, " ")
		if authmw.IsMFAAllowlisted(path) {
			continue
		}
		concrete := strings.ReplaceAll(path, "{slug}", "mfa-pending-app")
		concrete = routeParamPattern.ReplaceAllString(concrete, uuid.NewString())
		concrete = strings.TrimSuffix(concrete, "{$}")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req := httptest.NewRequest(method, concrete, strings.NewReader("{}")).WithContext(ctx)
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", i%250+1)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(pending)
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		cancel()
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeMFARequired) {
			t.Errorf("%s with an mfa_pending session = %d, want 403 %s: %.160s", route, rec.Code, api.CodeMFARequired, rec.Body.String())
		}
	}
}
