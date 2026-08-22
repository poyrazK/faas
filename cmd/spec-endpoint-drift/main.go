// Command spec-endpoint-drift diffs the current api/openapi.yaml against
// a base-spec snapshot (typically the PR base via `git show`), then
// fails on removal/rename of any (path, method) that's exposed in one
// of the customer-facing SDKs (sdk/node, sdk/python, pkg/api.Client).
//
// Internal-only paths can move freely: they're absent from the SDK
// exposure set, so the diff is a no-op for them. Optional allowlist
// (api/endpoint_allowlist.yaml) carves out specific entries for
// intentional customer-visible renames (e.g. deprecations).
//
// Pure read-only tool. Exit 0 if clean; exit 1 with a numbered list of
// removed/renamed endpoints on drift. Designed to be invoked by
// `make spec-endpoint-drift` from CI's spec-check job.
//
// See plan file /Users/poyrazk/.claude/plans/floating-wishing-marble.md
// (commit 1) for the full design rationale.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// endpoint is (HTTP method uppercase, OpenAPI path with {param} tokens).
type endpoint struct {
	method string
	path   string
}

func (e endpoint) String() string { return e.method + " " + e.path }

// opMap is the parsed OpenAPI spec's exposed operation set, keyed by
// endpoint with operationId as the value. operationId is the rename
// indicator — if base says GET /v1/foo has operationId listFoo and the
// current spec says GET /v1/foo has operationId listFooV2, the SDK
// would silently break.
type opMap map[endpoint]string

func main() {
	var (
		baseSpec    = flag.String("base-spec", "", "path to base spec (e.g. /tmp/base-spec.yaml from `git show origin/<base>:api/openapi.yaml`)")
		currentSpec = flag.String("current-spec", "api/openapi.yaml", "path to current spec")
		nodeSDK     = flag.String("node-sdk", "sdk/node/src/generated/services", "dir holding generated *.ts services")
		pythonSDK   = flag.String("python-sdk", "sdk/python/faas_sdk/api", "dir holding generated api modules")
		goSDK       = flag.String("go-sdk", "pkg/api/client.go", "path to hand-written Go SDK client")
		allowlist   = flag.String("allowlist", "api/endpoint_allowlist.yaml", "YAML file listing (path, method) pairs exempt from this check")
	)
	flag.Parse()

	if *baseSpec == "" {
		fail("missing required --base-spec")
	}
	if _, err := os.Stat(*baseSpec); err != nil {
		fail("base-spec unreadable: %v", err)
	}
	if _, err := os.Stat(*currentSpec); err != nil {
		fail("current-spec unreadable: %v", err)
	}

	base, err := parseSpec(*baseSpec)
	if err != nil {
		fail("parse base-spec: %v", err)
	}
	current, err := parseSpec(*currentSpec)
	if err != nil {
		fail("parse current-spec: %v", err)
	}

	exposed, err := collectSDKExposure(*nodeSDK, *pythonSDK, *goSDK)
	if err != nil {
		fail("collect SDK exposure: %v", err)
	}
	tally("SDK exposure", len(exposed))

	allow, err := loadAllowlist(*allowlist)
	if err != nil {
		fail("load allowlist: %v", err)
	}
	if len(allow) > 0 {
		fmt.Fprintf(os.Stderr, "allowlist: %d entries (see %s)\n", len(allow), *allowlist)
	}

	var removed, renamed []endpoint
	for ep := range base {
		if _, isAllow := allow[ep]; isAllow {
			continue
		}
		if _, stillExposed := exposed[ep]; !stillExposed {
			// Not in any SDK; only fails if it WAS exposed.
			// (Internal-only endpoints can disappear freely.)
			continue
		}
		newOp, inCurrent := current[ep]
		if !inCurrent {
			removed = append(removed, ep)
			continue
		}
		if newOp != base[ep] {
			renamed = append(renamed, ep)
		}
	}

	var added []endpoint
	for ep := range current {
		if _, isAllow := allow[ep]; isAllow {
			continue
		}
		if _, wasExposed := exposed[ep]; !wasExposed {
			continue
		}
		if _, inBase := base[ep]; !inBase {
			added = append(added, ep)
		}
	}

	sort.Slice(removed, func(i, j int) bool { return endpointLT(removed[i], removed[j]) })
	sort.Slice(renamed, func(i, j int) bool { return endpointLT(renamed[i], renamed[j]) })
	sort.Slice(added, func(i, j int) bool { return endpointLT(added[i], added[j]) })

	if len(removed) > 0 || len(renamed) > 0 {
		fmt.Fprintf(os.Stderr, "spec-endpoint-drift: FAIL (%d removed, %d renamed)\n", len(removed), len(renamed))
		for _, ep := range removed {
			fmt.Fprintf(os.Stderr, "  REMOVED: %s (was operationId=%s)\n", ep, base[ep])
		}
		for _, ep := range renamed {
			fmt.Fprintf(os.Stderr, "  RENAMED: %s (operationId %s -> %s)\n", ep, base[ep], current[ep])
		}
		fmt.Fprintf(os.Stderr, "\nfix: revert the spec change, or add an entry to %s with a justification.\n", *allowlist)
		os.Exit(1)
	}

	fmt.Printf("spec-endpoint-drift: OK (base=%d, current=%d, exposed=%d, removed=0, renamed=0, added=%d)\n",
		len(base), len(current), len(exposed), len(added))
	if len(added) > 0 {
		for _, ep := range added {
			fmt.Printf("  ADDED (informational): %s (operationId=%s)\n", ep, current[ep])
		}
	}
}

// httpMethods is the canonical OpenAPI HTTP method set; sibling keys
// at a path level (parameters, summary, $ref) are NOT methods and must
// be ignored. Mirrors the explicit enumeration at
// cmd/apid/spec_compliance_test.go:558.
var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true,
	"patch": true, "delete": true,
	"head": true, "options": true,
}

// parseSpec reads an OpenAPI YAML file and returns its (path, method)
// → operationId map. Methods are uppercased to match SDK exposure
// (HTTP wire is case-sensitive; we follow the wire). Path strings
// preserve {param} tokens because that's the identity surface the SDK
// keys off (e.g. /v1/apps/{slug} is distinct from /v1/apps/{id}).
func parseSpec(path string) (opMap, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("yaml unmarshal: %w", err)
	}
	out := opMap{}
	paths, ok := raw["paths"].(map[string]any)
	if !ok {
		return out, nil
	}
	for p, methods := range paths {
		mop, ok := methods.(map[string]any)
		if !ok {
			continue
		}
		for m, op := range mop {
			if !httpMethods[m] {
				continue
			}
			mm, ok := op.(map[string]any)
			if !ok {
				continue
			}
			opID, _ := mm["operationId"].(string)
			out[endpoint{method: strings.ToUpper(m), path: p}] = opID
		}
	}
	return out, nil
}

// collectSDKExposure walks the three customer-facing SDKs and returns
// the union of (path, method) pairs they each export. A path that is
// in the spec but absent from all three SDKs is internal-only and
// excluded from the failure set; a path that is in any of them is
// customer-visible and gated.
func collectSDKExposure(nodeDir, pyDir, goClientPath string) (map[endpoint]struct{}, error) {
	out := map[endpoint]struct{}{}

	nodeSet, err := walkNodeSDK(nodeDir)
	if err != nil {
		return nil, fmt.Errorf("node sdk: %w", err)
	}
	for ep := range nodeSet {
		out[ep] = struct{}{}
	}

	pySet, err := walkPythonSDK(pyDir)
	if err != nil {
		return nil, fmt.Errorf("python sdk: %w", err)
	}
	for ep := range pySet {
		out[ep] = struct{}{}
	}

	goSet, err := walkGoSDK(goClientPath)
	if err != nil {
		return nil, fmt.Errorf("go sdk: %w", err)
	}
	for ep := range goSet {
		out[ep] = struct{}{}
	}

	return out, nil
}

// walkNodeSDK scans generated service TypeScript files for the
// `__request(OpenAPI, { method: 'GET', url: '/v1/...', ... })` literal
// pair baked in by openapi-typescript-codegen (sdk/node/scripts/gen.mjs).
// The two regexes match the method and url lines independently
// because the generator formats them on separate lines.
var (
	nodeMethodRe = regexp.MustCompile(`(?m)method:\s*['"]([A-Z]+)['"]`)
	nodeURLRe    = regexp.MustCompile(`(?m)url:\s*['"](/[^'"]+)['"]`)
)

func walkNodeSDK(dir string) (map[endpoint]struct{}, error) {
	out := map[endpoint]struct{}{}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return out, nil // tolerated: regen may not have run yet
		}
		return nil, err
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*.ts"))
	if err != nil {
		return nil, err
	}
	for _, p := range entries {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		var curMethod string
		for scanner.Scan() {
			line := scanner.Text()
			if mm := nodeMethodRe.FindStringSubmatch(line); mm != nil {
				curMethod = mm[1]
				continue
			}
			if um := nodeURLRe.FindStringSubmatch(line); um != nil {
				if curMethod != "" {
					out[endpoint{method: curMethod, path: um[1]}] = struct{}{}
					curMethod = ""
				}
			}
		}
		f.Close()
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// walkPythonSDK scans generated api/*/*.py files for the
// `"method": "get", "url": "/v1/..."` literal pair baked in by
// openapi-python-client (sdk/python/scripts/gen.py). Python uses
// lowercase HTTP method strings per the generator's convention.
var (
	pyMethodRe = regexp.MustCompile(`(?m)"method":\s*"([a-z]+)"`)
	pyURLRe    = regexp.MustCompile(`(?m)"url":\s*"(/[^"]+)"`)
)

func walkPythonSDK(dir string) (map[endpoint]struct{}, error) {
	out := map[endpoint]struct{}{}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*/*.py"))
	if err != nil {
		return nil, err
	}
	for _, p := range entries {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		mms := pyMethodRe.FindAllStringSubmatch(string(b), -1)
		ums := pyURLRe.FindAllStringSubmatch(string(b), -1)
		n := len(mms)
		if len(ums) < n {
			n = len(ums)
		}
		for i := 0; i < n; i++ {
			out[endpoint{method: strings.ToUpper(mms[i][1]), path: ums[i][1]}] = struct{}{}
		}
	}
	return out, nil
}

// walkGoSDK AST-walks pkg/api/client.go for `func (c *Client) X(...)`
// methods whose body is a single return statement of the form
// `return out, c.do(ctx, "METHOD", "/v1/...", nil, &out)`. It extracts
// the literal METHOD and path from c.do's call. The path is the third
// arg in every typed method (verified at pkg/api/client.go:147); we
// ignore variadic helpers (no body matches).
func walkGoSDK(clientPath string) (map[endpoint]struct{}, error) {
	out := map[endpoint]struct{}{}
	if _, err := os.Stat(clientPath); err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, clientPath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	for _, decl := range src.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		recv := fn.Recv
		if recv == nil || len(recv.List) != 1 {
			continue
		}
		star, ok := recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok || ident.Name != "Client" {
			continue
		}
		if fn.Body == nil {
			continue
		}
		ep, ok := extractDoCall(fn.Body)
		if !ok {
			continue
		}
		out[ep] = struct{}{}
	}
	return out, nil
}

// extractDoCall scans a function body for a return statement whose
// arguments contain c.do(ctx, "METHOD", "/path", ...). Real client
// methods are shape: `var out T; return out, c.do(...)` — two stmts
// — so we don't require len(body.List) == 1. We just find the LAST
// return statement (the only one in any well-formed typed client
// method) and walk its values.
func extractDoCall(body *ast.BlockStmt) (endpoint, bool) {
	var ret *ast.ReturnStmt
	for _, stmt := range body.List {
		if r, ok := stmt.(*ast.ReturnStmt); ok {
			ret = r
		}
	}
	if ret == nil {
		return endpoint{}, false
	}
	for _, r := range ret.Results {
		call, ok := r.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "do" {
			continue
		}
		if len(call.Args) < 3 {
			continue
		}
		// Args: ctx, METHOD, path, ... (path is positional 3rd)
		method, ok1 := stringLit(call.Args[1])
		path, ok2 := stringLit(call.Args[2])
		if !ok1 || !ok2 {
			continue
		}
		return endpoint{method: method, path: path}, true
	}
	return endpoint{}, false
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(bl.Value, `"`), true
}

// loadAllowlist reads api/endpoint_allowlist.yaml and returns the set
// of (path, method) entries carved out from the failure set. Missing
// file is treated as empty (the gate starts strict and grows over
// time via PR-time allowlist additions).
type allowlistEntry struct {
	Path   string `yaml:"path"`
	Method string `yaml:"method"`
	Reason string `yaml:"reason"`
}

func loadAllowlist(path string) (map[endpoint]struct{}, error) {
	out := map[endpoint]struct{}{}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	var entries []allowlistEntry
	if err := yaml.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("yaml unmarshal: %w", err)
	}
	for _, e := range entries {
		out[endpoint{method: strings.ToUpper(e.Method), path: e.Path}] = struct{}{}
	}
	return out, nil
}

func tally(label string, n int) {
	fmt.Fprintf(os.Stderr, "%s: %d\n", label, n)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "spec-endpoint-drift: "+format+"\n", args...)
	os.Exit(1)
}

func endpointLT(a, b endpoint) bool {
	if a.path != b.path {
		return a.path < b.path
	}
	return a.method < b.method
}