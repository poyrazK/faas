package speccompliance

// edge_rule_kinds_test.go pins the edge-rule `kind` vocabulary and the
// per-kind action variants in api/openapi.yaml to the Go source of truth:
// the kinds accepted by state.EdgeRuleKind.IsValid (pkg/state/types.go),
// which apid's edge-rule validation gates on. kind=cache (ADR-122) shipped
// in Go while its action schema was left out of the spec's action oneOf;
// this check makes that class of drift fail CI.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const edgeRuleKindsFile = "types.go" // pkg/state

// edgeRuleKindSchemas carry a `kind` enum that must equal the Go vocabulary.
var edgeRuleKindSchemas = []string{"CreateEdgeRuleRequest", "EdgeRuleResponse"}

// edgeRuleActionSchemas carry an `action` oneOf with one variant per kind.
var edgeRuleActionSchemas = []string{"CreateEdgeRuleRequest", "UpdateEdgeRuleRequest", "EdgeRuleResponse"}

// edgeRuleActionAcronyms keeps the spec's existing schema names
// (EdgeRuleCORSAction, not EdgeRuleCorsAction).
var edgeRuleActionAcronyms = map[string]string{"cors": "CORS", "ip": "IP", "jwt": "JWT", "waf": "WAF"}

func testEdgeRuleKindsParity(t *testing.T, root string, spec *specDoc) {
	kinds, err := scanEdgeRuleKinds(filepath.Join(root, "pkg", "state", edgeRuleKindsFile))
	if err != nil {
		t.Fatalf("scan edge rule kinds: %v", err)
	}
	if len(kinds) == 0 {
		t.Fatal("no edge rule kinds found in pkg/state; the parity check is vacuous")
	}

	for _, name := range edgeRuleKindSchemas {
		got, err := schemaPropertyEnum(spec, name, "kind")
		if err != nil {
			t.Error(err)
			continue
		}
		if missing, extra := setDiff(kinds, got); len(missing)+len(extra) > 0 {
			t.Errorf("%s.kind enum drifts from state.EdgeRuleKind.IsValid: missing %v, extra %v", name, missing, extra)
		}
	}

	want := make([]string, 0, len(kinds))
	for _, k := range kinds {
		want = append(want, edgeRuleActionSchemaName(k))
	}
	for _, s := range want {
		if _, ok := spec.Schemas[s]; !ok {
			t.Errorf("components.schemas.%s is missing; every edge rule kind needs an action schema", s)
		}
	}
	for _, name := range edgeRuleActionSchemas {
		got, err := schemaPropertyOneOfRefs(spec, name, "action")
		if err != nil {
			t.Error(err)
			continue
		}
		if missing, extra := setDiff(want, got); len(missing)+len(extra) > 0 {
			t.Errorf("%s.action oneOf drifts from the edge rule kinds: missing %v, extra %v", name, missing, extra)
		}
	}
}

// scanEdgeRuleKinds returns the string values of the EdgeRuleKind constants
// listed in the `case` clauses of EdgeRuleKind.IsValid.
func scanEdgeRuleKinds(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	var isValid *ast.FuncDecl
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.CONST {
				continue
			}
			for _, s := range d.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok || !isIdent(vs.Type, "EdgeRuleKind") {
					continue
				}
				for i, n := range vs.Names {
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return nil, fmt.Errorf("const %s is not a string literal", n.Name)
					}
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						return nil, err
					}
					values[n.Name] = v
				}
			}
		case *ast.FuncDecl:
			if d.Name.Name == "IsValid" && d.Recv != nil && len(d.Recv.List) == 1 && isIdent(d.Recv.List[0].Type, "EdgeRuleKind") {
				isValid = d
			}
		}
	}
	if isValid == nil {
		return nil, fmt.Errorf("EdgeRuleKind.IsValid not found in %s", path)
	}
	var kinds []string
	ast.Inspect(isValid.Body, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, e := range cc.List {
			id, ok := e.(*ast.Ident)
			if !ok {
				continue
			}
			if v, ok := values[id.Name]; ok {
				kinds = append(kinds, v)
			}
		}
		return true
	})
	return kinds, nil
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func edgeRuleActionSchemaName(kind string) string {
	if a, ok := edgeRuleActionAcronyms[kind]; ok {
		return "EdgeRule" + a + "Action"
	}
	var b strings.Builder
	for _, part := range strings.Split(kind, "_") {
		if part != "" {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return "EdgeRule" + b.String() + "Action"
}

func schemaProperty(spec *specDoc, schema, prop string) (map[string]any, error) {
	s, ok := spec.Schemas[schema]
	if !ok {
		return nil, fmt.Errorf("components.schemas.%s not found", schema)
	}
	props, _ := s["properties"].(map[string]any)
	p, ok := props[prop].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.properties.%s not found", schema, prop)
	}
	return p, nil
}

func schemaPropertyEnum(spec *specDoc, schema, prop string) ([]string, error) {
	p, err := schemaProperty(spec, schema, prop)
	if err != nil {
		return nil, err
	}
	raw, ok := p["enum"].([]any)
	if !ok {
		return nil, fmt.Errorf("%s.%s has no enum", schema, prop)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		out = append(out, fmt.Sprint(v))
	}
	return out, nil
}

func schemaPropertyOneOfRefs(spec *specDoc, schema, prop string) ([]string, error) {
	p, err := schemaProperty(spec, schema, prop)
	if err != nil {
		return nil, err
	}
	raw, ok := p["oneOf"].([]any)
	if !ok {
		return nil, fmt.Errorf("%s.%s has no oneOf", schema, prop)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		m, _ := v.(map[string]any)
		ref, _ := m["$ref"].(string)
		const prefix = "#/components/schemas/"
		if !strings.HasPrefix(ref, prefix) {
			return nil, fmt.Errorf("%s.%s oneOf entry %v is not a components.schemas $ref", schema, prop, v)
		}
		out = append(out, strings.TrimPrefix(ref, prefix))
	}
	return out, nil
}

// setDiff returns the sorted entries of want absent from got, and of got
// absent from want.
func setDiff(want, got []string) (missing, extra []string) {
	in := func(xs []string) map[string]bool {
		m := make(map[string]bool, len(xs))
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	w, g := in(want), in(got)
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
