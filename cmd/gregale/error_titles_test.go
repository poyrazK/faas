package main_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// An error title names what failed ("Could not list deployments"); a title
// such as "Request failed" leaves the user guessing which call broke.
func TestErrorTitlesNameTheOperation(t *testing.T) {
	vague := map[string]bool{
		"Request failed": true, "Failed": true, "Error": true,
		"Create failed": true, "Update failed": true, "Delete failed": true,
	}
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range packages["main"].Files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			if name, ok := call.Fun.(*ast.Ident); !ok || name.Name != "printErr" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			if title, err := strconv.Unquote(literal.Value); err == nil && vague[title] {
				t.Errorf("%s: error title %q does not say what failed", fset.Position(call.Pos()), title)
			}
			return true
		})
	}
}
