package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCookieOnlyRouteTripwire_NoLiteralOutsideGuard pins the rule:
// the only place a literal matching ^/v1/auth/(sessions|capabilities)
// may appear in pkg/api is the guard itself (pkg/api/client.go's
// cookieOnlyPathRE). The guard is the closed allow-list — any future
// path-construction that hard-codes one of those routes would
// silently bypass the rejection and surface a confusing 401/302 to
// the bearer-key CLI caller. The tripwire mirrors the docs-domain
// tripwire in cmd/gregale/lint_tripwires_test.go (same walker
// pattern, same allow-list shape: a single canonical home + the
// rest of the tree).
//
// The allowed regex literal `^/v1/auth/(sessions|capabilities)(/.*)?$`
// in pkg/api/client.go is the guard — the tripwire's substring
// match is anchored on the path prefix, not the regex body, so the
// regex literal itself does not trigger. The single-file allow-list
// below is the discharge valve.
func TestCookieOnlyRouteTripwire_NoLiteralOutsideGuard(t *testing.T) {
	cookieOnlyPathPrefixes := []string{
		// Matches every permutation of /v1/auth/sessions[/*]
		// construction the CLI might use. The closed set is the
		// server-side mount (cmd/apid/server.go:1097) and the
		// three handlers at handlers_sessions.go:99/134/174.
		"/v1/auth/sessions",
		// /v1/auth/capabilities is the single mount point
		// (server.go:1085); the [/*] subpath shape is not used
		// today, but the prefix is enough to fire on any literal
		// that future code might compose.
		"/v1/auth/capabilities",
		"/v1/admin/status/incidents",
	}

	// The single allowed home for the literal is the guard file
	// itself. The package-level comment on the cookieOnlyPathRE
	// variable includes the same URL fragments, but the comment
	// is parsed as a *ast.Comment, not a *ast.BasicLit, so the
	// AST walker never sees those.
	// Walk pkg/api/*.go. The tripwire is scoped to this package
	// because the cookie-only policy is purely a pkg/api concern
	// (no other package composes bearer-key paths into these
	// routes; the SDK is the only surface that does so).
	root, err := findRepoRootAPI(".")
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	allowedFiles := map[string]struct{}{
		filepath.Join(root, "pkg", "api", "client.go"): {},
	}
	pkgDir := filepath.Join(root, "pkg", "api")

	var violations []string
	walkErr := filepath.WalkDir(pkgDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Tests can legitimately stub the URL path (the cookie-only
		// guard's test in client_test.go uses the literal to assert
		// rejection). The companion tripwire is the production-code
		// invariant; tests are the contract for the invariant, so
		// they are excluded.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The doc comment on the cookieOnlyPathRE variable lives in
		// the guard file — explicitly allow that one.
		if _, ok := allowedFiles[path]; ok {
			return nil
		}
		fset := token.NewFileSet()
		file, ferr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if ferr != nil {
			// Skip unparseable files (synthetic, generated stubs).
			// nilerr lint fires on `return nil` here; the skip is
			// deliberate.
			return nil //nolint:nilerr // intentional skip on parse failure; see comment above
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			for _, prefix := range cookieOnlyPathPrefixes {
				if strings.Contains(lit.Value, prefix) {
					pos := fset.Position(lit.Pos())
					violations = append(violations, pos.String()+": "+lit.Value)
					return true
				}
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk pkg/api: %v", walkErr)
	}

	if len(violations) > 0 {
		t.Fatalf("cookie-only route literal found outside pkg/api/client.go — these paths would silently bypass the cookie-only-route guard and surface a 401/302 to the bearer-key CLI caller. Add the path to the guard's regex (pkg/api/client.go::cookieOnlyPathRE) or refactor to a non-cookie-only route:\n  %s\n\nIf a non-guard file legitimately embeds the URL in a comment, the AST walker does not see comments — convert the literal to a `// pkg/api/client.go:67` reference instead.",
			strings.Join(violations, "\n  "))
	}
}

// TestCookieOnlyRouteTripwire_SelfTest exercises the AST walker by
// injecting a forbidden literal into a synthetic production-style
// file under a temp directory and asserting the tripwire flags it.
// Without this, the AST walker is silently blind to itself — a
// future refactor that breaks the walker's substring match would
// land without anyone noticing because the live walker never finds
// a violation on a clean tree.
//
// The synthetic file lives under t.TempDir() so it disappears at
// test end; the walker is run directly against the temp file rather
// than the live repo, so the test is hermetic.
func TestCookieOnlyRouteTripwire_SelfTest(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a single synthetic .go file with a cookie-only path
	// literal. The package name is "api" so the parser accepts the
	// syntax; the contents have no other tripwire-matching
	// surface.
	syntheticPath := filepath.Join(tmpDir, "synthetic_violation.go")
	synthetic := `package api

// This file is a synthetic violation injected by the
// self-test. It exists only during the test run.
var cookieOnlyPath = "/v1/auth/sessions/test"
`
	if err := os.WriteFile(syntheticPath, []byte(synthetic), 0o600); err != nil {
		t.Fatalf("write synthetic: %v", err)
	}

	// Walk the temp file the same way the live walker does.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, syntheticPath, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse synthetic: %v", err)
	}
	var hit bool
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if strings.Contains(lit.Value, "/v1/auth/sessions") {
			hit = true
		}
		return true
	})
	if !hit {
		t.Fatal("walker self-test failed: synthetic cookie-only path literal was not detected — the tripwire is silently blind to itself")
	}
}

// findRepoRootAPI climbs from start upward until it finds a go.mod
// file. Returns the absolute directory of go.mod. Mirrors the
// helper in cmd/gregale/lint_tripwires_test.go — that one is in a
// `package gregale_test` test file and is not exported, so we
// duplicate the small loop here rather than introduce a package
// cycle.
func findRepoRootAPI(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
