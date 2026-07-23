// sdk-coverage is the CI drift gate for the public Go SDK
// (pkg/api.Client). It scans api/openapi.yaml for every documented
// v1 route and walks pkg/api/*.go for every method on *Client, then
// fails when:
//
//   - a route in the spec has no corresponding SDK method (spec → SDK drift).
//   - an SDK method claims a route that's not in the spec (over-spec — usually
//     a renaming mistake; reported as a soft warning so adding new SDK methods
//     ahead of spec work isn't a blocker).
//
// Pure read-only tool: prints a one-line PASS or a numbered list of
// missing methods with the route they belong to, exit 0/1. Designed
// for `make sdk-check` to mirror `make spec-check`'s recipe shape.
//
// Mapping table lives here (not magic) so adding a route is a one-
// line edit. When a route's natural verb clashes with the SDK's name
// (e.g. POST /v1/account/restore ↔ Client.RestoreAccount) the
// explicit table takes precedence over the auto-derivation.
//
// Routes deliberately not exposed in the SDK (anon endpoints, public
// status) are filtered via routeExclude — they're either shape-only
// for documentation or they require authentication schemes the SDK
// doesn't model (HMAC webhooks, session cookies). Future SDK calls
// that add a typed wrapper to those routes are encouraged.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	specRelPath    = "api/openapi.yaml"
	sdkRelPath     = "pkg/api"
	sdkPackageName = "api"
)

// routeExclude lists spec routes that have no SDK method by design.
// Mirror logic in cmd/apid/spec_compliance_test.go::routeExclude;
// keep both in sync.
var routeExclude = map[string]bool{
	"GET /v1/account/dpa":        true, // public markdown (no Bearer; SDK consumers don't render HTML)
	"POST /v1/webhooks/stripe":   true, // HMAC-signed webhook; outside the Bearer-auth surface
	"GET /v1/openapi.yaml":       true, // metadata
	"GET /v1/openapi.json":       true, // metadata
	"POST /v1/cli-auth/code":     true, // anonymous device-code (CLI uses wrapper)
	"POST /v1/cli-auth/exchange": true,
	"GET /status/slo.json":       true,
	"GET /status":                true,
}

// sdkMethodExclude lists methods on *Client that aren't a 1:1 wire
// of any single spec route. Helpers (ListDeploymentsAll) and
// response-shape getters (HTTPClient/BaseURL/Token) belong here so
// the gate doesn't false-positive on them.
var sdkMethodExclude = map[string]bool{
	"HTTPClient":          true,
	"BaseURL":             true,
	"Token":               true,
	"ListDeploymentsAll":  true, // cursor walker; not a route
	"DeployMultipart":     true, // open-ended reader-based upload; CLI's DeployTarball is the wired route
	"MintCliAuthCode":     true, // anonymous device-code mint; route excluded above
	"ExchangeCliAuthCode": true, // anonymous device-code poll; route excluded above
	"GetStatusSLO":        true, // public status; route excluded above
}

// methodRouteMap pins the routes whose natural SDK verb doesn't
// match the standard <Verb><Resource> derivation. Adding a new
// route? Add a row here ONLY if the auto-derivation picks the
// wrong method name; otherwise leave it auto-derived.
//
// Key = "<METHOD> <path>"; value = SDK method name.
var methodRouteMap = map[string]string{
	"DELETE /v1/keys/{id}":                 "DeleteKey",
	"DELETE /v1/domains/{domain}":          "DeleteDomain",
	"DELETE /v1/crons/{id}":                "DeleteCron",
	"DELETE /v1/apps/{slug}":               "DeleteApp",
	"DELETE /v1/apps/{slug}/secrets/{key}": "UnsetSecret",
	"PUT /v1/apps/{slug}/secrets/{key}":    "SetSecret",
	"PATCH /v1/apps/{slug}":                "UpdateApp",
	"POST /v1/apps/{slug}/rename":          "RenameApp",
	"GET /v1/apps/{slug}":                  "GetApp",
	"GET /v1/apps/{slug}/instances":        "ListInstances",
	"POST /v1/apps/{slug}/park":            "Park",
	"POST /v1/apps/{slug}/wake":            "Wake",
	"POST /v1/apps/{slug}/rollback":        "Rollback",
	"POST /v1/apps/{slug}/deployments":     "Deploy",
	"GET /v1/account/export":               "ExportAccount",
	"DELETE /v1/account":                   "DeleteAccount",
	"PATCH /v1/account/plan":               "ChangePlan",
	"GET /v1/account":                      "Whoami",
	"POST /v1/account/restore":             "RestoreAccount",
	"GET /v1/apps/{slug}/logs":             "StreamAppLogs",
	"GET /v1/deployments/{id}/logs":        "StreamDeploymentLogs",
	"GET /v1/deployments/{id}":             "GetDeployment",
	"GET /v1/deployments":                  "ListDeployments",
	"GET /v1/apps":                         "ListApps",
	"POST /v1/apps":                        "CreateApp",
	"GET /status/slo.json":                 "GetStatusSLO",
	"PATCH /v1/crons/{id}":                 "UpdateCron",
	"POST /v1/crons":                       "CreateCron",
	"GET /v1/crons":                        "ListCrons",
	"GET /v1/usage/summary":                "UsageSummary",
	"GET /v1/usage":                        "GetUsage",
	"GET /v1/apps/{slug}/secrets":          "ListSecrets",
	"GET /v1/domains":                      "ListDomains",
	"POST /v1/domains":                     "CreateDomain",
	"GET /v1/keys":                         "ListKeys",
	"POST /v1/keys":                        "CreateKey",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "sdk-coverage: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	spec, err := loadSpec(filepath.Join(root, specRelPath))
	if err != nil {
		return err
	}
	methods, err := loadClientMethods(filepath.Join(root, sdkRelPath))
	if err != nil {
		return err
	}
	report := analyze(spec, methods)
	report.print()
	if !report.ok() {
		os.Exit(1)
	}
	return nil
}

// report collects the drift findings.
type reportT struct {
	missing []string // spec routes without an SDK method
	warn    []string // SDK methods without a spec route (soft)
}

func (r reportT) ok() bool { return len(r.missing) == 0 }
func (r *reportT) print() {
	if len(r.missing) == 0 && len(r.warn) == 0 {
		fmt.Println("sdk-coverage: PASS — every spec route has a typed SDK method")
		return
	}
	if len(r.missing) > 0 {
		fmt.Println("sdk-coverage: FAIL — these spec routes have no SDK method:")
		for _, m := range r.missing {
			fmt.Printf("  - %s\n", m)
		}
	}
	if len(r.warn) > 0 {
		fmt.Println("sdk-coverage: warn — these SDK methods have no matching spec route (usually helpers like List*All):")
		for _, w := range r.warn {
			fmt.Printf("  - %s\n", w)
		}
	}
}

func analyze(spec map[string]map[string]any, methods map[string]bool) reportT {
	r := reportT{}
	methodUsage := map[string]bool{} // SDK methods touched by a route
	for path, ops := range spec {
		for method := range ops {
			key := strings.ToUpper(method) + " " + path
			if routeExclude[key] {
				continue
			}
			sdkName := methodRouteMap[key]
			if sdkName == "" {
				sdkName = deriveMethodName(method, path)
			}
			if !methods[sdkName] {
				r.missing = append(r.missing, fmt.Sprintf("%s → SDK method %q", key, sdkName))
			}
			methodUsage[sdkName] = true
		}
	}
	for m := range methods {
		if sdkMethodExclude[m] || methodUsage[m] {
			continue
		}
		r.warn = append(r.warn, m+" (no spec route)")
	}
	sort.Strings(r.missing)
	sort.Strings(r.warn)
	return r
}

// deriveMethodName produces a best-effort SDK method name from a
// path + method. Falls back to "<Method>_<path-sanitized>" so the
// failure message is descriptive even when the auto-derivation
// misses the mark. The map in methodRouteMap overrides this when
// the natural verb differs (e.g. POST …/deployments → Deploy).
//
// Today every spec route is in methodRouteMap, so this function is
// unreachable at runtime. It exists only as a fallback so a future
// route that ships without a map entry produces a descriptive error
// (the developer sees "POST /v1/apps/{slug}/x → SDK method PostAppsSlugX"
// instead of "unknown"). Not authoritative; methodRouteMap is.
func deriveMethodName(method, path string) string {
	method = strings.Title(strings.ToLower(method))
	// Strip /v1/ prefix and {} placeholders; title-case each segment.
	cleaned := strings.TrimPrefix(path, "/v1/")
	cleaned = strings.ReplaceAll(cleaned, "{", "")
	cleaned = strings.ReplaceAll(cleaned, "}", "")
	segments := strings.Split(cleaned, "/")
	for i, s := range segments {
		segments[i] = strings.Title(s)
	}
	res := strings.Join(segments, "")
	return method + res
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
	return "", fmt.Errorf("go.mod not found (walked up 16 levels)")
}

// loadSpec reads api/openapi.yaml and returns path -> method -> true.
// Same string-keyed view as cmd/apid/spec_compliance_test.go.
func loadSpec(path string) (map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	root := yaml.Node{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("unexpected spec yaml shape")
	}
	spec := root.Content[0]
	// OpenAPI is a flat mapping; walk top-level Content (alternating
	// key/value nodes) rather than depending on a ContentMap helper.
	var paths *yaml.Node
	for i := 0; i+1 < len(spec.Content); i += 2 {
		if spec.Content[i].Value == "paths" {
			paths = spec.Content[i+1]
			break
		}
	}
	if paths == nil {
		return out, nil
	}
	for i := 0; i+1 < len(paths.Content); i += 2 {
		pn := paths.Content[i]   // path key
		on := paths.Content[i+1] // operation mapping
		if on.Kind != yaml.MappingNode {
			continue
		}
		methods := map[string]any{}
		for j := 0; j+1 < len(on.Content); j += 2 {
			m := on.Content[j].Value
			switch m {
			case "get", "post", "put", "patch", "delete":
				methods[m] = true
			}
		}
		if len(methods) > 0 {
			out[pn.Value] = methods
		}
	}
	return out, nil
}

// loadClientMethods walks pkg/api/*.go and returns the set of method
// names declared on *Client (the public SDK surface).
func loadClientMethods(dir string) (map[string]bool, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(os.FileInfo) bool { return true }, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	pkg, ok := pkgs[sdkPackageName]
	if !ok {
		return nil, fmt.Errorf("package %q not found in %s (found %d packages)", sdkPackageName, dir, len(pkgs))
	}
	out := map[string]bool{}
	for _, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Name.IsExported() == false {
				return true
			}
			// Filter to methods on *Client only.
			if !isClientRecv(fd.Recv) {
				return true
			}
			out[fd.Name.Name] = true
			return true
		})
	}
	return out, nil
}

func isClientRecv(recv *ast.FieldList) bool {
	if recv == nil || len(recv.List) == 0 {
		return false
	}
	t, ok := recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := t.X.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == "Client"
}
