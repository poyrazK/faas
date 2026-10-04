package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Prod hunt #3: splitArgsForFlags needs every bool flag named, or a bool flag
// swallows the positional after it (`triggers delete --quiet <id>` passed the
// id to --quiet), and naming a value flag strands its value as a positional.
// The lists are hand-kept at ~120 call sites, so check each one against the
// flags its own function declares.
func TestSplitArgsForFlagsNamesExactlyTheBoolFlags(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var problems []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			problems = append(problems, checkSplitArgsCalls(fset, fn)...)
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

func checkSplitArgsCalls(fset *token.FileSet, fn *ast.FuncDecl) []string {
	declared := map[string]bool{} // flag name -> is bool
	var calls []*ast.CallExpr
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if fun.Name == "splitArgsForFlags" {
				calls = append(calls, call)
			}
		case *ast.SelectorExpr:
			nameArg := 0
			switch fun.Sel.Name {
			case "Bool", "String", "Int", "Int64", "Uint", "Uint64", "Float64", "Duration", "Func":
			case "BoolVar", "StringVar", "IntVar", "Int64Var", "UintVar", "Uint64Var", "Float64Var", "DurationVar", "Var":
				nameArg = 1
			default:
				return true
			}
			if len(call.Args) <= nameArg {
				return true
			}
			lit, ok := call.Args[nameArg].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			flagName, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			declared[flagName] = fun.Sel.Name == "Bool" || fun.Sel.Name == "BoolVar"
		}
		return true
	})
	var problems []string
	for _, call := range calls {
		named := map[string]bool{}
		for _, arg := range call.Args[1:] {
			lit, ok := arg.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			v, err := strconv.Unquote(lit.Value)
			if err == nil {
				named[v] = true
			}
		}
		pos := fset.Position(call.Pos())
		where := pos.Filename + ":" + strconv.Itoa(pos.Line) + " (" + fn.Name.Name + ")"
		for flagName, isBool := range declared {
			if isBool && !named[flagName] {
				problems = append(problems, where+": bool flag --"+flagName+" is not named, so it swallows the next positional")
			}
		}
		for flagName := range named {
			if isBool, ok := declared[flagName]; ok && !isBool {
				problems = append(problems, where+": --"+flagName+" takes a value but is named as a bool, so its value becomes a positional")
			}
		}
	}
	return problems
}
