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
	for name, declaration := range operationDeclarations(t, filepath.Join(root, "operation_subject.go")) {
		want[name] = declaration
	}
	for name, declaration := range operationDeclarations(t, "operation_subject.go") {
		got[name] = declaration
	}
	for name, declaration := range operationDeclarations(t, filepath.Join(root, "operation_milestones.go")) {
		want[name] = declaration
	}
	for name, declaration := range operationDeclarations(t, "operation_milestones.go") {
		got[name] = declaration
	}
	for name, declaration := range operationDeclarations(t, filepath.Join(root, "operation_workflows.go")) {
		want[name] = declaration
	}
	for name, declaration := range operationDeclarations(t, "operation_workflows.go") {
		got[name] = declaration
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
