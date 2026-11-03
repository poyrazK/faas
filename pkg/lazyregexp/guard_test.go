package lazyregexp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guarded are the packages every daemon and every per-instance stream bridge
// imports. A package-level regexp in them compiles in each of those processes
// at startup whether or not it is used: pkg/api's 91 cost about 4 MB and 7 ms
// per process, pkg/state's 57 about 1.5 MB (ADR-20261101215228). They use New.
var guarded = []string{"../api", "../state"}

func TestGuardedPackagesHaveNoPackageLevelRegexpCompile(t *testing.T) {
	fset := token.NewFileSet()
	for _, dir := range guarded {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
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
						return false // compiled when the func runs
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
}
