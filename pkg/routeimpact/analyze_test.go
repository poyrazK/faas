package routeimpact

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func testSnapshot(sources map[string]string) sourceSnapshot {
	snapshot := sourceSnapshot{meta: Snapshot{Revision: "fixture"}, files: map[string]sourceFile{}}
	for path, body := range sources {
		snapshot.files[path] = sourceFile{path: path, hash: contentHash([]byte(body)), mode: "100644", body: []byte(body)}
	}
	fingerprint(&snapshot)
	return snapshot
}

func indexFixture(t *testing.T, sources map[string]string, entrypoint string) sourceIndex {
	t.Helper()
	requirePython(t)
	index, err := indexSource(context.Background(), testSnapshot(sources), entrypoint)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 3 is required for the FastAPI AST integration tests")
	}
}

func TestFastAPINestedRoutersAndImportedHandlers(t *testing.T) {
	index := indexFixture(t, map[string]string{
		"api/__init__.py":         "",
		"api/main.py":             "import fastapi as f\nfrom .routers import items\napp = f.FastAPI()\napp.include_router(items.router, prefix='/api')\n",
		"api/routers/__init__.py": "",
		"api/routers/items.py":    "from fastapi import APIRouter as Router\nfrom .details import child\nrouter = Router(prefix='/items')\n@router.api_route('/{id}', methods=['GET', 'PATCH'])\nasync def item(id: str):\n    return id\nrouter.include_router(child, prefix='/nested')\n",
		"api/routers/details.py":  "from fastapi import APIRouter\nfrom ..handlers import detail\nchild = APIRouter(prefix='/detail')\nchild.add_api_route('/{id}', endpoint=detail, methods=['POST'])\n",
		"api/handlers.py":         "async def detail(id: str):\n    return id\n",
	}, "api.main:app")
	if len(index.Issues) != 0 || len(index.Routes) != 3 {
		t.Fatalf("routes=%+v issues=%+v", index.Routes, index.Issues)
	}
	if index.Entrypoint != "api.main:app" {
		t.Fatal(index.Entrypoint)
	}
	route := index.Routes[0]
	if route.Method != "POST" || route.Path != "/api/items/nested/detail/{id}" || route.Source.File != "api/handlers.py" || route.Source.Line != 1 || route.Registration.File != "api/routers/details.py" {
		t.Fatalf("imported handler: %+v", route)
	}
	if !reflect.DeepEqual(index.Dependencies["api/routers/items.py"], []string{"api/__init__.py", "api/routers/__init__.py", "api/routers/details.py"}) {
		t.Fatalf("relative imports: %+v", index.Dependencies)
	}
}

func TestFastAPIUnknownsAreExplicit(t *testing.T) {
	cases := []struct {
		name   string
		source string
		code   string
	}{
		{"dynamic path", "app=FastAPI()\n@app.get(PATH)\ndef x(): pass\n", "dynamic_path"},
		{"dynamic prefix", "app=FastAPI()\nr=APIRouter(prefix=PREFIX)\napp.include_router(r)\n", "dynamic_prefix"},
		{"dynamic methods", "app=FastAPI()\n@app.api_route('/x', methods=METHODS)\ndef x(): pass\n", "dynamic_methods"},
		{"conditional registration", "app=FastAPI()\nif True:\n    @app.get('/x')\n    def x(): pass\n", "dynamic_registration"},
		{"factory", "def create():\n    return FastAPI()\napp=create()\n", "dynamic_factory"},
		{"missing router", "app=FastAPI()\napp.include_router(build_router())\n", "dynamic_router"},
		{"mount", "app=FastAPI()\napp.mount('/sub', other)\n", "unsupported_registration"},
		{"star import", "from local import *\napp=FastAPI()\n", "star_import"},
		{"expanded arguments", "app=FastAPI()\n@app.get('/x', **opts)\ndef x(): pass\n", "dynamic_arguments"},
		{"constructor routes", "app=FastAPI(routes=routes)\n", "unsupported_registration"},
		{"constructor expansion", "app=FastAPI(**options)\n", "dynamic_arguments"},
		{"opaque helper", "app=FastAPI()\nregister(app)\n", "opaque_object_use"},
		{"rebound object", "app=FastAPI()\napp=other\n", "rebound_object"},
		{"attribute mutation", "app=FastAPI()\napp.routes=[]\n", "object_mutation"},
		{"nested mutation", "app=FastAPI()\napp.router.routes=[]\n", "object_mutation"},
		{"dependency override", "app=FastAPI()\napp.dependency_overrides[auth]=override\n", "object_mutation"},
		{"collection mutation", "app=FastAPI()\napp.router.routes.clear()\n", "object_mutation"},
		{"starlette decorator", "app=FastAPI()\n@app.route('/x')\ndef x(): pass\n", "unsupported_registration"},
		{"duplicate routes", "app=FastAPI()\n@app.get('/x')\ndef one(): pass\n@app.get('/x')\ndef two(): pass\n", "duplicate_route"},
		{"registration order", "app=FastAPI()\nr=APIRouter()\napp.include_router(r)\n@r.get('/late')\ndef late(): pass\n", "registration_order"},
		{"cycle", "app=FastAPI()\nr=APIRouter()\nr.include_router(r)\napp.include_router(r)\n", "router_cycle"},
		{"dynamic import", "import importlib as loader\napp=FastAPI()\nloader.import_module('runtime_routes')\n", "dynamic_import"},
		{"parse error", "app=FastAPI(\nSECRET_LITERAL_DO_NOT_ECHO", "parse_failed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			index := indexFixture(t, map[string]string{"main.py": "from fastapi import FastAPI, APIRouter\n" + test.source}, "")
			found := false
			for _, issue := range index.Issues {
				if issue.Code == test.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", test.code, index)
			}
			body, _ := json.Marshal(index)
			if strings.Contains(string(body), "SECRET_LITERAL_DO_NOT_ECHO") {
				t.Fatal("parser diagnostics exposed source")
			}
		})
	}
}

func TestFastAPIExplicitEntrypointAndReExport(t *testing.T) {
	sources := map[string]string{
		"main.py":      "from fastapi import FastAPI\nfrom export import router\napp = FastAPI()\nother = FastAPI()\napp.include_router(router)\n",
		"export.py":    "from endpoints import router\n",
		"endpoints.py": "from fastapi import APIRouter\nrouter=APIRouter()\n@router.get('/x')\ndef x(): pass\n",
	}
	ambiguous := indexFixture(t, sources, "")
	if len(ambiguous.Routes) != 0 || len(ambiguous.Issues) == 0 {
		t.Fatalf("ambiguous apps accepted: %+v", ambiguous)
	}
	selected := indexFixture(t, sources, "main:app")
	if len(selected.Routes) != 1 || len(selected.Issues) != 0 {
		t.Fatalf("selected app: %+v", selected)
	}
}

func impactSources() map[string]string {
	return map[string]string{
		"main.py":       "from fastapi import FastAPI\nimport checkout\nimport products\nfrom middleware import Auth\napp=FastAPI()\napp.add_middleware(Auth)\napp.include_router(checkout.router)\napp.include_router(products.router)\n",
		"checkout.py":   "from fastapi import APIRouter\nimport pricing\nrouter=APIRouter()\n@router.post('/checkout')\ndef checkout():\n    return pricing.price()\n",
		"products.py":   "from fastapi import APIRouter\nrouter=APIRouter()\n@router.get('/products')\ndef products():\n    return []\n",
		"pricing.py":    "import tax\ndef price():\n    return tax.rate\n",
		"tax.py":        "rate=1\n",
		"middleware.py": "class Auth:\n    pass\n",
	}
}

func compareFixture(t *testing.T, base, candidate sourceSnapshot, before, after sourceIndex) Report {
	t.Helper()
	report, err := compare(base, candidate, before, after, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestImpactTransitiveDependenciesDoNotLinkSiblingRouters(t *testing.T) {
	beforeSources := impactSources()
	before := testSnapshot(beforeSources)
	beforeIndex := indexFixture(t, beforeSources, "")
	beforeSources["tax.py"] = "rate=2\n"
	after := testSnapshot(beforeSources)
	afterIndex := indexFixture(t, beforeSources, "")
	report := compareFixture(t, before, after, beforeIndex, afterIndex)
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 || report.Summary.NoLinkedChanges != 1 {
		t.Fatalf("sibling route polluted: %+v", report)
	}
	evidence := report.Routes[0].Evidence
	if len(evidence) != 2 || !reflect.DeepEqual(evidence[0].Via, []string{"checkout.py", "pricing.py", "tax.py"}) {
		t.Fatalf("transitive evidence: %+v", evidence)
	}
	beforeSources["middleware.py"] = "class Auth:\n    version=2\n"
	report = compareFixture(t, before, testSnapshot(beforeSources), beforeIndex, indexFixture(t, beforeSources, ""))
	if report.Summary.PotentiallyAffected != 2 {
		t.Fatalf("shared middleware did not affect both routes: %+v", report)
	}
}

func TestImpactAddedRemovedSourceChangesAndUnknownAbsence(t *testing.T) {
	beforeSources := map[string]string{"main.py": "from fastapi import FastAPI\napp=FastAPI()\n@app.get('/old')\ndef old(): pass\n@app.post('/same')\ndef same(): pass\n"}
	afterSources := map[string]string{"main.py": "from fastapi import FastAPI\napp=FastAPI()\n@app.get('/new')\ndef added(): pass\n@app.post('/same')\ndef same(): return 2\n"}
	report := compareFixture(t, testSnapshot(beforeSources), testSnapshot(afterSources), indexFixture(t, beforeSources, ""), indexFixture(t, afterSources, ""))
	if report.Summary.Added != 1 || report.Summary.Removed != 1 || report.Summary.SourceChanged != 1 {
		t.Fatalf("classification: %+v", report)
	}
	afterSources["main.py"] = "from fastapi import FastAPI\napp=FastAPI()\napp.include_router(build_router())\n"
	report = compareFixture(t, testSnapshot(beforeSources), testSnapshot(afterSources), indexFixture(t, beforeSources, ""), indexFixture(t, afterSources, ""))
	if report.Summary.Removed != 0 || report.Summary.Unknown != 2 || report.Status != "incomplete" {
		t.Fatalf("incomplete index claimed removals: %+v", report)
	}
}

func TestStaticAnalysisNeverExecutesApplicationOrShadowStdlib(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	sources := map[string]string{
		"main.py": "from fastapi import FastAPI\nraise RuntimeError('application must not run')\napp=FastAPI()\n@app.get('/safe')\ndef safe(): return 'private-body-should-not-appear'\n",
		"ast.py":  "open(" + pythonQuote(marker) + ", 'w').write('executed')\n",
	}
	index := indexFixture(t, sources, "main:app")
	if len(index.Routes) != 1 {
		t.Fatalf("index: %+v", index)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("customer source executed")
	}
	body, _ := json.Marshal(index)
	if strings.Contains(string(body), "private-body-should-not-appear") {
		t.Fatal("index contains function source")
	}
}

func pythonQuote(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}

func gitFixture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Route Impact Test", "GIT_AUTHOR_EMAIL=route-impact@example.invalid",
		"GIT_COMMITTER_NAME=Route Impact Test", "GIT_COMMITTER_EMAIL=route-impact@example.invalid")
	body, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, body)
	}
	return strings.TrimSpace(string(body))
}

func writeFixture(t *testing.T, dir, path, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeGitCommitsWorkingTreeAndScopedPaths(t *testing.T) {
	requirePython(t)
	dir := t.TempDir()
	gitFixture(t, dir, "init", "-q")
	gitFixture(t, dir, "remote", "add", "origin", "https://user:secret-origin@github.com/Team/Service.git")
	sources := impactSources()
	for path, body := range sources {
		writeFixture(t, dir, "service/"+path, body)
	}
	writeFixture(t, dir, ".gitignore", "service/ignored.py\n")
	writeFixture(t, dir, "service/settings.yaml", "secret-value-never-read\n")
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-qm", "baseline")
	base := gitFixture(t, dir, "rev-parse", "HEAD")
	writeFixture(t, dir, "service/tax.py", "rate=2\n")
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-qm", "update tax")
	writeFixture(t, dir, "service/products.py", sources["products.py"]+"\n@router.get('/new')\ndef added(): pass\n")
	writeFixture(t, dir, "service/untracked.py", "value=1\n")
	writeFixture(t, dir, "service/ignored.py", "broken ignored source !!\n")
	options := Options{Path: filepath.Join(dir, "service"), Base: base, Head: "HEAD", App: "my-api"}
	report, err := Analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "complete" || report.SourceRoot != "service" || report.Summary.PotentiallyAffected != 1 || report.Summary.Added != 0 || len(report.ChangedFiles) != 1 {
		t.Fatalf("commit report: %+v", report)
	}
	body, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if _, parseErr := ParseReport(body); parseErr != nil {
		t.Fatal(parseErr)
	}
	if report.Repository != "github.com/team/service" || strings.Contains(string(body), "secret-origin") {
		t.Fatal("origin identity was missing or leaked credentials")
	}
	options.Head = ""
	report, err = Analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "complete" || report.Summary.Added != 1 || report.Summary.SourceChanged != 0 || report.Summary.PotentiallyAffected != 2 || len(report.ChangedFiles) != 3 {
		t.Fatalf("working tree report: %+v", report)
	}
	first, _ := json.Marshal(report)
	second, err := Analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(second)
	if string(first) != string(again) {
		t.Fatal("same source snapshot produced different report")
	}
	writeFixture(t, dir, "service/settings.yaml", "new-secret-value-never-read\n")
	report, err = Analyze(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(report)
	if _, parseErr := ParseReport(body); parseErr != nil {
		t.Fatal(parseErr)
	}
	if report.Status != "incomplete" || strings.Contains(string(body), "secret-value-never-read") {
		t.Fatal("non-Python changes were hidden or exposed")
	}
}

func TestAnalyzeRejectsUnsafeRefsAndDoesNotFollowSourceLinks(t *testing.T) {
	requirePython(t)
	dir := t.TempDir()
	gitFixture(t, dir, "init", "-q")
	writeFixture(t, dir, "main.py", "from fastapi import FastAPI\napp=FastAPI()\n")
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-qm", "baseline")
	for _, ref := range []string{"--help", "missing-ref", "HEAD; touch executed"} {
		if _, err := Analyze(context.Background(), Options{Path: dir, Base: ref}); err == nil {
			t.Fatalf("accepted ref %q", ref)
		}
	}
	target := filepath.Join(t.TempDir(), "private.py")
	if err := os.WriteFile(target, []byte("SECRET_SOURCE_CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "linked.py")); err != nil {
		t.Fatal(err)
	}
	report, err := Analyze(context.Background(), Options{Path: dir, Base: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(report)
	if report.Status != "incomplete" || strings.Contains(string(body), "SECRET_SOURCE_CONTENT") {
		t.Fatal("source symlink was followed or reported as complete")
	}
}

func TestRouteImpactBoundsAndMissingInterpreter(t *testing.T) {
	buffer := &limitedBuffer{max: 2}
	if _, err := buffer.Write([]byte("abc")); err == nil || buffer.Len() != 0 {
		t.Fatal("subprocess output silently truncated")
	}
	dir := t.TempDir()
	gitFixture(t, dir, "init", "-q")
	writeFixture(t, dir, "main.py", strings.Repeat("x", api.RouteImpactFileMaxBytes+1))
	gitFixture(t, dir, "add", ".")
	gitFixture(t, dir, "commit", "-qm", "oversized")
	if _, err := Analyze(context.Background(), Options{Path: dir, Base: "HEAD", Head: "HEAD"}); err == nil {
		t.Fatal("oversized source accepted")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := indexSource(context.Background(), testSnapshot(map[string]string{"main.py": "pass\n"}), ""); err == nil || !strings.Contains(err.Error(), "requires python3") {
		t.Fatalf("missing interpreter: %v", err)
	}
}

func TestImportGraphDepthAndEvidenceBoundsFailWithoutTruncation(t *testing.T) {
	dependencies := map[string][]string{}
	for i := 0; i < api.RouteImpactMaxGraphDepth; i++ {
		dependencies[strings.Repeat("x", i+1)+".py"] = []string{strings.Repeat("x", i+2) + ".py"}
	}
	route := Route{Method: "GET", Path: "/", Source: Location{File: "x.py"}, ContextFiles: []string{}}
	if _, err := linkedEvidence(route, sourceIndex{Dependencies: dependencies}, nil, "base"); err == nil {
		t.Fatal("deep import chain was silently truncated")
	}
	route.Source.File = "large.py"
	dependencies = map[string][]string{"large.py": {}}
	files := map[string]sourceFile{"large.py": {path: "large.py", hash: "old"}}
	newFiles := map[string]sourceFile{"large.py": {path: "large.py", hash: "new"}}
	// Several routes importing many changed files can produce a report larger
	// than either source index; the comparison has its own independent bound.
	for i := 0; i < api.RouteImpactMaxPythonFiles; i++ {
		path := strings.Repeat("x", i+1) + ".py"
		dependencies["large.py"] = append(dependencies["large.py"], path)
		files[path] = sourceFile{path: path, hash: "old"}
		newFiles[path] = sourceFile{path: path, hash: "new"}
	}
	var routes []Route
	for _, path := range []string{"/a", "/b", "/c", "/d"} {
		row := route
		row.Path = path
		routes = append(routes, row)
	}
	index := sourceIndex{Routes: routes, Dependencies: dependencies}
	if _, err := compare(sourceSnapshot{files: files}, sourceSnapshot{files: newFiles}, index, index, "", ""); err == nil {
		t.Fatal("oversized evidence was silently truncated")
	}
}

func TestFastAPILocalDependencyAndImportedEndpointDependency(t *testing.T) {
	sources := map[string]string{
		"main.py":       "from fastapi import FastAPI, Depends\nfrom endpoints import handle\nfrom validation import check\nimport rules\ndef auth(): return rules.allowed\napp=FastAPI(dependencies=[Depends(auth)])\napp.add_api_route('/x', handle, dependencies=[Depends(check)])\n",
		"endpoints.py":  "def handle(): return 1\n",
		"validation.py": "import limits\ndef check(): return limits.allowed\n",
		"limits.py":     "allowed=True\n",
		"rules.py":      "allowed=True\n",
	}
	base := testSnapshot(sources)
	before := indexFixture(t, sources, "")
	sources["limits.py"], sources["rules.py"] = "allowed=False\n", "allowed=False\n"
	report := compareFixture(t, base, testSnapshot(sources), before, indexFixture(t, sources, ""))
	if report.Status != "complete" || report.Summary.PotentiallyAffected != 1 {
		t.Fatalf("dependencies: %+v", report)
	}
	seen := map[string]bool{}
	for _, evidence := range report.Routes[0].Evidence {
		seen[evidence.File] = true
	}
	if !seen["limits.py"] || !seen["rules.py"] {
		t.Fatalf("global or route dependency missing: %+v", report.Routes[0].Evidence)
	}
}

func TestFastAPISideEffectRegistrationsRequireImportedModules(t *testing.T) {
	main := "from fastapi import FastAPI\napp=FastAPI()\n@app.get('/real')\ndef real(): pass\n"
	extra := "from main import app\n@app.get('/extra')\ndef extra(): pass\n"
	for _, test := range []struct {
		name  string
		setup string
		count int
		issue string
	}{
		{"unimported", "", 1, ""},
		{"imported", "import extra\n", 2, ""},
		{"conditional", "if ENABLED:\n    import extra\n", 1, "conditional_route_import"},
		{"function import", "def later():\n    import extra\n", 1, "conditional_route_import"},
	} {
		t.Run(test.name, func(t *testing.T) {
			index := indexFixture(t, map[string]string{"main.py": main + test.setup, "extra.py": extra}, "main:app")
			if len(index.Routes) != test.count {
				t.Fatalf("unloaded source invented routes: %+v", index)
			}
			if test.issue == "" && len(index.Issues) != 0 {
				t.Fatal(index.Issues)
			}
			if test.issue != "" {
				found := false
				for _, issue := range index.Issues {
					found = found || issue.Code == test.issue
				}
				if !found {
					t.Fatalf("conditional registration was hidden: %+v", index)
				}
			}
		})
	}
}

func TestPythonSourceEncodingAndLeadingWhitespacePreserved(t *testing.T) {
	source := "\n# coding: latin-1\nfrom fastapi import FastAPI\napp=FastAPI()\n@app.get('/caf\xe9')\ndef coffee(): pass\n"
	index := indexFixture(t, map[string]string{"main.py": source}, "")
	if len(index.Issues) != 0 || len(index.Routes) != 1 || index.Routes[0].Path != "/café" {
		t.Fatalf("source encoding changed: %+v", index)
	}
}
