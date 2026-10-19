package speccompliance

// deployment_stage_state_test.go pins components.schemas.DeploymentStageState
// and its sub-schemas to the Go structs that produce `deployments.stage_state`
// (ADR-117). GET /v1/deployments/{id}/stages writes that jsonb verbatim and
// DeploymentResponse.stage_state embeds it, so the struct tags are the wire
// contract. The /stages schema once lacked dependency_gate,
// hosting_verification, the retry fields and the `cancelled` history status;
// this check makes a new field, a dropped field, a required/optional flip or a
// new stage/status constant fail CI until the spec is updated.
//
// Pure AST + YAML, like the rest of the package. The name has the
// TestSpecCompliance prefix so `make spec-check` runs it.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const schemaRefPrefix = "#/components/schemas/"

// stageStateSchemaPins maps each spec schema to the Go struct it describes.
var stageStateSchemaPins = []struct{ schema, file, goType string }{
	{"DeploymentStageState", "pkg/state/types.go", "StageState"},
	{"DeploymentStageHistoryEntry", "pkg/state/types.go", "StageStateItem"},
	{"DeploymentDependencyGate", "pkg/state/deployment_dependency_gate.go", "DeploymentDependencyGate"},
	{"DeploymentDependencyPin", "pkg/state/deployment_dependency_gate.go", "DeploymentDependencyPin"},
	{"DeploymentBuildDependencyGate", "pkg/state/build_dependency_gate.go", "BuildDependencyGate"},
	{"DeploymentHostingVerification", "pkg/state/hosting_verification.go", "HostingVerificationProgress"},
	{"HostingRouteCheckSet", "pkg/apihostingreceipt/receipt.go", "RouteCheckSet"},
	{"HostingRouteCheckResult", "pkg/apihostingreceipt/receipt.go", "RouteCheckResult"},
}

func TestSpecComplianceDeploymentStageState(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	spec, err := loadSpec(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load spec: %v", err)
	}
	t.Run("Fields", func(t *testing.T) { testStageStateFields(t, root, spec) })
	t.Run("Enums", func(t *testing.T) { testStageStateEnums(t, root, spec) })
	t.Run("Consumers", func(t *testing.T) { testStageStateConsumers(t, spec) })
}

// goJSONField is one tagged struct field as encoding/json emits it.
type goJSONField struct {
	name      string
	omitempty bool
	pointer   bool
	nullable  bool   // pointer without omitempty: emitted as null
	elemType  string // struct type name after stripping * and []
}

// required reports whether encoding/json always emits the key. omitempty
// never omits a struct value, so a non-pointer time.Time is always present.
func (f goJSONField) required() bool {
	return !f.omitempty || (!f.pointer && f.elemType == "time.Time")
}

func testStageStateFields(t *testing.T, root string, spec *specDoc) {
	schemaFor := map[string]string{}
	for _, p := range stageStateSchemaPins {
		schemaFor[p.goType] = p.schema
	}
	for _, p := range stageStateSchemaPins {
		fields, err := scanStructJSONFields(filepath.Join(root, filepath.FromSlash(p.file)), p.goType)
		if err != nil {
			t.Errorf("%s: %v", p.goType, err)
			continue
		}
		s, ok := spec.Schemas[p.schema]
		if !ok {
			t.Errorf("components.schemas.%s is missing", p.schema)
			continue
		}
		props, _ := s["properties"].(map[string]any)
		var goNames, goRequired []string
		for _, f := range fields {
			goNames = append(goNames, f.name)
			if f.required() {
				goRequired = append(goRequired, f.name)
			}
		}
		if missing, extra := stageStateSetDiff(goNames, mapKeys(props)); len(missing)+len(extra) > 0 {
			t.Errorf("%s properties drift from %s JSON tags: missing %v, extra %v", p.schema, p.goType, missing, extra)
		}
		if missing, extra := stageStateSetDiff(goRequired, anyStrings(s["required"])); len(missing)+len(extra) > 0 {
			t.Errorf("%s.required drifts from %s (fields without omitempty): missing %v, extra %v", p.schema, p.goType, missing, extra)
		}
		for _, f := range fields {
			prop, _ := props[f.name].(map[string]any)
			if prop == nil {
				continue
			}
			if f.nullable && !anyContains(prop["type"], "null") {
				t.Errorf("%s.%s must allow null: %s.%s is a pointer without omitempty", p.schema, f.name, p.goType, f.name)
			}
			if want, ok := schemaFor[f.elemType]; ok {
				if got := propertyRef(prop); got != want {
					t.Errorf("%s.%s must $ref %s (Go type %s), got %q", p.schema, f.name, want, f.elemType, got)
				}
			}
		}
	}
}

func testStageStateEnums(t *testing.T, root string, spec *specDoc) {
	stateDir := filepath.Join(root, "pkg", "state")
	receipt := filepath.Join(root, "pkg", "apihostingreceipt", "receipt.go")
	stages := stringConsts(t, []string{filepath.Join(stateDir, "types.go")}, func(name string, typ ast.Expr) bool {
		return isNamedIdent(typ, "StageName")
	})
	if len(stages) == 0 {
		t.Fatal("no StageName constants found in pkg/state/types.go; the parity check is vacuous")
	}
	stateFiles, err := filepath.Glob(filepath.Join(stateDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var nonTest []string
	for _, f := range stateFiles {
		if !strings.HasSuffix(f, "_test.go") {
			nonTest = append(nonTest, f)
		}
	}
	historyStatuses := stringConsts(t, nonTest, func(name string, _ ast.Expr) bool {
		return strings.HasPrefix(name, "stageHistoryStatus")
	})
	byPrefix := func(prefix string) []string {
		return stringConsts(t, []string{receipt}, func(name string, _ ast.Expr) bool { return strings.HasPrefix(name, prefix) })
	}
	named := func(names ...string) []string {
		return stringConsts(t, []string{receipt}, func(name string, _ ast.Expr) bool {
			for _, n := range names {
				if n == name {
					return true
				}
			}
			return false
		})
	}

	cases := []struct {
		schema, prop, source string
		want                 []string
	}{
		{"DeploymentStageState", "current", "state.StageName consts + \"\" (no open stage)", append([]string{""}, stages...)},
		{"DeploymentStageState", "retry_requested_stage", "state.StageName consts", stages},
		{"DeploymentStageHistoryEntry", "name", "state.StageName consts", stages},
		{"DeploymentStageHistoryEntry", "status", "state.stageHistoryStatus* consts", historyStatuses},
		{"HostingRouteCheckSet", "status", "apihostingreceipt.RouteCheckSet* consts", byPrefix("RouteCheckSet")},
		{"HostingRouteCheckSet", "source", "apihostingreceipt.RouteCheckSource* consts", byPrefix("RouteCheckSource")},
		{"HostingRouteCheckResult", "status", "apihostingreceipt.Smoke{Verified,Failed,Skipped}", named("SmokeVerified", "SmokeFailed", "SmokeSkipped")},
	}
	for _, c := range cases {
		props, _ := spec.Schemas[c.schema]["properties"].(map[string]any)
		prop, _ := props[c.prop].(map[string]any)
		got := anyStrings(prop["enum"])
		if missing, extra := stageStateSetDiff(c.want, got); len(missing)+len(extra) > 0 {
			t.Errorf("%s.%s enum drifts from %s: missing %q, extra %q", c.schema, c.prop, c.source, missing, extra)
		}
	}
}

// testStageStateConsumers pins both wire surfaces to the one named schema.
func testStageStateConsumers(t *testing.T, spec *specDoc) {
	props, _ := spec.Schemas["DeploymentResponse"]["properties"].(map[string]any)
	prop, _ := props["stage_state"].(map[string]any)
	if got := propertyRef(prop); got != "DeploymentStageState" {
		t.Errorf("DeploymentResponse.stage_state must $ref DeploymentStageState, got %q", got)
	}
	op := spec.Paths["/v1/deployments/{id}/stages"]["get"]
	schema := digMap(op, "responses", "200", "content", "application/json", "schema")
	if got := propertyRef(schema); got != "DeploymentStageState" {
		t.Errorf("GET /v1/deployments/{id}/stages 200 must $ref DeploymentStageState, got %q", got)
	}
}

// scanStructJSONFields returns the JSON-tagged fields of struct typeName.
func scanStructJSONFields(path, typeName string) ([]goJSONField, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	var st *ast.StructType
	ast.Inspect(f, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == typeName {
			st, _ = ts.Type.(*ast.StructType)
			return false
		}
		return st == nil
	})
	if st == nil {
		return nil, fmt.Errorf("struct %s not found in %s", typeName, path)
	}
	var out []goJSONField
	for _, field := range st.Fields.List {
		if field.Tag == nil {
			continue
		}
		tag, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return nil, err
		}
		parts := strings.Split(reflect.StructTag(tag).Get("json"), ",")
		if parts[0] == "" || parts[0] == "-" {
			continue
		}
		omit := false
		for _, o := range parts[1:] {
			omit = omit || o == "omitempty"
		}
		_, isPtr := field.Type.(*ast.StarExpr)
		out = append(out, goJSONField{name: parts[0], omitempty: omit, pointer: isPtr, nullable: isPtr && !omit, elemType: elemTypeName(field.Type)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("struct %s has no JSON-tagged fields", typeName)
	}
	return out, nil
}

func elemTypeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return elemTypeName(x.X)
	case *ast.ArrayType:
		return elemTypeName(x.Elt)
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok {
			if pkg.Name == "time" {
				return "time.Time"
			}
		}
		return x.Sel.Name
	}
	return ""
}

// stringConsts returns the values of string constants in files whose name
// and declared type satisfy keep.
func stringConsts(t *testing.T, files []string, keep func(name string, typ ast.Expr) bool) []string {
	t.Helper()
	var out []string
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, s := range gd.Specs {
				vs := s.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i >= len(vs.Values) || !keep(n.Name, vs.Type) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, v)
				}
			}
		}
	}
	return out
}

func isNamedIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// propertyRef returns the schema name a property (or its array items) refs.
func propertyRef(prop map[string]any) string {
	if prop == nil {
		return ""
	}
	if items, ok := prop["items"].(map[string]any); ok && prop["$ref"] == nil {
		prop = items
	}
	ref, _ := prop["$ref"].(string)
	return strings.TrimPrefix(ref, schemaRefPrefix)
}

func digMap(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		next, _ := m[k].(map[string]any)
		if next == nil {
			return nil
		}
		m = next
	}
	return m
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// anyStrings flattens a YAML scalar list; nil yields nil.
func anyStrings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, x := range raw {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

func anyContains(v any, want string) bool {
	if s, ok := v.(string); ok {
		return s == want
	}
	for _, x := range anyStrings(v) {
		if x == want {
			return true
		}
	}
	return false
}

// stageStateSetDiff returns the sorted entries of want absent from got, and
// of got absent from want.
func stageStateSetDiff(want, got []string) (missing, extra []string) {
	w, g := map[string]bool{}, map[string]bool{}
	for _, x := range want {
		w[x] = true
	}
	for _, x := range got {
		g[x] = true
	}
	for x := range w {
		if !g[x] {
			missing = append(missing, x)
		}
	}
	for x := range g {
		if !w[x] {
			extra = append(extra, x)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
