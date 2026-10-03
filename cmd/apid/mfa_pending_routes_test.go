package main

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
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
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
	e := setupWithMFA(t, api.PlanPro, false, true)
	if _, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID, Slug: "mfa-pending-app", Status: state.AppActive, RAMMB: 256,
	}); err != nil {
		t.Fatal(err)
	}
	pending := e.mfaIssueWithPending(t, true)
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
		e.h.ServeHTTP(rec, req)
		cancel()
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeMFARequired) {
			t.Errorf("%s with an mfa_pending session = %d, want 403 %s: %.160s", route, rec.Code, api.CodeMFARequired, rec.Body.String())
		}
	}
}
