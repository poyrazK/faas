package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEdgeStoreContractListsEveryStoreAssertion: a type assertion on the
// edge store to a state or objectstorage interface compiles even when the
// store lacks the methods, and then fails at start (rc.287, 2026-10-21).
// Every such assertion in this daemon and in the object-storage handlers it
// builds must name an interface edge_store_contract.go requires.
func TestEdgeStoreContractListsEveryStoreAssertion(t *testing.T) {
	contract, err := os.ReadFile("edge_store_contract.go")
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	handlers, err := filepath.Glob(filepath.Join("..", "..", "pkg", "objectstorage", "upload*.go"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "..", "pkg", "objectstorage", "public_read.go"))
	files = append(files, handlers...)
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			assert, ok := n.(*ast.TypeAssertExpr)
			if !ok || assert.Type == nil {
				return true
			}
			sel, ok := assert.Type.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || (pkg.Name != "state" && pkg.Name != "objectstorage") {
				return true
			}
			checked++
			name := pkg.Name + "." + sel.Sel.Name
			if !strings.Contains(string(contract), name+" ") && !strings.Contains(string(contract), name+"\t") {
				t.Errorf("%s asserts a store to %s, which edge_store_contract.go does not require of *state.EdgeStore", file, name)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("found no store assertions to check; the scan is broken")
	}
}
