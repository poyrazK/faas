package routeimpact

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestGoGinIndexesRoutesNestedGroupsMiddlewareAndMethods(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import (
	"net/http"
	router "github.com/gin-gonic/gin"
)
func routes() *router.Engine {
	r := router.New()
	r.Use(auth)
api := r.Group("/api", audit)
api.GET("/users/:id", getUser)
api.Use(tenantAuth)
api.Match([]string{http.MethodGet, "POST"}, "/mixed", getUser)
api.Any("/ready", ready)
	v1 := api.Group("/v1")
	v1.Handle(http.MethodPost, "/orders", router.HandlerFunc(createOrder))
	return r
}
func auth(*router.Context) {}
func audit(*router.Context) {}
func tenantAuth(*router.Context) {}
func getUser(*router.Context) {}
func ready(*router.Context) {}
func createOrder(*router.Context) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	wantRoutes := map[string]bool{}
	for _, method := range goGinMethods() {
		wantRoutes[method+"\x00/api/ready"] = true
	}
	wantRoutes["GET\x00/api/users/{id}"] = true
	wantRoutes["GET\x00/api/mixed"] = true
	wantRoutes["POST\x00/api/mixed"] = true
	wantRoutes["POST\x00/api/v1/orders"] = true
	got := make([]string, 0, len(index.Routes))
	for _, route := range index.Routes {
		key := route.Method + "\x00" + route.Path
		got = append(got, key)
		if !wantRoutes[key] {
			t.Errorf("unexpected Gin route %q", key)
		} else {
			delete(wantRoutes, key)
		}
		if route.RegistrationHash == "" {
			t.Errorf("Gin route lacks a registration fingerprint: %+v", route)
		}
		var wantDependencies []string
		switch route.Path {
		case "/api/users/{id}":
			wantDependencies = []string{"go:example.com/store.auth", "go:example.com/store.audit"}
		case "/api/ready", "/api/mixed", "/api/v1/orders":
			wantDependencies = []string{"go:example.com/store.auth", "go:example.com/store.audit", "go:example.com/store.tenantAuth"}
		}
		if !reflect.DeepEqual(route.DependencySymbols, wantDependencies) {
			t.Errorf("%s %s middleware roots = %v, want %v", route.Method, route.Path, route.DependencySymbols, wantDependencies)
		}
	}
	if len(index.Issues) != 0 {
		t.Fatalf("unexpected Gin route uncertainties: %+v", index.Issues)
	}
	if len(got) != 13 || len(wantRoutes) != 0 {
		t.Fatalf("Gin routes = %v, missing routes: %v", got, wantRoutes)
	}
}

func TestGoGinTracksPackageLevelGroupsAndMiddlewareFactories(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
var root = router.New()
var api = root.Group("/api", makeAuth())
func register() { api.GET("/users/:id", getUser) }
func makeAuth() router.HandlerFunc { return func(*router.Context) {} }
func getUser(*router.Context) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Issues) != 0 || len(index.Routes) != 1 || index.Routes[0].Method != "GET" || index.Routes[0].Path != "/api/users/{id}" ||
		!reflect.DeepEqual(index.Routes[0].DependencySymbols, []string{"go:example.com/store.makeAuth"}) {
		t.Fatalf("package-level Gin context was not resolved: %+v", index)
	}
}

func TestGoGinExpandsRouterHelpersAtEachCallSiteAndCarriesMiddleware(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
func register(api *router.RouterGroup) {
	api.Use(auth)
	api.GET("/users/:id", getUser)
}
func routes() {
	r := router.New()
	v1 := r.Group("/v1")
	v2 := r.Group("/v2")
	register(v1)
	v1.GET("/health", health)
	register(v2)
}
func auth(*router.Context) {}
func getUser(*router.Context) {}
func health(*router.Context) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"/v1/users/{id}": {"go:example.com/store.auth"},
		"/v1/health":     {"go:example.com/store.auth"},
		"/v2/users/{id}": {"go:example.com/store.auth"},
	}
	for _, route := range index.Routes {
		dependencies, ok := want[route.Path]
		if !ok || route.Method != "GET" || !reflect.DeepEqual(route.DependencySymbols, dependencies) {
			t.Errorf("unexpected expanded Gin route: %+v", route)
		}
		delete(want, route.Path)
	}
	if len(index.Issues) != 0 || len(want) != 0 {
		t.Fatalf("Gin helper routes missing=%v issues=%+v", want, index.Issues)
	}
}

func TestGoGinDoesNotInventRoutesForDynamicOrRecursiveHelpers(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
func register(r *router.Engine, prefix string) {
	recursive(r, prefix)
	r.GET("/safe", handler)
}
func recursive(r *router.Engine, prefix string) { register(r, prefix) }
func routes(prefix string) {
	r := router.New()
	register(r, prefix)
	register(r.Group(prefix), prefix)
}
func handler(*router.Context) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range index.Issues {
		codes[issue.Code] = true
	}
	if !codes["gin_route_helper_cycle"] || !codes["dynamic_gin_route_helper"] || !codes["dynamic_gin_route_prefix"] {
		t.Fatalf("Gin helper uncertainty missing: %+v", index.Issues)
	}
	for _, route := range index.Routes {
		if strings.Contains(route.Path, "prefix") || strings.HasPrefix(route.Path, "/safe") && route.Path != "/safe" {
			t.Errorf("dynamic Gin prefix produced a route: %+v", route)
		}
	}
}

func TestGoGinMiddlewareChangesThroughGroupAliasesApplyToSharedRoutes(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
func register() {
	r := router.New()
	api := r.Group("/api")
	alias := api
	alias.Use(auth)
	api.GET("/users", getUsers)
}
func auth(*router.Context) {}
func getUsers(*router.Context) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Issues) != 0 || len(index.Routes) != 1 || !reflect.DeepEqual(index.Routes[0].DependencySymbols, []string{"go:example.com/store.auth"}) {
		t.Fatalf("Gin group pointer alias lost middleware state: %+v", index)
	}
}

func TestGoGinFactoryReturnTypesMapRoutesButStayIncomplete(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
func newRouter() *router.Engine { return router.New() }
func register() {
	r := newRouter()
	r.GET("/health", health)
}
func health(*router.Context) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 1 || index.Routes[0].Path != "/health" || len(index.Issues) == 0 {
		t.Fatalf("typed Gin factory did not map routes with an uncertainty: %+v", index)
	}
	found := false
	for _, issue := range index.Issues {
		if issue.Code == "dynamic_gin_router_factory" {
			found = true
		}
	}
	if !found {
		t.Fatalf("factory implementation uncertainty missing: %+v", index.Issues)
	}
}

func TestGoGinDoesNotAssumeConditionalRoutesAreRegistered(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
func register(enabled bool) {
	r := router.New()
	if enabled { r.GET("/optional", handler) }
	r.GET("/stable", handler)
}
func handler(*router.Context) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 1 || index.Routes[0].Path != "/stable" {
		t.Fatalf("conditional Gin registration was treated as unconditional or hid a stable sibling: %+v", index.Routes)
	}
	for _, issue := range index.Issues {
		if issue.Code == "dynamic_gin_control_flow" {
			return
		}
	}
	t.Fatalf("conditional registration uncertainty missing: %+v", index.Issues)
}

func TestGoGinLinksMiddlewareChangesOnlyToScopedRoutes(t *testing.T) {
	baseSources := map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
func routes() {
	r := router.New()
	api := r.Group("/api")
	api.Use(auth)
	api.GET("/users/:id", getUser)
	r.GET("/health", health)
}
func auth(*router.Context) { _ = "before" }
func getUser(*router.Context) {}
func health(*router.Context) {}
`,
	}
	base := testSnapshot(baseSources)
	base.meta.Revision = strings.Repeat("a", 40)
	before, err := indexSource(t.Context(), base, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	changedSources := cloneSources(baseSources)
	changedSources["routes.go"] = strings.Replace(changedSources["routes.go"], `"before"`, `"after"`, 1)
	candidate := testSnapshot(changedSources)
	candidate.meta.Revision = strings.Repeat("b", 40)
	after, err := indexSource(t.Context(), candidate, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	report, err := compare(base, candidate, before, after, ".", "", "go-nethttp")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Summary.NoLinkedChanges != 1 ||
		report.Routes[0].Path != "/api/users/{id}" || report.Routes[0].Change != "potentially_affected" || report.Routes[1].Path != "/health" || report.Routes[1].Change != "no_linked_changes" {
		t.Fatalf("Gin middleware change escaped its route group: %+v", report)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseReport(body); err != nil {
		t.Fatalf("Gin source impact report failed validation: %v", err)
	}
}

func TestGoGinKeepsDynamicAndCatchAllRoutesExplicit(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
func routes(r *router.Engine, prefix, pattern string) {
	group := r.Group(prefix)
	group.GET("/users/:id", getUser)
	r.GET(pattern, getUser)
	r.Any("/all", getUser)
	r.StaticFile("/favicon.ico", "./favicon.ico")
	r.Static("/assets", "./assets")
}
func getUser(*router.Context) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range index.Issues {
		codes[issue.Code] = true
	}
	if !codes["dynamic_gin_route_prefix"] || !codes["gin_router_group_prefix_unknown"] || !codes["dynamic_gin_route_pattern"] || !codes["unsupported_gin_catch_all_route"] {
		t.Fatalf("dynamic Gin paths or catch-all uncertainty missing: %+v", index.Issues)
	}
	if len(index.Routes) != 13 {
		t.Fatalf("unsupported routes were either invented or stable sibling routes were lost: %+v", index.Routes)
	}
	staticFileMethods := map[string]bool{}
	for _, route := range index.Routes {
		if route.Path == "/users/{id}" || route.Path == "/" {
			t.Fatalf("a route with an unknown prefix or dynamic path was invented: %+v", route)
		}
		if route.Path == "/favicon.ico" {
			staticFileMethods[route.Method] = true
		}
	}
	if !staticFileMethods["GET"] || !staticFileMethods["HEAD"] {
		t.Fatalf("Gin StaticFile did not map both GET and HEAD: %+v", staticFileMethods)
	}
}

func TestGoGinDoesNotAttributeLookalikeRouterMethods(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"routes.go": `package routes
import router "github.com/gin-gonic/gin"
type fakeRouter struct{}
func (fakeRouter) GET(string, router.HandlerFunc) {}
func register() { fakeRouter{}.GET("/not-gin", func(*router.Context) {}) }
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 0 {
		t.Fatalf("an unrelated router method was attributed to Gin: %+v", index.Routes)
	}
}
