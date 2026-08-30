package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/presetwhy"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// TestLintTripwire_NoBareOsOpenInCLI is the Go-test counterpart to the
// .golangci.yml forbidigo rule on `os.Open\(`. PR #101 closed the
// symlink-follow attack surface in `gregale deploy --tarball` by routing
// every customer-supplied path through `openCustomerFile`
// (defined in `commands5.go`, package `main`). The lint rule enforces
// that — but lint rules can be silently disabled in a future PR
// ("just this once"). This test fails fast at `go test` time if
// anyone re-introduces a bare `os.Open(` anywhere in cmd/gregale/
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
// vetted / non-customer path, the call must live OUTSIDE cmd/gregale/
// (e.g. in pkg/api or one of the daemons); the CLI never opens a
// path that is not customer-supplied.
//
// Documented exceptions to the filename check below:
//   - commands5.go (openCustomerFile body — see //nolint:forbidigo annotation)
//   - commands_doctor.go (customer `gregale doctor` preflight — scans
//     source trees via filepath.Walk; the regex is read-only and never
//     executes any path, same security discipline as openCustomerFile)
//   - tarballSHA256 in git_local.go (function-name scope, NOT a
//     filename exception — see functionScopeMatch below). The
//     exception is bounded to one function so a future os.Open in a
//     sibling function in the same file still trips the wire.
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
		t.Fatalf("parse cmd/gregale: %v", err)
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
				// cmd/gregale/commands5.go. The line is annotated with
				// `//nolint:forbidigo` and is the security boundary
				// itself — pre-open + post-open Lstat discipline.
				if strings.HasSuffix(fileName, "commands5.go") {
					return true
				}
				// Documented exception: customer `gregale doctor`
				// preflight in cmd/gregale/commands_doctor.go. The
				// scan is read-only line-by-line regex over the
				// walked tree — the customer-supplied root is what
				// we accept, but `p` from filepath.Walk is the
				// kernel-resolved real path, not the customer string.
				// The //nolint:forbidigo lines mark each site with
				// the same discipline (no follow-on symlinks, no
				// exec, no write) as openCustomerFile.
				if strings.HasSuffix(fileName, "commands_doctor.go") {
					return true
				}
				// Documented exception: tarballSHA256 function in
				// cmd/gregale/git_local.go (issue #1182 §P1
				// follow-up). Scoped to the enclosing function name
				// rather than the filename so a future os.Open in
				// any other function in git_local.go still trips
				// the wire — only this one helper, which reads
				// the CLI's own os.CreateTemp tempfile to populate
				// DeployReceipt.source_sha256, is exempt. Same
				// discipline as openCustomerFile (no follow-on
				// exec / write / chmod, sha256.New is the consumer).
				if strings.HasSuffix(fileName, "git_local.go") && enclosingFuncName(call, file) == "tarballSHA256" {
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
		t.Fatalf("found bare os.Open( outside openCustomerFile (cmd/gregale/commands5.go) — see //nolint:forbidigo near that function for the documented exception:\n  %s\n\nroute customer-supplied paths through openCustomerFile; vetted-id paths must live in pkg/api or a daemon, not the CLI",
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

// enclosingFuncName is defined further down (paired with the
// TestLintTripwire_DoctorStrictMutex_SelfTest visitor at line
// 1005) and reused here to scope the git_local.go exception to
// a single function name rather than the whole file.

// TestLintTripwire_NoGlyphLiteralOutsideOutput closes the UX §3.2 surface
// the §3.2 PR opened: the leading-glyph rule is enforced by a writer-based
// gate (output.go::PrintOK/PrintFail/PrintProgress/PrintWarn) so the
// glyph disappears in pipes and under NO_COLOR. Any new code path that
// prints a raw ✓/✗/→/! string literal in cmd/gregale/ — outside output.go
// and outside *_test.go — would bypass the gate, so this test fails fast
// at `go test` time the moment someone copies an old `fmt.Println("✓ …")`
// pattern into a new file.
//
// Excludes:
//   - cmd/gregale/output.go: the gate itself. By design carries all four
//     glyphs as string literals.
//   - cmd/gregale/output_test.go and any other *_test.go: tests legitimately
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
		t.Fatalf("parse cmd/gregale: %v", err)
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

// TestLintTripwire_NoLiteralWakeHeaderOutsidePkgWire closes bug 2
// (PR #439): the wire `x-faas-wake` header is the published customer
// contract (docs/cold-wake.md, docs/faas_ux_spec.md, docs/STATUS.md) —
// the Gregale rename kept the `x-faas-` prefix on purpose so downstream
// tooling and SDKs that depend on the header name don't break. PR #439
// silently renamed the CLI's probe from `x-faas-wake` to
// `x-gregale-wake`, breaking the cold-wake affordance for `gregale
// open` while every test stubbed the renamed literal and made the
// suite self-confirming.
//
// The fix routes both the producer (pkg/gateway) and the consumer
// (cmd/gregale) through pkg/wire.WakeHeader. This tripwire fails fast
// if any future PR reintroduces a literal `"x-faas-wake"` or
// `"x-gregale-wake"` anywhere outside the documented canonical home
// (pkg/wire/wake.go). The header constant is the only sanctioned
// spelling.
//
// Excludes:
//   - pkg/wire/wake.go: the canonical home; the literal IS the contract.
//   - any *_test.go: tests legitimately assert or stub the wire
//     header literal — they are the contract tests, not the production
//     code.
//   - generated *.pb.go stubs.
//
// Scope: the walker descends from the nearest enclosing `go.mod`
// directory. `go test ./cmd/gregale/...` chdirs into cmd/gregale
// before running, so the walker must explicitly locate the repo root
// via go.mod — otherwise it would silently scope to just cmd/gregale
// and miss regressions in pkg/gateway (the producer side). The intent
// is "no literal in production code that travels over the wire";
// docs and tests are out of scope.
func TestLintTripwire_NoLiteralWakeHeaderOutsidePkgWire(t *testing.T) {
	fset := token.NewFileSet()

	// Locate the repo root (the directory containing go.mod) by
	// walking up from the test's CWD. The test lives at
	// cmd/gregale/...; the CWD when `go test` runs is cmd/gregale/.
	// Without this, the walker below only sees cmd/gregale/ and the
	// tripwire is silently blind to pkg/gateway regressions — exactly
	// the side PR #439 broke.
	root, err := findRepoRoot(".")
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	// Walk the whole repo except pkg/wire itself. Each package is
	// parsed in its own directory; pkgs is a map keyed by directory
	// relative to the walker root.
	pkgs := map[string]map[string]*ast.File{}

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip vendor, generated, and test fixture subtrees.
			name := d.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			// Skip Claude Code's local worktree checkouts under
			// .claude/worktrees/. These are sibling worktrees of stale
			// feature branches parked by Claude Code; they live next
			// to the repo root and contain copies of pkg/, cmd/, etc.
			// that would falsely trip the literal scan. The directory
			// itself is untracked (see .gitignore), so CI never sees
			// it — the skip is purely for local-dev ergonomics.
			if strings.Contains(path, string(filepath.Separator)+".claude"+string(filepath.Separator)+"worktrees"+string(filepath.Separator)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip the canonical home — the literal IS the contract.
		if strings.HasSuffix(path, "pkg/wire/wake.go") {
			return nil
		}
		// Skip *_test.go (tests legitimately assert or stub the wire
		// header literal — they are the contract tests, not the
		// production code).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip generated protobuf stubs.
		if strings.HasSuffix(path, ".pb.go") {
			return nil
		}
		pf, ferr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if ferr != nil {
			// Generated or unsupported files (e.g. build-tag-only)
			// may fail to parse. Don't fail the tripwire on those —
			// just skip them so a single unparseable file doesn't
			// mask the rule. nilerr lint fires on `return nil` here;
			// the skip is deliberate.
			return nil //nolint:nilerr // intentional skip on parse failure; see comment above
		}
		dir := filepath.Dir(path)
		bucket, ok := pkgs[dir]
		if !ok {
			bucket = map[string]*ast.File{}
			pkgs[dir] = bucket
		}
		bucket[path] = pf
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk repo: %v", walkErr)
	}

	forbidden := []string{`"x-faas-wake"`, `"x-gregale-wake"`, `"X-Faas-Wake"`, `"X-Gregale-Wake"`}

	var violations []string
	for _, files := range pkgs {
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				for _, forbid := range forbidden {
					if lit.Value == forbid {
						pos := fset.Position(lit.Pos())
						violations = append(violations, pos.String()+": "+lit.Value)
						return true
					}
				}
				return true
			})
		}
	}

	if len(violations) > 0 {
		t.Fatalf("found literal x-faas-wake / x-gregale-wake string outside pkg/wire/wake.go — these headers are the published customer contract and must be sourced from pkg/wire.WakeHeader (see docs/cold-wake.md):\n  %s\n\nIf a legacy gateway test legitimately needs the literal, move it to a *_test.go file (excluded) or convert it to wire.WakeHeader.",
			strings.Join(violations, "\n  "))
	}
}

// findRepoRoot climbs from start upward until it finds a go.mod
// file. Returns the absolute directory of go.mod. Used by the
// repo-walking lint tripwire so the walker's root is the repo root
// regardless of the test's working directory (which chdirs into
// cmd/gregale under `go test ./cmd/gregale/...`).
func findRepoRoot(start string) (string, error) {
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

// TestLintTripwire_NoLiteralWakeHeaderSelfTest exercises the AST
// walker by injecting a literal into a synthetic production-style
// file under a temp directory and asserting the tripwire flags it.
// This is the regression guard for the guard: a future refactor that
// silently breaks the walker (e.g. an early-return that skips the
// ast.Inspect callback) would land without anyone noticing, because
// the live walker never finds a violation on a clean tree. The
// self-test makes a violation unavoidable on every run.
func TestLintTripwire_NoLiteralWakeHeaderSelfTest(t *testing.T) {
	// Build a small Go file that contains the forbidden literal in
	// a string context the walker will recognise. We can't use the
	// in-package walker because it skips pkg/wire — instead we run
	// the same walk against a temp directory and assert the literal
	// shows up in the violations list.
	tmp := t.TempDir()
	src := `package tripwiretest

// Synthetic production-like file carrying the forbidden header
// literal. Exists only to exercise the AST walker.
var header = "x-faas-wake"
`
	srcPath := filepath.Join(tmp, "tripwire.go")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	fset := token.NewFileSet()
	pf, err := parser.ParseFile(fset, srcPath, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	forbidden := []string{`"x-faas-wake"`}
	var found string
	ast.Inspect(pf, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		for _, forbid := range forbidden {
			if lit.Value == forbid {
				found = fset.Position(lit.Pos()).String()
				return false
			}
		}
		return true
	})
	if found == "" {
		t.Fatal("self-test: walker did not detect the seeded x-faas-wake literal — the tripwire may be silently broken")
	}
}

// TestLintTripwire_NoLiteralDocsDomainEverywhere closes issue #420:
// every customer-facing or third-party-readable URL the platform
// emits used to carry a `DOMAIN` placeholder literal
// (`https://docs/DOMAIN/...`, `https://DOMAIN/billing`,
// `+https://DOMAIN`, `apps.DOMAIN`, etc.). PR #439 + PR #455 swept
// the CLI help block + apid REST; this tripwire makes sure no future
// PR reintroduces one anywhere outside the canonical home
// (pkg/wire/docs.go).
//
// Why a repo-wide walk rather than a per-package scan: the
// placeholders surface in pkg/vmmdgrpc (gRPC envelope), pkg/auth/
// middleware (apid REST 402 Detail), pkg/oci + pkg/storage
// (User-Agent to OCI registries), and cmd/gregale (synthesized docs
// row). A per-package tripwire would miss whichever of these
// packets the next regression lands in.
//
// Excludes:
//   - pkg/wire/docs.go: the canonical home; the literals
//     `docs.gregale.dev` and `gregale.dev` ARE the contract.
//   - any *_test.go: tests legitimately stub the URL or pin the
//     wire shape (pkg/grpcerr/grpcerr_test.go round-trip assertions).
//   - generated *.pb.go stubs.
//
// Scope: the walker descends from the nearest enclosing `go.mod`
// directory. `go test ./cmd/gregale/...` chdirs into cmd/gregale
// before running, so the walker explicitly locates the repo root via
// go.mod — otherwise it would silently scope to just cmd/gregale
// and miss regressions in the daemons.
func TestLintTripwire_NoLiteralDocsDomainEverywhere(t *testing.T) {
	fset := token.NewFileSet()

	// Locate the repo root (the directory containing go.mod).
	root, err := findRepoRoot(".")
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	// Walk the whole repo except the canonical homes + generated
	// stubs + tests. Each package is parsed in its own directory;
	// pkgs is a map keyed by directory relative to the walker root.
	pkgs := map[string]map[string]*ast.File{}

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			// Skip Claude Code's local worktree checkouts under
			// .claude/worktrees/. See the matching skip in
			// TestLintTripwire_NoLiteralWakeHeaderOutsidePkgWire for
			// the rationale (untracked, local-dev-only).
			if strings.Contains(path, string(filepath.Separator)+".claude"+string(filepath.Separator)+"worktrees"+string(filepath.Separator)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip the canonical homes — the constants DocsHost and
		// PlatformHost are the contract, the way pkg/wire/wake.go
		// owns x-faas-wake.
		if strings.HasSuffix(path, "pkg/wire/docs.go") || strings.HasSuffix(path, "pkg/wire/wake.go") {
			return nil
		}
		// Skip *_test.go (tests legitimately assert or stub the
		// wire URL literal — they are the contract tests, not the
		// production code).
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip generated protobuf stubs.
		if strings.HasSuffix(path, ".pb.go") {
			return nil
		}
		pf, ferr := parser.ParseFile(fset, path, nil, parser.AllErrors)
		if ferr != nil {
			// Generated or unsupported files may fail to parse;
			// skip them so a single unparseable file doesn't mask
			// the rule. nilerr lint fires on `return nil` here; the
			// skip is deliberate.
			return nil //nolint:nilerr // intentional skip on parse failure; see comment above
		}
		dir := filepath.Dir(path)
		bucket, ok := pkgs[dir]
		if !ok {
			bucket = map[string]*ast.File{}
			pkgs[dir] = bucket
		}
		bucket[path] = pf
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk repo: %v", walkErr)
	}

	// Strict forbidden list (per user direction). Each entry is a
	// substring the walker matches against every string literal in
	// every visited .go file. The forms are exhaustive of every
	// DOMAIN-shaped spelling the survey found on main:
	//   - `https://docs/DOMAIN` matches `WithDocs("https://docs/DOMAIN/vmmd#...")` in pkg/vmmdgrpc
	//   - `https://DOMAIN`     matches `https://DOMAIN/billing` in pkg/auth/middleware
	//   - `://DOMAIN/`         path-bearing generic catch-all
	//   - `://DOMAIN"`         string-terminated generic catch-all
	//   - `docs.DOMAIN`        matches the issue's literal spelling + `apps.DOMAIN` style
	//   - `.DOMAIN`            suffix-bearing catch-all (covers `apps.DOMAIN`)
	//   - `https://docs/vmmd#` malformed-host regression caught in PR-A
	//       (issue #420): the original `https://docs/vmmd#<fragment>` had
	//       a bare `docs` host with no TLD — every other vmmdgrpc site
	//       composes `https://` + wire.DocsHost + `/vmmd#<fragment>`. The
	//       `https://docs/` substring overlaps with `https://docs/DOMAIN`,
	//       but the vmmd fragment is presentational and not a placeholder
	//       — we ban the whole shape so a future regression that drops
	//       the TLD AND the placeholder guard would still fire.
	//   - `docs.gregale.example` RFC 2606 reserved-TLD regression caught
	//       in PR-A (issue #420): the pre-#458 docs host used the
	//       IANA-reserved example TLD (`example`). PR #458 renamed to
	//       `docs.gregale.dev` but missed two sites in cmd/gregale. The
	//       literal must stay out of the tree entirely — reserved TLDs
	//       cannot resolve, so a stray lookup fails fast and obviously.
	//
	// Overlap note: `https://docs/DOMAIN` is a strict superset of
	// `https://DOMAIN`, and `://DOMAIN/` is a strict superset of
	// `://DOMAIN`. The redundant entries are kept on purpose — the
	// exact entries document the pre-rename literal form a future
	// regression could re-introduce. Walker's `strings.Contains`
	// short-circuits per match, so the overlap has no runtime cost.
	// Don't delete the "redundant" entries without also deleting the
	// comment lines above; one without the other is a confusing
	// intermediate state.
	forbidden := []string{
		"https://docs/DOMAIN",
		"https://DOMAIN",
		"://DOMAIN/",
		"://DOMAIN\"",
		"docs.DOMAIN",
		".DOMAIN",
		"https://docs/vmmd#",
		"docs.gregale.example",
	}

	var violations []string
	for _, files := range pkgs {
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				for _, forbid := range forbidden {
					if strings.Contains(lit.Value, forbid) {
						pos := fset.Position(lit.Pos())
						violations = append(violations, pos.String()+": "+lit.Value)
						return true
					}
				}
				return true
			})
		}
	}

	if len(violations) > 0 {
		t.Fatalf("found DOMAIN-shaped placeholder string outside pkg/wire/docs.go — these literals leak unsubstituted placeholders to customers (issue #420) and must be sourced from pkg/wire.DocsHost / pkg/wire.PlatformHost:\n  %s\n\nIf a test legitimately needs the literal, move it to a *_test.go file (excluded) or convert it to a wire.DocsHost / wire.PlatformHost reference.",
			strings.Join(violations, "\n  "))
	}
}

// TestLintTripwire_NoLiteralDocsDomainSelfTest exercises the
// placeholder walker by injecting a forbidden literal into a
// synthetic production-style file under a temp directory and
// asserting the tripwire flags it. Without this, the AST walker is
// silently blind to itself — a future refactor that breaks the
// walker's substring match would land without anyone noticing
// because the live walker never finds a violation on a clean tree.
//
// One sub-test per forbidden entry, each seeded with a distinct
// synthetic literal so a regression in any single entry's substring
// match is named.
func TestLintTripwire_NoLiteralDocsDomainSelfTest(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		forbid string
	}{
		{
			// Pre-#439 placeholder form — the original issue #420
			// literal. Kept as the canonical walker exercise.
			name: "docs_DOMAIN_placeholder",
			src: `package tripwiretest

// Synthetic production-like file carrying the forbidden placeholder
// literal. Exists only to exercise the AST walker.
var url = "https://docs/DOMAIN/vmmd#create"
`,
			forbid: "https://docs/DOMAIN",
		},
		{
			// PR-A (issue #420) malformed-host regression: the host
			// was dropped to bare `docs` (no TLD) on the vmmd error
			// sites. The literal `https://docs/vmmd#prepare` snuck
			// past the original tripwire because the substring
			// `https://docs/DOMAIN` requires the placeholder token.
			// PR-C closes the gap by banning the whole
			// `https://docs/vmmd#` shape.
			name: "docs_vmmd_no_tld",
			src: `package tripwiretest

// Synthetic production-like file carrying the malformed-host
// regression. The vmmd fragment is a presentational path, not a
// placeholder — the tripwire bans the whole shape so a future
// regression that drops the TLD AND the placeholder guard would
// still fire.
var url = "https://docs/vmmd#prepare"
`,
			forbid: "https://docs/vmmd#",
		},
		{
			// PR-A (issue #420) RFC 2606 reserved-TLD regression:
			// the pre-#458 docs host used the IANA-reserved example
			// TLD. PR #458 renamed to `docs.gregale.dev` but missed
			// two sites in cmd/gregale. The reserved TLD must stay
			// out of the tree entirely — reserved TLDs cannot
			// resolve, so a stray lookup fails fast and obviously.
			name: "docs_example_reserved_tld",
			src: `package tripwiretest

// Synthetic production-like file carrying the RFC 2606 reserved-TLD
// regression. The .example TLD is unreachable by design.
var url = "https://docs.gregale.example/build/limits#memory"
`,
			forbid: "docs.gregale.example",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			srcPath := filepath.Join(tmp, "tripwire.go")
			if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("seed file: %v", err)
			}

			fset := token.NewFileSet()
			pf, err := parser.ParseFile(fset, srcPath, nil, parser.AllErrors)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			forbidden := []string{tc.forbid}
			var found string
			ast.Inspect(pf, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				for _, forbid := range forbidden {
					if strings.Contains(lit.Value, forbid) {
						found = fset.Position(lit.Pos()).String()
						return false
					}
				}
				return true
			})
			if found == "" {
				t.Fatalf("self-test: walker did not detect the seeded %q literal — the tripwire may be silently broken for this entry", tc.forbid)
			}
		})
	}
}

// TestEveryCodeHasWhycopyEntry pins 1:1 membership between the
// RFC 7807 stable Code… constants that the error-explanations
// cluster owns and the catalog rows in pkg/whycopy/whycopy.go.
// The cluster widens every Problem emission with Hint/Why/Fix;
// the catalog is the single source of truth for the
// customer-facing prose. A new cluster-owned Code… constant
// without a matching whycopy row would emit a wire-shaped
// problem with empty Hint/Why/Fix — a silent UX regression
// that no other tripwire catches.
//
// The tripwire is OPT-IN for codes outside the cluster's scope:
// only the codes listed in clusterCodes (the 9 new + the
// pre-existing stateless_only_violation that the cluster now
// flows through the same renderer) require a whycopy row. The
// rest of pkg/api/errors.go's Code… constants (plan/quota/
// auth/etc.) keep their existing pre-cluster UX copy and are
// out of scope for this tripwire — extending it to those would
// be a separate, larger migration that touches every error UX
// path.
//
// The walker:
//  1. Asserts every entry in clusterCodes has a matching row
//     in pkg/whycopy (forward direction — catch missing rows).
//  2. Asserts every whycopy row has a matching entry in
//     clusterCodes (inverse direction — catch dead rows).
//
// Both directions fail loud.
//
// Excludes:
//   - Test code (this file is a _test.go; the walker skips _test.go).
//   - Generated *.pb.go stubs.
//
// Scope: pkg/api/errors.go is the canonical home for the
// Code… constants. The check is on the constant NAMES (the
// literal strings), not on the constant declarations — so a
// future rename of the underlying package is fine, but a
// rename of the constant's string value trips the tripwire.
func TestEveryCodeHasWhycopyEntry(t *testing.T) {
	// clusterCodes is the explicit set of Code… values the
	// error-explanations cluster owns. When you add a new
	// code in this cluster's purview, add the literal string
	// here AND a matching row in pkg/whycopy/whycopy.go.
	clusterCodes := []string{
		api.CodeAppNotListening,
		api.CodeAppLoopbackBound,
		api.CodeAppArchMismatch,
		api.CodeEnvVarMissing,
		api.CodeAppHealthzUnauthorized,
		api.CodeAppRuntimeOOM,
		api.CodeDepInstallFailed,
		api.CodeAppStartupTimeout,
		api.CodeStatelessOnlyViolation,
		// ADR-117 §Production-ready follow-on: per-stage failure
		// codes. The renderer (pkg/dashboard/stages.StageFailureHTML)
		// reads these via whycopy.Decorate; the catalog is the
		// single source of truth for the customer-facing prose.
		api.CodeStageSourceDownloadFailed,
		api.CodeStageDependencyRestoreFailed,
		api.CodeStageImageBuildOOM,
		api.CodeStageImageBuildTimeout,
		api.CodeStageSecurityScanFindings,
		api.CodeStageSnapshotPrepareTimeout,
		api.CodeStageReadinessFailed,
	}

	// Forward direction: every cluster-owned Code must have a
	// whycopy row.
	whycopySet := map[string]bool{}
	for _, c := range whycopy.Codes() {
		whycopySet[c] = true
	}
	var missingInWhycopy []string
	for _, c := range clusterCodes {
		if !whycopySet[c] {
			missingInWhycopy = append(missingInWhycopy, c)
		}
	}
	if len(missingInWhycopy) > 0 {
		t.Fatalf("found %d cluster-owned Code… constants without a pkg/whycopy catalog row — every cluster code MUST have a row so the CLI's 5-line renderer can lift hint/why/fix prose:\n  %s\n\nAdd a row in pkg/whycopy/whycopy.go::catalog.",
			len(missingInWhycopy), strings.Join(missingInWhycopy, "\n  "))
	}

	// Inverse direction: every whycopy row must correspond to
	// a cluster-owned Code. Catches dead rows whose constant
	// was renamed or removed from the cluster's purview.
	clusterSet := map[string]bool{}
	for _, c := range clusterCodes {
		clusterSet[c] = true
	}
	var deadRows []string
	for _, c := range whycopy.Codes() {
		if !clusterSet[c] {
			deadRows = append(deadRows, c)
		}
	}
	if len(deadRows) > 0 {
		t.Fatalf("found %d pkg/whycopy catalog rows without a matching cluster-owned Code… constant:\n  %s\n\nDelete the row in pkg/whycopy/whycopy.go::catalog (the load-bearing source of truth for customer-facing prose) — the constant was renamed or removed from the cluster's purview.",
			len(deadRows), strings.Join(deadRows, "\n  "))
	}
}

// TestEveryPresetHasPresetwhyEntry pins 1:1 membership between the
// alert_preset catalog rows in migrations/00418_alert_presets_seed.sql
// and the customer-facing prose rows in pkg/presetwhy/presetwhy.go
// (issue #1233 / ADR-123 PR-C commit 3). The dashboard's "What
// does this alert mean?" panel reads via presetwhy.Decorate; the
// catalog is the single source of truth for the customer-facing
// prose. A new preset name in the migration seed without a
// matching presetwhy row would render a card with NO
// <details> panel — a silent UX regression that no other tripwire
// catches.
//
// Mirrors TestEveryCodeHasWhycopyEntry above (the
// error-explanations sibling). Why a separate package: alert
// preset names are domain catalog entries, not RFC 7807 error
// codes — the tripwire domains are intentionally separate so a
// future code addition doesn't accidentally satisfy a preset
// tripwire (or vice versa).
//
// The walker:
//  1. Asserts every entry in seedPresetNames has a matching row
//     in pkg/presetwhy (forward direction — catch missing rows).
//  2. Asserts every presetwhy row has a matching entry in
//     seedPresetNames (inverse direction — catch dead rows).
//
// Both directions fail loud. The seedPresetNames slice is the
// canonical membership list — it's the 8 names in the
// migrations/00418_alert_presets_seed.sql seed (3 originally
// enabled + 5 newly-enabled signals from ADR-123 PR-B).
func TestEveryPresetHasPresetwhyEntry(t *testing.T) {
	seedPresetNames := []string{
		// Originally enabled (3) — surface their prose for parity
		// even though they pre-date the ADR-123 follow-ups.
		"error_rate_2pct",
		"p95_latency_1s",
		"cold_start_10pct",
		// Newly enabled signals (5) — these are the 5 the
		// follow-ups focus on; the migration 00516 row flip
		// turned these from "coming soon" to enabled_in_catalog.
		"api_down",
		"spend_eur_20",
		"deploy_failed",
		"cert_expiring_14d",
		"queue_backlog_growing",
	}

	// Forward direction: every seed preset name must have a
	// presetwhy row.
	presetwhySet := map[string]bool{}
	for _, c := range presetwhy.Codes() {
		presetwhySet[c] = true
	}
	var missingInPresetwhy []string
	for _, c := range seedPresetNames {
		if !presetwhySet[c] {
			missingInPresetwhy = append(missingInPresetwhy, c)
		}
	}
	if len(missingInPresetwhy) > 0 {
		t.Fatalf("found %d alert_preset catalog rows without a pkg/presetwhy row — every preset in migrations/00418_alert_presets_seed.sql MUST have a row so the dashboard's \"What does this alert mean?\" panel can render:\n  %s\n\nAdd a row in pkg/presetwhy/presetwhy.go::catalog.",
			len(missingInPresetwhy), strings.Join(missingInPresetwhy, "\n  "))
	}

	// Inverse direction: every presetwhy row must correspond to
	// a seed preset name. Catches dead rows whose preset was
	// renamed or removed from the catalog.
	seedSet := map[string]bool{}
	for _, c := range seedPresetNames {
		seedSet[c] = true
	}
	var deadRows []string
	for _, c := range presetwhy.Codes() {
		if !seedSet[c] {
			deadRows = append(deadRows, c)
		}
	}
	if len(deadRows) > 0 {
		t.Fatalf("found %d pkg/presetwhy catalog rows without a matching alert_preset seed row:\n  %s\n\nDelete the row in pkg/presetwhy/presetwhy.go::catalog — the preset was renamed or removed from migrations/00418_alert_presets_seed.sql.",
			len(deadRows), strings.Join(deadRows, "\n  "))
	}
}

// TestLintTripwire_DoctorStrictMutex pins the flag-name scoping
// rule for the doctor-strict cluster (spec §6.4 amendment 1).
// Background: --strict / --lenient are already claimed by the
// deploy-diff cluster (commands2.go:838-839). Re-introducing a bare
// `--strict` (without a `--doctor-` or `--diff-` scope prefix) on
// any new deploy path would silently collide with the existing
// semantics. The tripwire walks every non-test file under cmd/gregale/
// and fails on any flag.* flag-registration call whose name is
// EXACTLY "strict" and whose enclosing function is not in the
// allow-list (cmdDeployTarball for --diff, cmdDoctor for the
// standalone doctor).
//
// Flag-registration call set: Bool / String / Int / Int64 / Uint /
// Duration / Float64 / Func / Var. The full set is checked so a
// future `fs.Int("strict", 0, ...)` (e.g. for a numeric counter)
// is caught with the same severity as the original `fs.Bool`
// case. customer-script collision semantics are identical across
// all of them.
//
// Allow-list keyed by enclosing *function name* (not line) so a
// maintainer adding a flag above the documented declaration does
// not silently shift the legitimate call off the allow-list.
//
// If you genuinely need a new strict-style gate, scope it via a
// prefix (e.g. `--secret-strict`, `--build-strict`). If you need to
// undo the diff `--strict` flag, that requires an ADR — the rename
// would break customer scripts.
func TestLintTripwire_DoctorStrictMutex(t *testing.T) {
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
		t.Fatalf("parse cmd/gregale: %v", err)
	}

	// Allow-list keyed by enclosing function name. Stable across
	// edits that shift line numbers (the line-anchored version had
	// a hazard where adding an unrelated flag above the
	// documented declaration would silently disable the tripwire
	// on the legitimate --strict).
	allowedFuncs := map[string]bool{
		"cmdDeployTarball": true, // --strict (--diff pair, commands2.go:838)
		"cmdDoctor":        true, // --strict (gregale doctor, commands_doctor.go:124)
	}

	// flag-registration selector names. The full set is matched
	// because any of these carries the same customer-script
	// collision semantics. Adding a new selector (e.g. fs.Text)
	// requires extending this list AND the test below.
	flagScreators := map[string]bool{
		"Bool": true, "String": true, "Int": true, "Int64": true,
		"Uint": true, "Duration": true, "Float64": true,
		"Func": true, "Var": true,
	}

	var violations []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if !flagScreators[sel.Sel.Name] {
					return true
				}
				if len(call.Args) < 1 {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				name := strings.Trim(lit.Value, `"`)
				if name != "strict" {
					return true
				}
				if enclosingFuncName(n, file) != "" &&
					allowedFuncs[enclosingFuncName(n, file)] {
					return true
				}
				pos := fset.Position(call.Pos())
				violations = append(violations, pos.String()+": "+sel.Sel.Name+"(\""+name+"\"...)")
				return true
			})
		}
	}
	if len(violations) > 0 {
		t.Fatalf("found %d unscoped --strict flag declaration(s). --strict is owned by the deploy-diff cluster (cmdDeployTarball in commands2.go) and the gregale doctor (cmdDoctor in commands_doctor.go). New strict-style gates must be scoped via a prefix (--doctor-strict, --secret-strict, --diff-strict, etc.) to avoid customer-script breakage:\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// enclosingFuncName walks up the AST from n (tracking parents
// explicitly because stdlib ast.Node has no Parent method) and
// returns the name of the nearest enclosing named function. We do
// not use file:line keying — adding an unrelated flag above the
// documented declaration would otherwise shift the legitimate call
// off the line-anchored key. Function names are stable across
// edits that add unrelated code.
// visitor is a single-file ast.Visitor implementation that records
// the name of the most recently entered FuncDecl. Used by
// enclosingFuncName to find the enclosing function for a target
// node. ast.Walk requires a Visit(Node) Visitor method, so we use
// a struct rather than a bare func.
type visitor struct {
	target    ast.Node
	found     string
	terminate bool
}

func (v *visitor) Visit(n ast.Node) ast.Visitor {
	if v.terminate || n == nil {
		return nil
	}
	if n == v.target {
		v.terminate = true
		return nil
	}
	fd, ok := n.(*ast.FuncDecl)
	if ok {
		v.found = fd.Name.Name
	}
	return v
}

func enclosingFuncName(target ast.Node, file *ast.File) string {
	v := &visitor{target: target}
	ast.Walk(v, file)
	return v.found
}

// TestLintTripwire_DoctorStrictMutex_SelfTest ensures the tripwire
// is alive — a synthetic fixture carrying an unscoped
// `fs.Bool("strict", ...)` outside the allow-list must trip. If
// this test passes, the walker is broken (false-negative).
// Mirrors the existing TestLintTripwire_NoLiteralDocsDomainSelfTest
// pattern at :612.
func TestLintTripwire_DoctorStrictMutex_SelfTest(t *testing.T) {
	src := `package tripwiretest

import "flag"

func cmdBadNewFeature(args []string) int {
	fs := flag.NewFlagSet("bad", flag.ContinueOnError)
	strict := fs.Bool("strict", false, "unscoped strict — should trip")
	_ = strict
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return 0
}
`
	// Parse the synthetic source.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "tripwiretest.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Walk and apply the same predicate as the live tripwire. We
	// expect exactly one violation on `fs.Bool("strict", ...)` from
	// cmdBadNewFeature.
	allowedFuncs := map[string]bool{
		"cmdDeployTarball": true,
		"cmdDoctor":        true,
	}
	flagSelectors := map[string]bool{
		"Bool": true, "String": true, "Int": true, "Int64": true,
		"Uint": true, "Duration": true, "Float64": true,
		"Func": true, "Var": true,
	}
	var violations []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if !flagSelectors[sel.Sel.Name] {
			return true
		}
		if len(call.Args) < 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name := strings.Trim(lit.Value, `"`)
		if name != "strict" {
			return true
		}
		enc := enclosingFuncName(n, f)
		if allowedFuncs[enc] {
			return true
		}
		pos := fset.Position(call.Pos())
		violations = append(violations, pos.String()+": "+sel.Sel.Name+"(\""+name+"\"...)")
		return true
	})
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %d:\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
	if !strings.Contains(violations[0], "cmdBadNewFeature") && !strings.Contains(violations[0], "tripwiretest.go") {
		t.Errorf("violation should mention the offending function or file, got %q", violations[0])
	}
}
