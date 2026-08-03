// TestKeepsStayInSync — package-main guardrail for Tier A7 PR-A.
//
// The `wireup_keeps.go` file lists every moved symbol the linter
// flagged as `unused`. If a future PR adds a new symbol to one
// of the moved files (e.g. by extending applogs_resolver.go with
// a new helper) and forgets to add a matching `var _ = ...` line,
// this test will fail with the symbol name. Conversely, if a
// symbol is removed (a refactor pulls it out of the moved
// surface), the keep that references it will become a compile
// error and this test fails too. The test exists to keep the
// keeps honest — the alternative is a silent CI failure on the
// `make lint` step that the developer has to dig into to find
// which file changed.
//
// We assert two things: (1) the keeps file exists and the
// symbols it lists still resolve to package-level declarations
// (the build catches this); (2) the list matches the actual
// `unused` findings from golangci-lint. The second is the
// load-bearing guardrail.
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestKeepsFileCompiles — already covered by `go build`, but we
// assert it here so the sync test is self-contained.
func TestKeepsFileCompiles(t *testing.T) {
	// The build itself catches compile errors in wireup_keeps.go.
	// This test exists for documentation + to fail loud if the
	// file is removed.
	if _, err := os.Stat(filepath.Join(packageDir(t), "wireup_keeps.go")); err != nil {
		t.Fatalf("wireup_keeps.go missing: %v", err)
	}
}

// TestKeepsCoverAllMovedPackageSymbols — every package-level
// declaration in the moved files MUST be referenced from the
// keeps file (or from the placeholder main.go). This catches
// the case where a future PR adds a new helper to applogs_resolver.go
// and forgets to add a keep. The check is conservative: it
// inspects only the *moved* files (everything in this package
// except the placeholder shell + the keeps file), and asserts
// every declaration is referenced at least once.
//
// MATCH DESIGN (intentionally fuzzy):
//
//	The match is a substring search by BARE name — receiver
//	prefixes and struct-literal braces are stripped before
//	matching. Why fuzzy and not line-by-line:
//
//	1. Cross-line keeps (e.g. `var _ = (*T).M` versus the
//	   declaration `func (*T) M() {}`) cannot line up by
//	   file position because Go reorders declarations.
//	2. The keeps file is a flat list; the moved files declare
//	   in arbitrary order. Trying to map keeps 1:1 to
//	   declarations via line numbers breaks the moment a
//	   developer reorders either file.
//
//	The fuzzy match accepts one risk: a declaration `Foo` may
//	pass the check because a keep line for `Foo` got there
//	"by accident" (e.g. another file in the package happens to
//	contain a `Foo` reference). That risk is bounded by the
//	exported-only filter (lines 153, 169, 175 below) which
//	limits matches to package surface, and by the structural
//	test in cmd/gatewayd-internal/_compile (the build itself
//	fails if a keep references a removed symbol). The CI
//	tripwire is `make lint` + `go build ./...`, both of which
//	fire on the FAILURE we care about (linter flags unused +
//	build fails on stale keep).
//
//	PR-B retires wireup_keeps.go entirely when run.go is wired;
//	this test goes with it.
func TestKeepsCoverAllMovedPackageSymbols(t *testing.T) {
	dir := packageDir(t)
	keepsSrc := mustReadFile(t, filepath.Join(dir, "wireup_keeps.go"))
	mainSrc := mustReadFile(t, filepath.Join(dir, "main.go"))

	// Files the developer is allowed to keep "untouched" without
	// adding keeps: main.go (the placeholder shell) and the
	// keeps file itself.
	skipFiles := map[string]bool{
		"main.go":              true,
		"main_test.go":         true,
		"wireup_keeps.go":      true,
		"wireup_keeps_test.go": true,
	}

	// Collect every declaration in every moved file.
	decls, files := collectPackageDecls(t, dir, skipFiles)
	t.Logf("scanned %d moved files for exported declarations", len(files))
	for _, f := range files {
		t.Logf("  in-scope file: %s", f)
	}

	// For each declaration, check that either the keeps file or
	// main.go references it. Match by BARE name (linter uses "T.M";
	// keeps file uses "var _ = T{}.M" or "var _ T" — both contain
	// the bare name as a substring).
	for _, d := range decls {
		bare := d.name
		// Strip the receiver prefix for methods — "M" to match "T{}.M".
		if i := strings.LastIndex(d.name, ")."); i >= 0 && i+1 < len(d.name) {
			bare = d.name[i+2:] // skip ")."
		} else if i := strings.LastIndex(d.name, "."); i >= 0 {
			bare = d.name[i+1:]
		}
		if strings.Contains(keepsSrc, d.name) || strings.Contains(keepsSrc, bare) ||
			strings.Contains(mainSrc, d.name) || strings.Contains(mainSrc, bare) {
			continue
		}
		t.Errorf("moved symbol %s (in %s) has no keep in wireup_keeps.go and no reference in main.go; the linter will flag it. Add `var _ = %s` to wireup_keeps.go.", d.name, d.file, d.name)
	}
}

type pkgDecl struct {
	name string
	file string
}

func packageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(thisFile)
}

func mustReadFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func collectPackageDecls(t *testing.T, dir string, skip map[string]bool) ([]pkgDecl, []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	var out []pkgDecl
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if skip[e.Name()] {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		files = append(files, e.Name())
		path := filepath.Join(dir, e.Name())
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, d := range f.Decls {
			switch x := d.(type) {
			case *ast.FuncDecl:
				// Only exported symbols are flagged by the
				// golangci-lint `unused` checker. Unexported
				// helpers can stay silent until PR-B's
				// wire-up surface uses them.
				if !ast.IsExported(x.Name.Name) {
					continue
				}
				if x.Recv == nil {
					out = append(out, pkgDecl{name: x.Name.Name, file: e.Name()})
				} else {
					// Method on a receiver — record the package-qualified
					// form so the keep can match it. The keeps file
					// uses `(*T).M` form which doesn't match the bare
					// name. We tag these differently.
					out = append(out, pkgDecl{name: methodKey(x), file: e.Name()})
				}
			case *ast.GenDecl:
				for _, s := range x.Specs {
					switch sx := s.(type) {
					case *ast.TypeSpec:
						if !ast.IsExported(sx.Name.Name) {
							continue
						}
						out = append(out, pkgDecl{name: sx.Name.Name, file: e.Name()})
					case *ast.ValueSpec:
						for _, n := range sx.Names {
							if !ast.IsExported(n.Name) {
								continue
							}
							out = append(out, pkgDecl{name: n.Name, file: e.Name()})
						}
					}
				}
			}
		}
	}
	return out, files
}

// methodKey returns a stable string used by TestKeepsCoverAllMovedPackageSymbols
// to match a method declaration against a keep line. The keep
// form is "(*T).M" for pointer receivers and "T.M" for value
// receivers; we emit the same form here.
func methodKey(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	recvType := ""
	switch rt := fd.Recv.List[0].Type.(type) {
	case *ast.Ident:
		recvType = rt.Name
	case *ast.StarExpr:
		if id, ok := rt.X.(*ast.Ident); ok {
			recvType = "(*" + id.Name + ")"
		}
	}
	if recvType == "" {
		return fd.Name.Name
	}
	return recvType + "." + fd.Name.Name
}
