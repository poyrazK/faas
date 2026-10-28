package apidserver_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// departedCreatorAllowed lists app routes a member removed from the
// organization may still call on a company app they created, with the
// reason. Keep it empty unless a route is public by design.
var departedCreatorAllowed = map[string]string{}

var appRoutePattern = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE) /(v1/apps|dashboard/apps|dashboard/mcp/apps)/\{slug\}(/.*)?$`)

// dashboardAppPages are pages the "GET /dashboard/" subtree serves by path
// rather than by a route pattern; they share one gate on the app's slug.
var dashboardAppPages = []string{"GET /dashboard/apps/{slug}", "GET /dashboard/apps/{slug}/logs"}

// A member removed from an organization keeps no access to its apps, the
// ones they created included. A company app's account_id still names its
// creator, so a handler that loads the app by the caller's account instead
// of through the organization's access check hands it back to someone who
// left; the 2026-10 security audits found that class five times. This calls
// every /v1/apps/{slug}, /dashboard/apps/{slug} and /dashboard/mcp/apps/{slug}
// route written in apid as the departed creator and expects a refusal from
// each.
//
// adr: 20261026151103
func TestDepartedCreatorReachesNoCompanyAppRoute(t *testing.T) {
	useTestSealRecipient(t)
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	creator := *e.Acct()
	org := apidserver.SeedSharedOrgWithOwner(t, e, "departed", "Departed", api.PlanPro)
	if r := e.Do(t, http.MethodPost, "/v1/orgs/"+org.Slug+"/apps", api.CreateAppRequest{Slug: "company-api"}, nil); r.Code != http.StatusCreated {
		t.Fatalf("create company app = %d %s", r.Code, r.Body)
	}
	admin, asAdmin := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	if err := store.TransferOrgOwnership(t.Context(), org.ID, creator.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	if r := e.Do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+creator.ID, nil, asAdmin); r.Code >= 300 {
		t.Fatalf("remove creator = %d %s", r.Code, r.Body)
	}
	if r := e.Do(t, http.MethodGet, "/v1/apps/company-api", nil, asAdmin); r.Code != http.StatusOK {
		t.Fatalf("admin reading the company app = %d %s", r.Code, r.Body)
	}

	routes := appRoutes(t)
	if len(routes) < 500 {
		t.Fatalf("found %d app routes; the source scan is broken", len(routes))
	}
	// The creator signs in both ways: an API key, and a dashboard session
	// for the routes that only take one.
	sessionToken, err := apidserver.MintDashboardSession(t.Context(), store, *(*e.S()).Sessions(), creator.ID)
	if err != nil {
		t.Fatal(err)
	}
	credentials := map[string]func(*http.Request){
		"api key": func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+*e.Key()) },
		"session": func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sessionToken})
		},
	}
	handler := *e.H()
	for _, route := range routes {
		method, pattern, _ := strings.Cut(route, " ")
		for via, authenticate := range credentials {
			body := ""
			if method != http.MethodGet && method != http.MethodDelete {
				body = "{}"
			}
			// A stream that wrongly opens ends with the context, not the test.
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			req := httptest.NewRequestWithContext(ctx, method, appRoutePath(pattern, "company-api"), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			authenticate(req)
			r := httptest.NewRecorder()
			handler.ServeHTTP(r, req)
			cancel()
			if refused(r) {
				continue
			}
			if _, ok := departedCreatorAllowed[route]; ok {
				continue
			}
			t.Errorf("departed creator (%s): %s = %d %.200s", via, route, r.Code, r.Body)
		}
	}
}

// refused reports an error status or a redirect to sign in.
func refused(r *httptest.ResponseRecorder) bool {
	if r.Code >= 400 {
		return true
	}
	return r.Code >= 300 && strings.HasPrefix(r.Header().Get("Location"), "/login")
}

// appRoutes lists the app routes written as string literals in the
// non-test sources under pkg/apid, mutating routes after reads and deleting
// the app last, so a route that wrongly succeeds cannot hide the ones after
// it.
func appRoutes(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	err := filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil && appRoutePattern.MatchString(s) {
				seen[s] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range dashboardAppPages {
		seen[page] = true
	}
	rank := func(route string) int {
		switch {
		case route == "DELETE /v1/apps/{slug}":
			return 3
		case strings.HasPrefix(route, "DELETE "):
			return 2
		case strings.HasPrefix(route, "GET "):
			return 0
		}
		return 1
	}
	out := make([]string, 0, len(seen))
	for route := range seen {
		out = append(out, route)
	}
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// appRoutePath fills a route pattern: the app's slug, and an id no row has
// for every other parameter.
func appRoutePath(pattern, slug string) string {
	return pathParam.ReplaceAllStringFunc(pattern, func(p string) string {
		switch p {
		case "{slug}":
			return slug
		case "{$}":
			return ""
		}
		return "00000000-0000-4000-8000-000000000001"
	})
}
