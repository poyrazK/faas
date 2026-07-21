package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestLintTripwire_NoBareOsOpenInCLI is the Go-test counterpart to the
// .golangci.yml forbidigo rule on `os.Open\(`. PR #101 closed the
// symlink-follow attack surface in `faas deploy --tarball` by routing
// every customer-supplied path through `openCustomerFile`
// (defined in `commands5.go`, package `main`). The lint rule enforces
// that — but lint rules can be silently disabled in a future PR
// ("just this once"). This test fails fast at `go test` time if
// anyone re-introduces a bare `os.Open(` anywhere in cmd/faas/
// outside the documented escape hatch in commands5.go.
//
// Tripwire contract:
//   - any `*ast.CallExpr` whose Function is `os.Open` in any non-test
//     .go file under this package is a violation
//   - the only allowed exception is inside commands5.go, where
//     `openCustomerFile` itself uses os.Open as the security boundary
//     (and is already annotated with `//nolint:forbidigo`)
//   - test files (*_test.go) are excluded because `writeMinimalFile`
//     uses os.Create — but never os.Open — and the test fixtures
//     should never reach the wire
//
// If a new caller legitimately needs os.Open on a customer path,
// route it through openCustomerFile. If it needs os.Open for a
// vetted / non-customer path, the call must live OUTSIDE cmd/faas/
// (e.g. in pkg/api or one of the daemons); the CLI never opens a
// path that is not customer-supplied.
func TestLintTripwire_NoBareOsOpenInCLI(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		// Walk every .go file except generated protobuf stubs and test
		// fixtures (test files use os.Create, never os.Open, and
		// tripwire-ing them would couple this test to fixture churn).
		name := fi.Name()
		if strings.HasSuffix(name, "_test.go") {
			return false
		}
		if strings.HasSuffix(name, ".pb.go") || strings.HasSuffix(name, "_grpc.pb.go") {
			return false
		}
		return true
	}, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse cmd/faas: %v", err)
	}

	var violations []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			fileName := fset.Position(file.Pos()).Filename
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if !isOsOpenCall(call) {
					return true
				}
				// Documented exception: openCustomerFile body in
				// cmd/faas/commands5.go. The line is annotated with
				// `//nolint:forbidigo` and is the security boundary
				// itself — pre-open + post-open Lstat discipline.
				if strings.HasSuffix(fileName, "commands5.go") {
					return true
				}
				pos := fset.Position(call.Pos())
				violations = append(violations, pos.String())
				return true
			})
		}
	}

	if len(violations) > 0 {
		// The path-in-help-text points maintainers at the helper file
		// and the rule annotation it carries, so the next reader can
		// find the right fix without grepping the codebase.
		t.Fatalf("found bare os.Open( outside openCustomerFile (cmd/faas/commands5.go) — see //nolint:forbidigo near that function for the documented exception:\n  %s\n\nroute customer-supplied paths through openCustomerFile; vetted-id paths must live in pkg/api or a daemon, not the CLI",
			strings.Join(violations, "\n  "))
	}
}

// isOsOpenCall reports whether call is `os.Open(...)` — i.e. the
// function is a SelectorExpr whose X is the package qualifier "os"
// and whose Sel.Name is "Open". Matches both `os.Open(f)` and
// method-style receiver calls if anyone ever writes one.
func isOsOpenCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Open" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return id.Name == "os"
}
