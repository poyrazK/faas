package routeimpact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGoChiIndexesMethodsAndNestedRouteGroups(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import (
	"net/http"
	router "github.com/go-chi/chi/v5"
)
func routes() router.Router {
	r := router.NewRouter()
	r.Get("/health", health)
	r.Method(http.MethodGet, "/status", health)
	r.Route("/api", func(api router.Router) {
		api.Get("/", health)
		api.Route("/v1", func(v1 router.Router) { v1.Post("/items", create) })
		api.Group(func(scoped router.Router) { scoped.Method("PATCH", "/items/{id}", update) })
	})
	return r
}
func health(http.ResponseWriter, *http.Request) {}
func create(http.ResponseWriter, *http.Request) {}
func update(http.ResponseWriter, *http.Request) {}
func registerConcrete(router *router.Mux) { router.Get("/concrete", health) }
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /api", "GET /api/", "GET /concrete", "GET /health", "GET /status", "PATCH /api/items/{id}", "POST /api/v1/items"}
	got := make([]string, 0, len(index.Routes))
	for _, route := range index.Routes {
		got = append(got, route.Method+" "+route.Path)
		if route.HandlerSymbol == "" || route.RegistrationHash == "" {
			t.Errorf("Chi route missing source links: %+v", route)
		}
	}
	if !reflect.DeepEqual(got, want) || len(index.Issues) != 0 {
		t.Fatalf("Chi index routes=%v issues=%+v, want routes=%v and no issues", got, index.Issues, want)
	}
}

func TestGoChiFlagsDynamicPrefixesAndMiddleware(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import (
	"net/http"
	"github.com/go-chi/chi/v5"
)
func routes(prefix string, router chi.Router) {
	router.Use(makeMiddleware())
	router.Route(prefix, func(child chi.Router) { child.Get("/items", list) })
	router.Get("/public", list)
}
func list(http.ResponseWriter, *http.Request) {}
func makeMiddleware() func(http.Handler) http.Handler { return func(next http.Handler) http.Handler { return next } }
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 1 || index.Routes[0].Path != "/public" {
		t.Fatalf("dynamic prefix hid a stable sibling route: %+v", index.Routes)
	}
	codes := map[string]bool{}
	for _, issue := range index.Issues {
		codes[issue.Code] = true
	}
	if !codes["dynamic_chi_route_prefix"] || !codes["dynamic_chi_middleware"] {
		t.Fatalf("dynamic prefix or middleware uncertainty missing: %+v", index.Issues)
	}
}

func TestGoChiExpandsRouterHelpersUnderEachNestedPrefix(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import (
	"net/http"
	router "github.com/go-chi/chi/v5"
)
func register(api router.Router) {
	api.Use(auth)
	api.Get("/users", handler)
}
func routes() {
	r := router.NewRouter()
	r.Route("/v1", func(v1 router.Router) { register(v1) })
	r.Route("/v2", func(v2 router.Router) { register(v2) })
}
func handler(http.ResponseWriter, *http.Request) {}
func auth(http.Handler) http.Handler { return nil }
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"/v1/users": true, "/v2/users": true}
	for _, route := range index.Routes {
		if route.Method != "GET" || !reflect.DeepEqual(route.DependencySymbols, []string{"go:example.com/store.auth"}) || !want[route.Path] {
			t.Errorf("unexpected expanded Chi route: %+v", route)
		}
		delete(want, route.Path)
	}
	if len(index.Issues) != 0 || len(want) != 0 {
		t.Fatalf("Chi helper routes missing=%v issues=%+v", want, index.Issues)
	}
}

func TestGoChiExpandsSameModuleImportedRouteHelpers(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes/routes.go": `package routes
import (
	"github.com/go-chi/chi/v5"
	"example.com/store/registration"
)
func routes() {
	r := chi.NewRouter()
	r.Route("/api", func(api chi.Router) { registration.Register(api) })
}
`,
		"registration/routes.go": `package registration
import (
	"net/http"
	"github.com/go-chi/chi/v5"
)
func Register(api chi.Router) { api.Get("/items", list) }
func list(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Issues) != 0 || len(index.Routes) != 1 || index.Routes[0].Method != "GET" || index.Routes[0].Path != "/api/items" ||
		index.Routes[0].HandlerSymbol != "go:example.com/store/registration.list" {
		t.Fatalf("same-module Chi helper did not retain its call-site context: %+v", index)
	}
}

func TestGoChiReportsRecursiveAndDynamicRouterHelpers(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import (
	"net/http"
	router "github.com/go-chi/chi/v5"
)
func register(r router.Router, prefix string) {
	recursive(r, prefix)
	r.Get("/safe", handler)
}
func recursive(r router.Router, prefix string) { register(r, prefix) }
func routes(prefix string) {
	r := router.NewRouter()
	route := r
	register(route, prefix)
	r.Route(prefix, func(child router.Router) { register(child, prefix) })
}
func handler(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range index.Issues {
		codes[issue.Code] = true
	}
	if !codes["chi_route_helper_cycle"] || !codes["dynamic_chi_route_prefix"] {
		t.Fatalf("Chi helper uncertainty missing: %+v", index.Issues)
	}
	if len(index.Routes) != 1 || index.Routes[0].Path != "/safe" {
		t.Fatalf("Chi helper expansion invented or dropped routes: %+v", index.Routes)
	}
}

func TestGoChiLinksScopedMiddlewareDependencies(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import (
	"net/http"
	"github.com/go-chi/chi/v5"
)
func routes() {
	r := chi.NewRouter()
	r.Use(auth)
	r.Get("/root", handler)
	r.With(audit).Get("/with", handler)
	secured := r.With(auth)
	secured.Get("/alias", handler)
	r.Route("/api", func(api chi.Router) {
		api.Use(audit)
		api.Get("/items", handler)
		api.Group(func(child chi.Router) { child.Get("/nested", handler) })
	})
	other := chi.NewRouter()
	other.Get("/other", handler)
}
func handler(http.ResponseWriter, *http.Request) {}
func auth(http.Handler) http.Handler { return nil }
func audit(http.Handler) http.Handler { return nil }
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Issues) != 0 {
		t.Fatalf("unexpected middleware uncertainty: %+v", index.Issues)
	}
	want := map[string][]string{
		"/root":       {"go:example.com/store.auth"},
		"/with":       {"go:example.com/store.auth", "go:example.com/store.audit"},
		"/alias":      {"go:example.com/store.auth", "go:example.com/store.auth"},
		"/api/items":  {"go:example.com/store.auth", "go:example.com/store.audit"},
		"/api/nested": {"go:example.com/store.auth", "go:example.com/store.audit"},
		"/other":      {},
	}
	for _, route := range index.Routes {
		if !reflect.DeepEqual(route.DependencySymbols, want[route.Path]) {
			t.Errorf("%s middleware roots=%v, want %v", route.Path, route.DependencySymbols, want[route.Path])
		}
		delete(want, route.Path)
	}
	if len(want) != 0 {
		t.Fatalf("expected routes were not indexed: %v", want)
	}
}

func TestGoChiMiddlewareChangesAffectOnlyProtectedRoutes(t *testing.T) {
	baseSources := map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes/routes.go": `package routes
import (
	"net/http"
	"github.com/go-chi/chi/v5"
	"example.com/store/middleware"
)
func routes() {
	r := chi.NewRouter()
	r.With(middleware.Auth, middleware.Audit).Get("/secure", handler)
	r.Get("/public", handler)
}
func handler(http.ResponseWriter, *http.Request) {}
`,
		"middleware/auth.go": `package middleware
import "net/http"
func Auth(next http.Handler) http.Handler { return next }
func Audit(next http.Handler) http.Handler { return next }
`,
	}
	base := testSnapshot(baseSources)
	base.meta.Revision = strings.Repeat("a", 40)
	before, err := indexSource(t.Context(), base, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	changed := cloneSources(baseSources)
	changed["middleware/auth.go"] = `package middleware
import "net/http"
func Auth(next http.Handler) http.Handler { if next == nil { return nil }; return next }
func Audit(next http.Handler) http.Handler { return next }
`
	afterSnapshot := testSnapshot(changed)
	afterSnapshot.meta.Revision = strings.Repeat("b", 40)
	after, err := indexSource(t.Context(), afterSnapshot, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	report, err := compare(base, afterSnapshot, before, after, "", "", "go-nethttp")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Summary.NoLinkedChanges != 1 {
		t.Fatalf("middleware change was not scoped to the protected route: %+v", report)
	}
	if report.Routes[0].Path != "/public" || report.Routes[0].Change != "no_linked_changes" || report.Routes[1].Path != "/secure" || report.Routes[1].Change != "potentially_affected" {
		t.Fatalf("unexpected middleware route impact: %+v", report.Routes)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseReport(body); err != nil {
		t.Fatalf("middleware dependency report did not pass report validation: %v", err)
	}

	removedMiddleware := cloneSources(baseSources)
	removedMiddleware["routes/routes.go"] = strings.Replace(baseSources["routes/routes.go"], "r.With(middleware.Auth, middleware.Audit).Get", "r.Get", 1)
	removedSnapshot := testSnapshot(removedMiddleware)
	removedSnapshot.meta.Revision = strings.Repeat("c", 40)
	removedIndex, err := indexSource(t.Context(), removedSnapshot, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	removedReport, err := compare(base, removedSnapshot, before, removedIndex, "", "", "go-nethttp")
	if err != nil {
		t.Fatal(err)
	}
	if removedReport.Summary.SourceChanged != 1 || removedReport.Summary.NoLinkedChanges != 1 ||
		removedReport.Routes[0].Path != "/public" || removedReport.Routes[0].Change != "no_linked_changes" ||
		removedReport.Routes[1].Path != "/secure" || removedReport.Routes[1].Change != "source_changed" {
		t.Fatalf("middleware removal was not attributed to the protected route: %+v", removedReport)
	}

	reorderedMiddleware := cloneSources(baseSources)
	reorderedMiddleware["routes/routes.go"] = strings.Replace(baseSources["routes/routes.go"], "r.With(middleware.Auth, middleware.Audit).Get", "r.With(middleware.Audit, middleware.Auth).Get", 1)
	reorderedSnapshot := testSnapshot(reorderedMiddleware)
	reorderedSnapshot.meta.Revision = strings.Repeat("d", 40)
	reorderedIndex, err := indexSource(t.Context(), reorderedSnapshot, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	reorderedReport, err := compare(base, reorderedSnapshot, before, reorderedIndex, "", "", "go-nethttp")
	if err != nil {
		t.Fatal(err)
	}
	if reorderedReport.Summary.SourceChanged != 1 || reorderedReport.Summary.NoLinkedChanges != 1 || reorderedReport.Routes[1].Change != "source_changed" {
		t.Fatalf("middleware order change was not attributed to the protected route: %+v", reorderedReport)
	}
}

func TestGoChiExpandsLocalMountsAndNestedMounts(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import (
	"net/http"
	"github.com/go-chi/chi/v5"
)
func routes() {
	root := chi.NewRouter()
	root.Use(rootAuth)
	admin := chi.NewRouter()
	admin.Use(adminAuth)
	v1 := chi.NewRouter()
	v1.Use(versionAuth)
	v1.Get("/", handler)
	v1.Get("/users", handler)
	admin.Mount("/v1", v1)
	admin.Get("/home", handler)
	root.Mount("/admin", admin)
	root.Route("/nested", func(nested chi.Router) { nested.Mount("/v1", v1) })
}
func handler(http.ResponseWriter, *http.Request) {}
func rootAuth(http.Handler) http.Handler { return nil }
func adminAuth(http.Handler) http.Handler { return nil }
func versionAuth(http.Handler) http.Handler { return nil }
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Issues) != 0 {
		t.Fatalf("unexpected mount uncertainty: %+v", index.Issues)
	}
	want := map[string][]string{
		"/":                {"go:example.com/store.versionAuth"},
		"/users":           {"go:example.com/store.versionAuth"},
		"/v1":              {"go:example.com/store.adminAuth", "go:example.com/store.versionAuth"},
		"/v1/":             {"go:example.com/store.adminAuth", "go:example.com/store.versionAuth"},
		"/v1/users":        {"go:example.com/store.adminAuth", "go:example.com/store.versionAuth"},
		"/admin/v1":        {"go:example.com/store.rootAuth", "go:example.com/store.adminAuth", "go:example.com/store.versionAuth"},
		"/admin/v1/":       {"go:example.com/store.rootAuth", "go:example.com/store.adminAuth", "go:example.com/store.versionAuth"},
		"/admin/v1/users":  {"go:example.com/store.rootAuth", "go:example.com/store.adminAuth", "go:example.com/store.versionAuth"},
		"/nested/v1":       {"go:example.com/store.rootAuth", "go:example.com/store.versionAuth"},
		"/nested/v1/":      {"go:example.com/store.rootAuth", "go:example.com/store.versionAuth"},
		"/nested/v1/users": {"go:example.com/store.rootAuth", "go:example.com/store.versionAuth"},
		"/home":            {"go:example.com/store.adminAuth"},
		"/admin/home":      {"go:example.com/store.rootAuth", "go:example.com/store.adminAuth"},
	}
	for _, route := range index.Routes {
		if !reflect.DeepEqual(route.DependencySymbols, want[route.Path]) {
			t.Errorf("%s dependencies=%v, want %v", route.Path, route.DependencySymbols, want[route.Path])
		}
		delete(want, route.Path)
	}
	if len(want) != 0 {
		t.Fatalf("expected mounted routes were not indexed: %v", want)
	}
}

func TestGoChiReportsDynamicMountsAsIncomplete(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import (
	"net/http"
	"github.com/go-chi/chi/v5"
)
func routes(prefix string, root, child chi.Router) {
	root.Mount(prefix, child)
	child.Get("/users", handler)
	root.Mount("/api", buildRouter())
}
func buildRouter() chi.Router { return chi.NewRouter() }
func handler(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range index.Issues {
		codes[issue.Code] = true
	}
	if !codes["dynamic_chi_mount_prefix"] || !codes["unmodeled_chi_mount"] {
		t.Fatalf("dynamic path or target uncertainty missing: %+v", index.Issues)
	}
	if len(index.Routes) != 1 || index.Routes[0].Path != "/users" {
		t.Fatalf("dynamic mount was treated as a stable path: %+v", index.Routes)
	}
}

func TestGoChiBoundsCyclicMountExpansion(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import (
	"net/http"
	"github.com/go-chi/chi/v5"
)
func routes() {
	a := chi.NewRouter()
	b := chi.NewRouter()
	a.Get("/health", handler)
	a.Mount("/b", b)
	b.Mount("/a", a)
}
func handler(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) < 2 || len(index.Routes) > 4 {
		t.Fatalf("cyclic mounts were not bounded: routes=%+v issues=%+v", index.Routes, index.Issues)
	}
	for _, issue := range index.Issues {
		if issue.Code == "chi_mount_cycle" {
			return
		}
	}
	t.Fatalf("cyclic mount uncertainty missing: %+v", index.Issues)
}

func TestGoChiDoesNotRecognizeUnrelatedRouterNames(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
type customRouter struct{}
func register(router *customRouter) { router.Get("/not-chi", nil) }
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 0 {
		t.Fatalf("unrelated router was treated as Chi: %+v", index.Routes)
	}
}

func TestGoChiBoundsNestedRouteGroups(t *testing.T) {
	var source strings.Builder
	source.WriteString("package routes\nimport chi \"github.com/go-chi/chi/v5\"\nfunc routes() { r := chi.NewRouter()\n")
	for range api.RouteImpactMaxGraphDepth + 1 {
		source.WriteString(`r.Route("/nested", func(r chi.Router) {`)
	}
	source.WriteString(`r.Get("/health", health)`)
	for range api.RouteImpactMaxGraphDepth + 1 {
		source.WriteString("})")
	}
	source.WriteString("}\nfunc health() {}\n")
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod":    "module example.com/store\n",
		"routes.go": source.String(),
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 0 {
		t.Fatalf("route beyond the group depth bound was reported: %+v", index.Routes)
	}
	for _, issue := range index.Issues {
		if issue.Code == "chi_route_group_depth_exceeded" {
			return
		}
	}
	t.Fatalf("route group depth uncertainty missing: %+v", index.Issues)
}

func TestGoChiPathJoinEnforcesReportPathLimit(t *testing.T) {
	part := "/" + strings.Repeat("x", api.RouteImpactMetadataMaxBytes/2)
	if _, ok := goChiJoinPath(part, part, false); ok {
		t.Fatal("joined Chi route path exceeded the report metadata limit")
	}
}
