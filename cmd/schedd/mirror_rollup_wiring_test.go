// adr: 133
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Mirror maintenance only needs Postgres. Turning off the optional gateway
// metrics scraper must not also turn off summary recovery and raw retention.
func TestMirrorRollupIndependentOfMetricsScraper(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, statement := range fn.Body.List {
			goCall, ok := statement.(*ast.GoStmt)
			if !ok {
				continue
			}
			selector, ok := goCall.Call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "RollupLoop" {
				continue
			}
			if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "mirrorRollup" {
				return // Direct startup statement, not nested in scraper != nil.
			}
		}
	}
	t.Fatal("mirror rollup must start unconditionally, independent of the gateway scraper")
}
