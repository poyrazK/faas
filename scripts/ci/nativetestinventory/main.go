// Command nativetestinventory reads native Linux/amd64 test sources without
// compiling them or requiring KVM. go/build owns build-constraint evaluation.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode"
)

type nativeTest struct{ File, Name string }

func inventory(root, tags, files string) ([]nativeTest, error) {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH = "linux", "amd64"
	ctx.BuildTags = strings.FieldsFunc(tags, func(r rune) bool { return r == ',' })
	withoutMetal := ctx
	withoutMetal.BuildTags = slices.DeleteFunc(slices.Clone(ctx.BuildTags), func(tag string) bool { return tag == "metal" })
	dir := filepath.Join(root, "cmd", "e2e")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var result []nativeTest
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") || (files != "" && !slices.Contains(strings.Split(files, ","), name)) {
			continue
		}
		selected, err := ctx.MatchFile(dir, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		plain, err := withoutMetal.MatchFile(dir, name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if !selected || plain {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isTest(fn) {
				continue
			}
			result = append(result, nativeTest{name, fn.Name.Name})
		}
	}
	slices.SortFunc(result, func(a, b nativeTest) int { return strings.Compare(a.Name, b.Name) })
	for i := 1; i < len(result); i++ {
		if result[i-1].Name == result[i].Name {
			return nil, fmt.Errorf("duplicate native test %s", result[i].Name)
		}
	}
	return result, nil
}

func isTest(fn *ast.FuncDecl) bool {
	name := []rune(fn.Name.Name)
	if !strings.HasPrefix(fn.Name.Name, "Test") || (len(name) > 4 && unicode.IsLower(name[4])) {
		return false
	}
	if fn.Type.TypeParams != nil || (fn.Type.Results != nil && len(fn.Type.Results.List) != 0) || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	param := fn.Type.Params.List[0]
	star, ok := param.Type.(*ast.StarExpr)
	if !ok || len(param.Names) > 1 {
		return false
	}
	switch typ := star.X.(type) {
	case *ast.SelectorExpr:
		return typ.Sel.Name == "T"
	case *ast.Ident:
		return typ.Name == "T"
	}
	return false
}

// Every metal-bearing test must have an owner. Extra tag suites are deliberately
// separate from the general metal phases, and their strict runner contracts own
// their complete test sets. Unknown extra tags fail this check instead of hiding.
func checkCoverage(root string) error {
	var suites []struct{ Tag, Contract string }
	data, err := os.ReadFile(filepath.Join(root, "scripts/ci/binding-native-suites.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &suites); err != nil {
		return err
	}
	owned := map[string]bool{}
	general, err := inventory(root, "metal", "")
	if err != nil {
		return err
	}
	for _, test := range general {
		owned[test.File] = true
	}
	for _, suite := range suites {
		selected, err := inventory(root, "metal,"+suite.Tag, "")
		if err != nil {
			return err
		}
		var contract struct{ Root string }
		data, err := os.ReadFile(filepath.Join(root, suite.Contract))
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &contract); err != nil {
			return err
		}
		var extra []string
		for _, test := range selected {
			if !slices.Contains(general, test) {
				extra = append(extra, test.Name)
				owned[test.File] = true
			}
		}
		if len(extra) != 1 || extra[0] != contract.Root {
			return fmt.Errorf("%s runner contract %s does not cover selected tests %v", suite.Tag, contract.Root, extra)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "cmd/e2e/*_test.go"))
	if err != nil {
		return err
	}
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		metal := false
		for _, group := range parsed.Comments {
			for _, comment := range group.List {
				if constraint.IsGoBuild(comment.Text) || constraint.IsPlusBuild(comment.Text) {
					expr, err := constraint.Parse(comment.Text)
					if err != nil {
						return err
					}
					metal = metal || mentionsMetal(expr)
				}
			}
		}
		if !metal || owned[filepath.Base(file)] {
			continue
		}
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && isTest(fn) {
				return fmt.Errorf("native test file has no configured suite: %s", filepath.Base(file))
			}
		}
	}
	return nil
}

func mentionsMetal(expr constraint.Expr) bool {
	switch expr := expr.(type) {
	case *constraint.TagExpr:
		return expr.Tag == "metal"
	case *constraint.NotExpr:
		return mentionsMetal(expr.X)
	case *constraint.AndExpr:
		return mentionsMetal(expr.X) || mentionsMetal(expr.Y)
	case *constraint.OrExpr:
		return mentionsMetal(expr.X) || mentionsMetal(expr.Y)
	}
	return false
}

func main() {
	// grep -q intentionally closes its pipe after finding a requested test.
	// That does not invalidate the inventory we already computed successfully.
	signal.Ignore(syscall.SIGPIPE)
	root := flag.String("root", ".", "repository to inspect")
	tags := flag.String("tags", "metal", "enabled build tags")
	files := flag.String("files", "", "optional comma-separated source filenames")
	check := flag.Bool("check", false, "reject native tests missing from general or binding suite contracts")
	flag.Parse()
	var err error
	if *check {
		err = checkCoverage(*root)
	} else {
		var tests []nativeTest
		tests, err = inventory(*root, *tags, *files)
		if err == nil {
			var output strings.Builder
			for _, test := range tests {
				output.WriteString(test.Name + "\n")
			}
			if _, writeErr := os.Stdout.WriteString(output.String()); writeErr != nil && !errors.Is(writeErr, syscall.EPIPE) {
				err = writeErr
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "native test inventory:", err)
		os.Exit(1)
	}
}
