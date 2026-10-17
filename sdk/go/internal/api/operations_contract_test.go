// adr: 521
package api

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// The standalone module remains buildable without the daemon. In the monorepo,
// validate the actual platform declarations so copied wire types cannot drift.
func TestOperationWireContractMatchesPlatform(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "pkg", "api")
	platform := filepath.Join(root, "operations.go")
	if _, err := os.Stat(platform); os.IsNotExist(err) {
		t.Skip("platform source is absent from the standalone module")
	} else if err != nil {
		t.Fatal(err)
	}
	want := operationDeclarations(t, platform)
	got := operationDeclarations(t, "operations.go")
	for _, file := range []string{"operation_subject.go", "operation_milestones.go", "operation_workflows.go", "operation_waits.go", "operation_limits.go"} {
		for name, declaration := range operationDeclarations(t, filepath.Join(root, file)) {
			want[name] = declaration
		}
		for name, declaration := range operationDeclarations(t, file) {
			got[name] = declaration
		}
	}
	for name, declaration := range want {
		if got[name] != declaration {
			t.Errorf("operation wire declaration drift: %s", name)
		}
	}
	if len(got) != len(want) {
		t.Fatal("operation wire declaration set changed")
	}
	bounds := operationDeclarations(t, filepath.Join(root, "limits.go"))
	sdkBounds := operationDeclarations(t, "operations_http.go")
	for sdkName, platformName := range map[string]string{
		"operationArtifactMaxBytes": "OperationArtifactSpoolMaxBytes",
		"operationSubmissionKeyMax": "OperationIdempotencyKeyMaxBytes",
	} {
		if sdkBounds[sdkName] != bounds[platformName] || sdkBounds[sdkName] == "" {
			t.Errorf("operation bound drift: %s", sdkName)
		}
	}
	headers := operationDeclarations(t, filepath.Join(root, "headers.go"))
	if sdkBounds["InvocationIDHeader"] != headers["InvocationIDHeader"] || sdkBounds["InvocationIDHeader"] == "" {
		t.Fatal("operation invocation header drift")
	}
}

func operationDeclarations(t *testing.T, path string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string)
	for _, declaration := range f.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range general.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				operationWireFields(t, spec.Type)
				var out bytes.Buffer
				if err := format.Node(&out, fset, spec.Type); err != nil {
					t.Fatal(err)
				}
				result[spec.Name.Name] = out.String()
			case *ast.ValueSpec:
				if general.Tok != token.CONST {
					continue
				}
				for i, name := range spec.Names {
					if i >= len(spec.Values) {
						continue
					}
					var out bytes.Buffer
					if err := format.Node(&out, fset, spec.Values[i]); err != nil {
						t.Fatal(err)
					}
					result[name.Name] = out.String()
				}
			}
		}
	}
	return result
}

// Server readiness projections are explicitly absent from JSON. Compare the
// wire declarations while retaining every serialized field, type and tag.
func operationWireFields(t *testing.T, declaration ast.Expr) {
	t.Helper()
	ast.Inspect(declaration, func(node ast.Node) bool {
		structure, ok := node.(*ast.StructType)
		if !ok {
			return true
		}
		fields := structure.Fields.List[:0]
		for _, field := range structure.Fields.List {
			if field.Tag != nil {
				tag, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					t.Fatal(err)
				}
				if reflect.StructTag(tag).Get("json") == "-" {
					continue
				}
			}
			fields = append(fields, field)
		}
		structure.Fields.List = fields
		return true
	})
}

func TestOperationDeclarationsPreserveSerializedFieldDrift(t *testing.T) {
	baseline := "type Snapshot struct { Value string `json:\"value\"` }"
	for _, test := range []struct {
		name        string
		declaration string
		match       bool
	}{
		{"server projection", "type Snapshot struct { Hidden time.Time `json:\"-\"`; Value string `json:\"value\"` }", true},
		{"public type", "type Snapshot struct { Value int `json:\"value\"` }", false},
		{"public name", "type Snapshot struct { Other string `json:\"value\"` }", false},
		{"public tag", "type Snapshot struct { Value string `json:\"renamed\"` }", false},
		{"public options", "type Snapshot struct { Value string `json:\"value,omitempty\"` }", false},
		{"missing field", "type Snapshot struct {}", false},
		{"extra default field", "type Snapshot struct { Value string `json:\"value\"`; Hidden string }", false},
		{"empty tag", "type Snapshot struct { Value string `json:\"value\"`; Hidden string `json:\"\"` }", false},
		{"serialized dash", "type Snapshot struct { Value string `json:\"value\"`; Hidden string `json:\"-,omitempty\"` }", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name, declaration string) map[string]string {
				t.Helper()
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte("package api\n"+declaration), 0600); err != nil {
					t.Fatal(err)
				}
				return operationDeclarations(t, path)
			}
			want := write("baseline.go", baseline)
			got := write("candidate.go", test.declaration)
			if match := got["Snapshot"] == want["Snapshot"]; match != test.match {
				t.Fatalf("wire match = %v, want %v; got %s; want %s", match, test.match, got["Snapshot"], want["Snapshot"])
			}
		})
	}
}
