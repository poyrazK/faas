package routeimpact

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestGoNetHTTPIndexesDefaultMuxAndMethodPatterns(t *testing.T) {
	snapshot := testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"main.go": `package main
import "net/http"
func main() {
	http.HandleFunc("GET /users/{id}", getUser)
	http.Handle("POST /users", http.HandlerFunc(createUser))
}
func getUser(http.ResponseWriter, *http.Request) {}
func createUser(http.ResponseWriter, *http.Request) {}
`,
	})
	index, err := indexSource(t.Context(), snapshot, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Issues) != 0 || len(index.Routes) != 3 {
		t.Fatalf("route index: %+v", index)
	}
	want := []string{"GET /users/{id}", "HEAD /users/{id}", "POST /users"}
	got := make([]string, 0, len(index.Routes))
	for _, route := range index.Routes {
		got = append(got, route.Method+" "+route.Path)
		if route.RegistrationHash == "" || route.HandlerSymbol == "" {
			t.Fatalf("route missing source links: %+v", route)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routes = %v, want %v", got, want)
	}
}

func TestGoNetHTTPTracksRouteAndCrossPackageFunctionChanges(t *testing.T) {
	baseSources := map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"main.go": `package main
import (
	"net/http"
	"example.com/store/handlers"
)
func main() { http.HandleFunc("GET /price", handlers.Get); http.ListenAndServe(":8080", nil) }
`,
		"handlers/handler.go": `package handlers
import (
	"net/http"
	"example.com/store/tax"
)
func Get(http.ResponseWriter, *http.Request) { _ = tax.Rate() }
`,
		"tax/tax.go": `package tax
func Rate() int { return 10 }
`,
	}
	base := testSnapshot(baseSources)
	before, err := indexSource(t.Context(), base, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Routes) != 2 || len(before.Issues) != 0 {
		t.Fatalf("before index: %+v", before)
	}
	changed := map[string]string{}
	for name, body := range baseSources {
		changed[name] = body
	}
	changed["tax/tax.go"] = `package tax
func Rate() int { return 20 }
`
	potential, err := indexSource(t.Context(), testSnapshot(changed), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	report, err := compare(base, testSnapshot(changed), before, potential, "", "", "go-nethttp")
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 2 {
		t.Fatalf("helper change was not linked to its route: %+v", report)
	}
	if len(report.Routes[0].Evidence) != 2 || report.Routes[0].Evidence[0].File != "tax/tax.go" || report.Routes[0].Evidence[1].File != "tax/tax.go" {
		t.Fatalf("wrong route evidence: %+v", report.Routes[0].Evidence)
	}

	added := map[string]string{}
	for name, body := range baseSources {
		added[name] = body
	}
	added["main.go"] = `package main
import (
	"net/http"
	"example.com/store/handlers"
)
func main() {
	http.HandleFunc("GET /price", handlers.Get)
	http.HandleFunc("POST /price", handlers.Get)
	http.ListenAndServe(":8080", nil)
}
`
	after, err := indexSource(t.Context(), testSnapshot(added), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	addedReport, err := compare(base, testSnapshot(added), before, after, "", "", "go-nethttp")
	if err != nil {
		t.Fatal(err)
	}
	if addedReport.Status != "complete" || addedReport.Summary.Added != 1 {
		t.Fatalf("route addition was not detected: %+v", addedReport)
	}
}

func TestGoNetHTTPMarksDynamicRegistrationsIncomplete(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"main.go": `package main
import "net/http"
func main() { http.HandleFunc(routePattern(), getUser) }
func getUser(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 0 || len(index.Issues) != 1 || index.Issues[0].Code != "dynamic_go_route_pattern" {
		t.Fatalf("dynamic registration did not remain explicit: %+v", index)
	}
}

func TestGoRouteRegistrationChangeMarksOnlyChangedRegistration(t *testing.T) {
	baseSources := map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"main.go": `package main
import "net/http"
func main() { mux := http.NewServeMux(); mux.HandleFunc("GET /a", get); mux.HandleFunc("GET /b", get); _ = mux }
func get(http.ResponseWriter, *http.Request) {}
`,
	}
	base := testSnapshot(baseSources)
	before, err := indexSource(t.Context(), base, "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	changed := map[string]string{
		"go.mod": baseSources["go.mod"],
		"main.go": `package main
import "net/http"
func main() { mux := http.NewServeMux(); mux.HandleFunc("GET /a", get); mux.HandleFunc("GET /b", wrapped); _ = mux }
func get(http.ResponseWriter, *http.Request) {}
func wrapped(http.ResponseWriter, *http.Request) {}
`,
	}
	after, err := indexSource(t.Context(), testSnapshot(changed), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	report, err := compare(base, testSnapshot(changed), before, after, "", "", "go-nethttp")
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.SourceChanged != 2 || report.Summary.PotentiallyAffected != 0 || report.Summary.NoLinkedChanges != 2 {
		t.Fatalf("registration leaked to sibling route: %+v", report.Summary)
	}
}

func TestGoNetHTTPDetectsPackageMuxAcrossFiles(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"mux.go": `package main
import "net/http"
var publicMux = http.NewServeMux()
`,
		"routes.go": `package main
import "net/http"
func register() { publicMux.HandleFunc("GET /health", health) }
func health(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 2 || len(index.Issues) != 0 {
		t.Fatalf("package-level ServeMux was not followed: %+v", index)
	}
}

func TestGoNetHTTPRecognizesZeroValueMuxAndRejectsShadowedRouter(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"mux.go": `package main
import "net/http"
var publicMux http.ServeMux
func register() { publicMux.HandleFunc("GET /health", health) }
func registerCustom(publicMux *customRouter) { publicMux.HandleFunc("GET /not-net-http", health) }
func health(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 2 || len(index.Issues) != 0 || index.Routes[0].Path != "/health" || index.Routes[1].Path != "/health" {
		t.Fatalf("zero-value ServeMux or shadowed receiver was misclassified: %+v", index)
	}
}

func TestGoRoutePatternRejectsMalformedWildcards(t *testing.T) {
	for _, pattern := range []string{"GET /users/{", "GET /users/{id/name}", "GET /users/{id...}/details", "GET /users/{id}/{id}"} {
		if _, _, ok := goRoutePattern(pattern); ok {
			t.Errorf("goRoutePattern(%q) accepted malformed wildcard syntax", pattern)
		}
	}
}

func TestGoNetHTTPDoesNotTreatSameNamedMuxInAnotherFunctionAsRoute(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n",
		"main.go": `package main
import "net/http"
func unused() { mux := http.NewServeMux(); _ = mux }
func register(mux *customRouter) { mux.HandleFunc("GET /health", health) }
func health(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 0 {
		t.Fatalf("unrelated mux name was treated as the standard ServeMux: %+v", index.Routes)
	}
}

func TestGoNetHTTPDoesNotTreatShadowedHTTPImportAsDefaultMux(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"main.go": `package main
import "net/http"
func register(http *customRouter) { http.HandleFunc("GET /health", health) }
func health(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 0 {
		t.Fatalf("shadowed import alias was treated as DefaultServeMux: %+v", index.Routes)
	}
}

func TestGoNetHTTPMarksUnknownLegacyPatternSemantics(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.21\n",
		"main.go": `package main
import "net/http"
func main() { http.HandleFunc("GET /users/{id}", get) }
func get(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 2 || len(index.Issues) != 1 || index.Issues[0].Code != "go_route_semantics_unknown" {
		t.Fatalf("legacy Go pattern semantics were not disclosed: %+v", index)
	}
}

func TestGoNetHTTPMarksBuildConstrainedFilesIncomplete(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"main.go": `//go:build linux
package main
import "net/http"
func main() { http.HandleFunc("GET /", get) }
func get(http.ResponseWriter, *http.Request) {}
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 2 || len(index.Issues) != 1 || index.Issues[0].Code != "go_build_constraints_unknown" {
		t.Fatalf("build constraints were not disclosed: %+v", index)
	}
}

func TestGoNetHTTPMarksIndirectLocalCallsIncomplete(t *testing.T) {
	index, err := indexSource(t.Context(), testSnapshot(map[string]string{
		"go.mod": "module example.com/store\n\ngo 1.25\n",
		"main.go": `package main
import "net/http"
func main() { http.HandleFunc("GET /", get) }
func get(http.ResponseWriter, *http.Request) { handler := func() {}; handler() }
`,
	}), "go-nethttp", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Routes) != 2 || len(index.Issues) != 0 {
		t.Fatalf("unexpected route index: %+v", index)
	}
	handler := index.Symbols[index.Routes[0].HandlerSymbol]
	if len(handler.Issues) != 1 || handler.Issues[0].Code != "unresolved_go_call" {
		t.Fatalf("indirect local call uncertainty was lost: %+v", handler)
	}
}

func TestAnalyzeAutoDetectsGoAndMapsWorkingTreeChanges(t *testing.T) {
	dir := t.TempDir()
	gitFixture(t, dir, "init", "-q")
	writeFixture(t, dir, "go.mod", "module example.com/store\n\ngo 1.25\n")
	writeFixture(t, dir, "main.go", `package main
import (
	"net/http"
	"example.com/store/handlers"
)
func main() { http.HandleFunc("GET /price", handlers.Get) }
`)
	writeFixture(t, dir, "handlers/handler.go", `package handlers
import (
	"net/http"
	"example.com/store/tax"
)
func Get(http.ResponseWriter, *http.Request) { _ = tax.Rate() }
`)
	writeFixture(t, dir, "tax/tax.go", "package tax\nfunc Rate() int { return 10 }\n")
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-qm", "baseline")
	writeFixture(t, dir, "tax/tax.go", "package tax\nfunc Rate() int { return 20 }\n")
	report, err := Analyze(t.Context(), Options{Path: dir, Base: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Framework != "go-nethttp" || report.Status != "complete" || report.Summary.PotentiallyAffected != 2 {
		t.Fatalf("Go framework autodetection or impact mapping failed: %+v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseReport(encoded); err != nil {
		t.Fatalf("preview route report rejected Go source impact: %v", err)
	}
}
