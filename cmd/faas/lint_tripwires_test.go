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

// TestLintTripwire_NoGlyphLiteralOutsideOutput closes the UX §3.2 surface
// the §3.2 PR opened: the leading-glyph rule is enforced by a writer-based
// gate (output.go::PrintOK/PrintFail/PrintProgress/PrintWarn) so the
// glyph disappears in pipes and under NO_COLOR. Any new code path that
// prints a raw ✓/✗/→/! string literal in cmd/faas/ — outside output.go
// and outside *_test.go — would bypass the gate, so this test fails fast
// at `go test` time the moment someone copies an old `fmt.Println("✓ …")`
// pattern into a new file.
//
// Excludes:
//   - cmd/faas/output.go: the gate itself. By design carries all four
//     glyphs as string literals.
//   - cmd/faas/output_test.go and any other *_test.go: tests legitimately
//     assert "glyph present" / "glyph absent" shapes, plus §3.3's static
//     Error() contract test which always carries "→".
//   - Comments: BasicLits in source comments aren't part of the AST
//     token stream, so they're naturally excluded.
//
// Two intentional exceptions worth knowing:
//   - commands5.go:504: `"Renamed %s → %s"` keeps the mid-string `→`
//     (a semantic from-to, not a progress glyph — preserved per the
//     §3.2 plan, follow-up to clean up separately).
//   - commands2.go:315: `"Opening %s to bind %s → %s"` — same shape,
//     semantic mid-string `→` for "bind X → Y". Not a progress glyph.
//
// Both literals are not leading-prefix glyphs so they wouldn't be matched
// by the simple "starts with" rule below; they're listed here so a future
// reviewer who sees a "should this be excluded?" question has the answer
// in-tree.
func TestLintTripwire_NoGlyphLiteralOutsideOutput(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
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

	// Leading-glyph strings we care about. The check is "starts with the
	// glyph" because the migration spec is "leading prefix only" — mid-string
	// `→` (semantic from-to notation) is explicitly preserved. A more
	// aggressive "any occurrence" rule would over-trigger on legitimate
	// cross-references and the §3.3 docs-URL line.
	leadingGlyphs := []string{"✓", "✗", "→"}

	var violations []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			fileName := fset.Position(file.Pos()).Filename
			if strings.HasSuffix(fileName, "output.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v := lit.Value
				for _, g := range leadingGlyphs {
					// strconv.UnquoteChar would be more precise, but a
					// leading-prefix check on the raw literal (including
					// its opening quote) is enough for the patterns this
					// PR introduces: `"✓ ", "✗ ", "→ ` (single byte UTF-8).
					if strings.HasPrefix(v, "\""+g) || strings.HasPrefix(v, "`"+g) {
						pos := fset.Position(lit.Pos())
						violations = append(violations, pos.String()+": "+v)
						break
					}
				}
				return true
			})
		}
	}

	if len(violations) > 0 {
		t.Fatalf("found leading ✓/✗/→ string literal outside output.go — gate every customer-facing line through PrintOK/PrintFail/PrintProgress/PrintWarn so it strips in pipes and under NO_COLOR:\n  %s\n\n(mid-string `→` is allowed; this rule matches leading prefix only. Add `// lint:allow-glyph` above the line and document the reason if you genuinely need an exception.)",
			strings.Join(violations, "\n  "))
	}
}
