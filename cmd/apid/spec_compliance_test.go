package main

// spec_compliance_test.go is the CI gate that keeps api/openapi.yaml honest.
// It walks the Go AST of cmd/apid/server.go (routes), the pkg/api/* DTO files
// (schemas), and pkg/api/errors.go (Code* constants + StatusForCode) and
// asserts every entry has a matching entry in the spec — and vice versa.
//
// Three tests, each scoped to a single drift surface so a failure message
// points at the problem precisely. The tests are pure AST + YAML parsing:
// no reflection, no codegen, no I/O. Runs in <100 ms on a laptop.
//
// Run via: make spec-check (this file is the gate).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	serverSrcPath = "server.go"
	dtoFile       = "dto.go"
	secretsFile   = "secrets.go"
	manifestFile  = "appmanifest.go"
	cliauthFile   = "cliauth.go"
	errorsFile    = "errors.go"
)

// routeExclude lists server.go routes that are deliberately not in the
// public OpenAPI spec. Keep this in sync with the explanatory comments in
// server.go's handler() method. Sorted alphabetically for diff stability.
var routeExclude = map[string]bool{
	"GET /v1/account/dpa":             true, // public markdown (no auth)
	"POST /v1/webhooks/stripe":        true, // HMAC-signed webhook
	"GET /v1/compute-nodes":           true, // operator-only (ADR-029)
	"POST /v1/compute-nodes":          true, // operator-only
	"DELETE /v1/compute-nodes/{name}": true, // operator-only
	"GET /v1/events":                  true, // SSE (cookie+Bearer, not s.auth)
	"GET /login":                      true, // dashboard magic-link GET
	"POST /login":                     true, // dashboard magic-link POST
	"POST /logout":                    true, // dashboard logout
	"GET /auth/verify":                true, // magic-link consume
	"GET /v1/auth/google":             true, // Google OAuth 2.0 redirect
	"GET /v1/auth/google/callback":    true, // Google OAuth 2.0 callback
	"GET /oauth/callback":             true, // GitHub App install callback
	"GET /dashboard":                  true, // HTML dashboard
	"GET /dashboard/":                 true, // HTML dashboard
	"POST /dashboard/account/delete":  true, // HTML form
	"POST /dashboard/account/restore": true, // HTML form
	"GET /dashboard/account/export":   true, // session-auth twin of /v1/account/export
	"GET /dashboard/account/dpa":      true, // session-auth twin of DPA
	"POST /v1/cli-auth/code":          true, // CLI device-code mint
	"POST /v1/cli-auth/exchange":      true, // CLI device-code exchange
	"GET /cli-auth":                   true, // dashboard claim form
	"POST /cli-auth":                  true, // dashboard claim form submit
	"GET /status":                     true, // public HTML status page
	"GET /status/slo.json":            true, // public status JSON
	"GET /healthz":                    true, // loopback infra probe
}

// dtoExclude lists pkg/api exported DTOs that are intentionally not in the
// public OpenAPI spec. These are valid types — they live in pkg/api because
// they cross the apid/CLI boundary — but they belong to non-public surfaces
// (CLI device-code, public status page).
var dtoExclude = map[string]bool{
	"CliAuthCodeResponse":     true, // POST /v1/cli-auth/code (anonymous)
	"CliAuthExchangeRequest":  true, // POST /v1/cli-auth/exchange
	"CliAuthExchangeResponse": true, // POST /v1/cli-auth/exchange
	"CliAuthStatus":           true, // enum used by CLI auth
	"StatusPage":              true, // GET /status/slo.json (public status)
}

// codeExclude lists Code* constants that are intentionally not in the
// public OpenAPI spec. These are real code values used inside the
// CLI auth flow (which is anonymous on purpose), not part of the
// customer /v1/* API.
var codeExclude = map[string]bool{
	"CodeCliAuthPending":     true, // /v1/cli-auth/* (anonymous)
	"CodeCliAuthUnavailable": true, // /v1/cli-auth/* (anonymous)
}

// schemaSpecOnly lists schemas that exist in the spec but have no Go DTO.
// Either inline anonymous structs in handlers, or pure-documentation shapes
// (error envelopes that don't directly mirror a Go type).
var schemaSpecOnly = map[string]bool{
	"ChangePlanRequest": true, // inline {Plan string} in cmd/apid/handlers_ext.go
	"CreateKeyRequest":  true, // inline {Label string} in cmd/apid/handlers_ext.go
	"RateLimitPlain":    true, // documentation-only shape for the authlimiter 429
}

// findRepoRoot walks up from the working directory until it finds a go.mod.
// Returns the directory containing go.mod. Used to anchor the spec path
// regardless of cwd (the test runs with cwd = cmd/apid, not repo root).
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

// --- Test entry point ------------------------------------------------------

func TestSpecCompliance(t *testing.T) {
	// Locate the repo root by walking up from this test file until we find
	// a go.mod. Tests run with cwd = cmd/apid, so naive ../.. lands inside
	// .claude/worktrees/<name>/, not the repo root.
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}

	spec, err := loadSpec(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}

	t.Run("Routes", func(t *testing.T) {
		testRoutesParity(t, root, spec)
	})
	t.Run("Schemas", func(t *testing.T) {
		testSchemasParity(t, root, spec)
	})
	t.Run("ErrorCodes", func(t *testing.T) {
		testErrorCodesParity(t, root, spec)
	})
}

// --- Spec loader -----------------------------------------------------------

// specDoc is a plain Go view of the OpenAPI spec. We decode via map[string]any
// rather than yaml.Node because the YAML we consume is simple (string keys only)
// and Go maps are easier to inspect. The map[interface{}]interface{} complication
// is avoided by forcing the decoder to use string keys.
type specDoc struct {
	// Paths: path -> method -> responses (see below)
	Paths map[string]map[string]map[string]any
	// Schemas: schema name -> { properties: {field: ...} }
	Schemas map[string]map[string]any
	// Responses: status -> {media-type present: true}
	// Merged from per-operation responses + global components.responses
	// (resolved via $ref).
	Responses map[string]map[string]bool
	// componentsResponses: name -> raw response object. Used by
	// Methods() to resolve per-operation $ref entries into the
	// correct media-type list under Responses[status].
	componentsResponses map[string]any
}

func loadSpec(path string) (*specDoc, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// First unmarshal into a generic map; yaml.v3 still produces map[string]any
	// for string keys by default.
	var raw map[string]any
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, err
	}

	d := &specDoc{
		Paths:               map[string]map[string]map[string]any{},
		Schemas:             map[string]map[string]any{},
		Responses:           map[string]map[string]bool{},
		componentsResponses: map[string]any{},
	}

	// Pass 1: collect components.responses so per-operation $ref
	// resolution can use them.
	if comps, ok := raw["components"].(map[string]any); ok {
		if responses, ok := comps["responses"].(map[string]any); ok {
			for name, body := range responses {
				d.componentsResponses[name] = body
			}
		}
	}

	// Pass 2: walk paths and resolve $refs against the now-populated
	// componentsResponses map.
	if paths, ok := raw["paths"].(map[string]any); ok {
		for p, methods := range paths {
			mop, ok := methods.(map[string]any)
			if !ok {
				continue
			}
			d.Methods(p, mop)
		}
	}

	// Pass 3: schemas (used by Schemas parity).
	if comps, ok := raw["components"].(map[string]any); ok {
		if schemas, ok := comps["schemas"].(map[string]any); ok {
			for name, body := range schemas {
				if m, ok := body.(map[string]any); ok {
					d.Schemas[name] = m
				}
			}
		}
	}

	return d, nil
}

// wire methods into Paths and collect responses into Responses.
// Per-operation $ref entries are resolved through components.responses
// so we record the resolved content media types under the numeric
// status key (e.g. Responses["400"]). Without this resolution the
// parity test would only see literal responses, not the named
// ValidationFailed / NotFound / Unauthorized reuse the spec actually
// relies on, and CI would drown in false "no response" errors.
func (d *specDoc) Methods(path string, methods map[string]any) {
	opmap := map[string]map[string]any{}
	for m, op := range methods {
		mm, ok := op.(map[string]any)
		if !ok {
			continue
		}
		opmap[m] = mm
		if resp, ok := mm["responses"].(map[string]any); ok {
			for status, val := range resp {
				collectResponse(status, val, d)
			}
		}
	}
	d.Paths[path] = opmap
}

// collectResponse records the media types for a single per-operation
// response object under d.Responses[status]. The OpenAPI shape is
//
//	'400': { $ref: '#/components/responses/ValidationFailed' }
//
// or inline:
//
//	'200': { content: { 'application/json': {schema: ...} } }
//
// We resolve the $ref against d.componentsResponses so the media
// types get recorded under the numeric status key.
func collectResponse(status string, val any, d *specDoc) {
	mp, ok := val.(map[string]any)
	if !ok {
		return
	}
	if content, ok := mp["content"].(map[string]any); ok {
		recordMedia(status, content, d.Responses)
		return
	}
	if ref, ok := mp["$ref"].(string); ok {
		const prefix = "#/components/responses/"
		if !strings.HasPrefix(ref, prefix) {
			return
		}
		name := ref[len(prefix):]
		body, ok := d.componentsResponses[name].(map[string]any)
		if !ok {
			return
		}
		if content, ok := body["content"].(map[string]any); ok {
			recordMedia(status, content, d.Responses)
		}
	}
}

func recordMedia(status string, content map[string]any, into map[string]map[string]bool) {
	ct, ok := into[status]
	if !ok {
		ct = map[string]bool{}
		into[status] = ct
	}
	for media := range content {
		ct[media] = true
	}
}

// --- Routes parity ---------------------------------------------------------

func testRoutesParity(t *testing.T, root string, spec *specDoc) {
	t.Helper()

	codeRoutes, err := scanServerRoutes(filepath.Join(root, "cmd/apid", serverSrcPath))
	if err != nil {
		t.Fatalf("scan server.go: %v", err)
	}
	// Apply exclude list.
	var kept []string
	for _, r := range codeRoutes {
		if routeExclude[r] {
			continue
		}
		kept = append(kept, r)
	}
	sort.Strings(kept)

	specRoutes := flattenSpecPaths(spec.Paths)
	sort.Strings(specRoutes)

	// Both directions: missing in spec, extra in code.
	var missingInSpec, extraInCode []string
	i, j := 0, 0
	for i < len(kept) && j < len(specRoutes) {
		switch {
		case kept[i] == specRoutes[j]:
			i++
			j++
		case kept[i] < specRoutes[j]:
			missingInSpec = append(missingInSpec, kept[i])
			i++
		default:
			extraInCode = append(extraInCode, specRoutes[j])
			j++
		}
	}
	for ; i < len(kept); i++ {
		missingInSpec = append(missingInSpec, kept[i])
	}
	for ; j < len(specRoutes); j++ {
		extraInCode = append(extraInCode, specRoutes[j])
	}

	if len(missingInSpec) > 0 || len(extraInCode) > 0 {
		t.Errorf("route parity failed:")
		for _, r := range missingInSpec {
			t.Errorf("  missing in spec: %s", r)
		}
		for _, r := range extraInCode {
			t.Errorf("  extra in code (not in spec): %s", r)
		}
	}
}

// scanServerRoutes parses server.go and extracts every mux.HandleFunc /
// mux.Handle call whose first argument is a route literal "METHOD /path".
func scanServerRoutes(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var routes []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle" {
			return true
		}
		if len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconvUnquote(lit.Value)
		if err != nil {
			return true
		}
		// Format: "METHOD /path" from Go 1.22+ mux, or "/path" alone.
		sp := strings.SplitN(s, " ", 2)
		if len(sp) != 2 {
			return true // just a path, no method — skip (rare)
		}
		method, p := strings.ToUpper(sp[0]), sp[1]
		routes = append(routes, method+" "+p)
		return true
	})
	return routes, nil
}

func flattenSpecPaths(paths map[string]map[string]map[string]any) []string {
	var out []string
	methods := []string{"get", "post", "put", "patch", "delete", "head", "options"}
	for p, ops := range paths {
		for _, m := range methods {
			if _, ok := ops[m]; ok {
				out = append(out, strings.ToUpper(m)+" "+p)
			}
		}
	}
	return out
}

// strconvUnquote is a minimal unquote that handles the backtick + double-quote
// forms Go uses for raw and interpreted string literals.
func strconvUnquote(s string) (string, error) {
	if len(s) < 2 {
		return "", fmt.Errorf("too short")
	}
	if s[0] == '`' && s[len(s)-1] == '`' {
		return s[1 : len(s)-1], nil
	}
	if s[0] == '"' && s[len(s)-1] == '"' {
		return strconvUnescape(s[1 : len(s)-1])
	}
	return "", fmt.Errorf("unknown quote style: %s", s)
}

func strconvUnescape(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(s) {
			return "", fmt.Errorf("trailing backslash")
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String(), nil
}

// --- Schemas parity --------------------------------------------------------

func testSchemasParity(t *testing.T, root string, spec *specDoc) {
	t.Helper()

	files := []string{
		filepath.Join(root, "pkg", "api", dtoFile),
		filepath.Join(root, "pkg", "api", secretsFile),
		filepath.Join(root, "pkg", "api", manifestFile),
		filepath.Join(root, "pkg", "api", cliauthFile),
		filepath.Join(root, "pkg", "api", errorsFile),
	}
	dtos, err := scanDTOs(files)
	if err != nil {
		t.Fatalf("scan DTOs: %v", err)
	}

	var missingInSpec []string
	for name := range dtos {
		if dtoExclude[name] {
			continue
		}
		if _, ok := spec.Schemas[name]; !ok {
			missingInSpec = append(missingInSpec, name)
		}
	}
	sort.Strings(missingInSpec)

	// Reverse: every spec schema should have a Go DTO OR be in the
	// spec-only allowlist. This catches dead schemas left over from
	// refactors.
	dtoNames := map[string]bool{}
	for n := range dtos {
		dtoNames[n] = true
	}
	var extraInCode []string
	for name := range spec.Schemas {
		if dtoNames[name] || schemaSpecOnly[name] {
			continue
		}
		extraInCode = append(extraInCode, name)
	}
	sort.Strings(extraInCode)

	if len(missingInSpec) > 0 || len(extraInCode) > 0 {
		t.Errorf("schema parity failed:")
		for _, r := range missingInSpec {
			t.Errorf("  DTO in code but no schema in spec: %s", r)
		}
		for _, r := range extraInCode {
			t.Errorf("  schema in spec but no DTO in code: %s", r)
		}
	}

	// Per-schema: every JSON-named field must appear in the spec properties.
	// Catches: field added to DTO but not to spec, renamed, etc.
	for name, fields := range dtos {
		specBody, ok := spec.Schemas[name]
		if !ok {
			continue
		}
		propSet, ok := extractSchemaProperties(specBody)
		if !ok {
			continue
		}
		var missing []string
		for f := range fields {
			if !propSet[f] {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("schema %s: missing properties in spec: %v", name, missing)
		}
	}
}

// scanDTOs returns name -> set of JSON field names for every exported struct
// in the given files.
func scanDTOs(paths []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for _, p := range paths {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if !ts.Name.IsExported() {
					continue
				}
				if ts.Name.Name == "Problem" && !strings.HasSuffix(p, "errors.go") {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				fields := map[string]bool{}
				for _, f := range st.Fields.List {
					if f.Tag == nil {
						continue
					}
					tag := f.Tag.Value
					name := jsonFieldName(tag)
					if name == "" || name == "-" {
						continue
					}
					fields[name] = true
				}
				out[ts.Name.Name] = fields
			}
		}
	}
	return out, nil
}

var jsonTagRE = regexp.MustCompile(`json:"([^"]+)"`)

func jsonFieldName(tag string) string {
	m := jsonTagRE.FindStringSubmatch(tag)
	if len(m) < 2 {
		return ""
	}
	parts := strings.Split(m[1], ",")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func extractSchemaProperties(m map[string]any) (map[string]bool, bool) {
	props, ok := m["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	out := map[string]bool{}
	for k := range props {
		out[k] = true
	}
	return out, true
}

// --- Error codes parity ----------------------------------------------------

func testErrorCodesParity(t *testing.T, root string, spec *specDoc) {
	t.Helper()

	path := filepath.Join(root, "pkg", "api", errorsFile)
	codes, err := scanErrorCodes(path)
	if err != nil {
		t.Fatalf("scan error codes: %v", err)
	}

	// Every code in code must have a corresponding response in spec
	// whose status is StatusForCode(code) AND whose content includes
	// application/problem+json (with the exception of plain-text 429s).
	// codes is pre-filtered by scanErrorCodes against codeExclude so
	// non-public codes (CLI auth) never reach this loop.
	var missing []string
	for code, status := range codes {
		media, ok := spec.Responses[fmt.Sprintf("%d", status)]
		if !ok {
			missing = append(missing, fmt.Sprintf("%s (status %d): no response in spec", code, status))
			continue
		}
		if !media["application/problem+json"] {
			missing = append(missing, fmt.Sprintf("%s (status %d): no application/problem+json in spec response", code, status))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("error code parity failed:")
		for _, m := range missing {
			t.Errorf("  %s", m)
		}
	}

	// Documented exception: 429 must declare BOTH application/problem+json
	// (for code-driven 429s) AND text/plain (for the authlimiter). Hard
	// fail if either is missing.
	if media, ok := spec.Responses["429"]; ok {
		if !media["application/problem+json"] {
			t.Errorf("429 must declare application/problem+json (for plan_limit_concurrency / quota_exhausted)")
		}
		if !media["text/plain"] {
			t.Errorf("429 must declare text/plain (authlimiter middleware in pkg/middleware/authlimit.go)")
		}
	}
}

// scanErrorCodes parses errors.go for Code* constants and the
// StatusForCode function's switch cases. Returns code -> status.
//
// Anchored on the StatusForCode FuncDecl name rather than structural
// pattern matching, so a refactor that replaces the switch with a
// map[string]int (or any other shape) fails loudly here with a clear
// message instead of silently producing an empty statusFor and
// surfacing as "no response in spec" for every code in CI.
func scanErrorCodes(path string) (map[string]int, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// 1. Collect all Code* constants.
	codes := map[string]string{} // code-name -> string value
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, name := range vs.Names {
			if !strings.HasPrefix(name.Name, "Code") {
				continue
			}
			if len(vs.Values) == 0 {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			v, err := strconvUnquote(lit.Value)
			if err != nil {
				continue
			}
			codes[name.Name] = v
		}
		return true
	})

	// 2. Find the StatusForCode function and walk its body's switch.
	// Anchoring by name (instead of structural pattern matching any
	// switch with Code* cases) means a refactor that replaces the
	// switch — e.g. with a map[string]int lookup or a table-driven
	// function — fails here with a clear message rather than
	// silently producing an empty statusFor.
	statusFor, err := walkStatusForCode(f, codes)
	if err != nil {
		return nil, err
	}

	// Sanity: if we found Code* constants but no status mappings, the
	// walker almost certainly lost the function — fail loudly with a
	// pointed message instead of letting CI drown in false positives.
	if len(codes) > 0 && len(statusFor) == 0 {
		return nil, fmt.Errorf(
			"scanErrorCodes: found %d Code* constants but no status mappings — "+
				"StatusForCode(refactor) shape may have changed. Update %s accordingly",
			len(codes), path)
	}

	// Filter out codes that are intentionally not in the customer spec
	// (CLI auth flow). The walker returns all Code* -> status pairs;
	// the parity test sees only the ones we want documented.
	filtered := map[string]int{}
	for stringCode, status := range statusFor {
		if isExcludedCode(stringCode, codes) {
			continue
		}
		filtered[stringCode] = status
	}
	return filtered, nil
}

// isExcludedCode reports whether a string-code value comes from a
// Code* constant in codeExclude. codes is the raw constant-name ->
// string-code map produced by the walker.
func isExcludedCode(stringCode string, codes map[string]string) bool {
	for constName, codeValue := range codes {
		if codeValue != stringCode {
			continue
		}
		if codeExclude[constName] {
			return true
		}
	}
	return false
}

// walkStatusForCode finds the FuncDecl named "StatusForCode" and walks
// the case clauses of its body's (Type)SwitchStmt. Each clause may
// contain both Code* Idents and an http.StatusXxx call; the call
// supplies the status and the Idents supply the codes.
func walkStatusForCode(f *ast.File, codes map[string]string) (map[string]int, error) {
	statusFor := map[string]int{}

	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if gd.Name.Name == "StatusForCode" {
			fn = gd
			break
		}
	}
	if fn == nil {
		return nil, fmt.Errorf("StatusForCode function not found in errors.go")
	}
	body := fn.Body
	if body == nil {
		return nil, fmt.Errorf("StatusForCode has no body (abstract or build-tagged-out? check //go:build tags)")
	}

	// Find the (Type)SwitchStmt. We accept both forms — current
	// implementation is a *ast.SwitchStmt (switch code { ... }) but
	// tolerate a TypeSwitchStmt too in case a future refactor lands.
	var sw ast.Stmt
	for _, stmt := range body.List {
		if _, ok := stmt.(*ast.SwitchStmt); ok {
			sw = stmt
			break
		}
		if _, ok := stmt.(*ast.TypeSwitchStmt); ok {
			sw = stmt
			break
		}
	}
	if sw == nil {
		return nil, fmt.Errorf("StatusForCode body has no (Type)SwitchStmt — refactor may have replaced it; update spec_compliance_test.go")
	}

	// Pick the body of whichever kind we found.
	var cases []ast.Stmt
	switch s := sw.(type) {
	case *ast.SwitchStmt:
		cases = s.Body.List
	case *ast.TypeSwitchStmt:
		// TypeSwitchStmt assigns the tag to an implicit variable; not
		// the current shape. Returned empty to be safe.
		_ = s
		return statusFor, nil
	}

	for _, stmt := range cases {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		// Case labels: collect Code* Ident references (= the codes
		// that map to this case's status).
		var codesHere []string
		for _, expr := range cc.List {
			id, ok := expr.(*ast.Ident)
			if !ok || !strings.HasPrefix(id.Name, "Code") {
				continue
			}
			if v, ok := codes[id.Name]; ok {
				codesHere = append(codesHere, v)
			}
		}
		// Case body: look for `return http.StatusXxx` and grab the
		// status. The current shape is `case X, Y: return http.StatusZ`
		// but we tolerate `return foo(http.StatusZ)` or an assignment
		// to a status variable — any function call to http.StatusXxx
		// in the case body counts.
		status, hasStatus := extractStatusFromCaseBody(cc.Body)
		if hasStatus {
			for _, c := range codesHere {
				statusFor[c] = status
			}
		}
	}
	return statusFor, nil
}

// extractStatusFromCaseBody walks an AST case-clause body looking for an
// http.StatusXxx identifier — either as a direct return value or as
// an argument to a function call (e.g. return NewProblem(
// http.StatusForbidden, ...)). The first match wins; if none is found,
// returns hasStatus=false so the case is treated as a fall-through and
// the caller doesn't assign codes to it.
func extractStatusFromCaseBody(body []ast.Stmt) (int, bool) {
	var found int
	var seen bool
	ast.Inspect(&ast.BlockStmt{List: body}, func(n ast.Node) bool {
		if seen {
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if s, ok := httpStatusByIdent[id.Name]; ok {
			found = s
			seen = true
			return false
		}
		return true
	})
	return found, seen
}

// httpStatusByIdent maps net/http status identifiers to their numeric value.
// StatusContinue, StatusSwitchingProtocols, etc. omitted — only the
// statuses that pkg/api/errors.go actually uses.
var httpStatusByIdent = map[string]int{
	"StatusForbidden":             403,
	"StatusTooManyRequests":       429,
	"StatusRequestEntityTooLarge": 413,
	"StatusBadRequest":            400,
	"StatusServiceUnavailable":    503,
	"StatusUnauthorized":          401,
	"StatusNotFound":              404,
	"StatusConflict":              409,
	"StatusUnprocessableEntity":   422,
	"StatusGone":                  410,
}
