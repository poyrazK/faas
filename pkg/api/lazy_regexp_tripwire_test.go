package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Every daemon and every per-instance stream bridge imports this package. A
// package-level regexp compiles in each of them at startup whether or not it
// is used; 91 of them cost about 4 MB and 7 ms per process
// (ADR-20261101215228). Package-level patterns use lazyregexp.New, which
// compiles on first use.
func TestNoPackageLevelRegexpCompile(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			ast.Inspect(gd, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "regexp" && (sel.Sel.Name == "MustCompile" || sel.Sel.Name == "Compile") {
					t.Errorf("%s: package-level regexp.%s compiles in every importing process at startup; use lazyregexp.New",
						fset.Position(sel.Pos()), sel.Sel.Name)
				}
				return true
			})
		}
	}
}
