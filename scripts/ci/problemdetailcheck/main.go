// Command problemdetailcheck keeps raw error text out of 5xx problem
// responses. A 500 or 503 detail built from an error hands the caller
// whatever the failing layer wrote: SQL fragments and constraint names from
// Postgres, internal hostnames and socket paths from a dial, file paths and
// decoder positions. 4xx validation errors that echo the caller's own input
// are fine and are not checked.
//
// It flags, in non-test Go files under pkg/ and cmd/:
//
//   - api.ErrInternal, api.ErrCapacity and the other 5xx constructors in
//     pkg/api whose detail argument mentions an error;
//   - api.NewProblem with a 5xx status whose detail argument does.
//
// An argument mentions an error when it calls .Error() or names a variable
// called err, errSomething or somethingErr. Log the error server-side and
// give the caller a fixed detail instead. A line that must keep it says so
// with a `problemdetail:allow <reason>` comment on that line or the one
// above.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const apiImport = "github.com/onebox-faas/faas/pkg/api"

// detailArg is the index of the detail argument of each pkg/api
// constructor that answers 5xx with caller-supplied text.
var detailArg = map[string]int{
	"ErrInternal":                   0,
	"ErrCapacity":                   0,
	"ErrDebugRegressionUnavailable": 0,
	"ErrSourceRefUnavailable":       0,
	"ErrEdgeRuleMaintenance":        1,
	"ErrDoctorUnavailable":          1,
}

var serverStatuses = map[string]bool{
	"StatusInternalServerError":           true,
	"StatusNotImplemented":                true,
	"StatusBadGateway":                    true,
	"StatusServiceUnavailable":            true,
	"StatusGatewayTimeout":                true,
	"StatusHTTPVersionNotSupported":       true,
	"StatusVariantAlsoNegotiates":         true,
	"StatusInsufficientStorage":           true,
	"StatusLoopDetected":                  true,
	"StatusNotExtended":                   true,
	"StatusNetworkAuthenticationRequired": true,
}

var errName = regexp.MustCompile(`^err$|^err[A-Z0-9_]|Err$`)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	findings, err := scan(*root, []string{"pkg", "cmd"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "problemdetailcheck:", err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, "problemdetailcheck:", f)
	}
	if len(findings) > 0 {
		fmt.Fprintln(os.Stderr, "problemdetailcheck: a 5xx problem detail must not carry error text; log the error and return a fixed detail")
		os.Exit(1)
	}
}

func scan(root string, dirs []string) ([]string, error) {
	var out []string
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			found, err := checkFile(p, filepath.ToSlash(rel))
			if err != nil {
				return err
			}
			out = append(out, found...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func checkFile(path, rel string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	return check(fset, f, rel), nil
}

// check returns the findings in one parsed file, as "path:line: reason".
func check(fset *token.FileSet, f *ast.File, rel string) []string {
	apiName := ""
	if f.Name.Name == "api" && strings.HasPrefix(rel, "pkg/api/") {
		apiName = "."
	}
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == apiImport {
			apiName = "api"
			if imp.Name != nil {
				apiName = imp.Name.Name
			}
		}
	}
	if apiName == "" {
		return nil
	}
	allowed := allowedLines(fset, f)
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := apiFunc(call.Fun, apiName)
		var detail ast.Expr
		switch {
		case name == "NewProblem" && len(call.Args) == 4 && isServerStatus(call.Args[0]):
			detail = call.Args[3]
		case name != "":
			if i, ok := detailArg[name]; ok && i < len(call.Args) {
				detail = call.Args[i]
			}
		}
		if detail == nil || !mentionsError(detail) {
			return true
		}
		line := fset.Position(call.Pos()).Line
		if allowed[line] || allowed[line-1] {
			return true
		}
		out = append(out, fmt.Sprintf("%s:%d: %s detail carries error text", rel, line, name))
		return true
	})
	return out
}

// apiFunc names the pkg/api function fun calls, or "".
func apiFunc(fun ast.Expr, apiName string) string {
	switch fn := fun.(type) {
	case *ast.SelectorExpr:
		if id, ok := fn.X.(*ast.Ident); ok && id.Name == apiName {
			return fn.Sel.Name
		}
	case *ast.Ident:
		if apiName == "." {
			return fn.Name
		}
	}
	return ""
}

func isServerStatus(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok && id.Name == "http" {
			return serverStatuses[v.Sel.Name]
		}
	case *ast.BasicLit:
		n, err := strconv.Atoi(v.Value)
		return err == nil && n >= 500 && n <= 599
	}
	return false
}

func mentionsError(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(v.Args) == 0 {
				found = true
			}
		case *ast.Ident:
			if errName.MatchString(v.Name) {
				found = true
			}
		}
		return !found
	})
	return found
}

func allowedLines(fset *token.FileSet, f *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, g := range f.Comments {
		for _, c := range g.List {
			if strings.Contains(c.Text, "problemdetail:allow ") {
				out[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return out
}
