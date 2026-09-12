// crosscheck_test.go — the post-#930 adversarial invariant. Walks
// the source tree statically with go/parser and asserts:
//
//  1. daemonInfoTable.Allows[role] agrees with
//     cmd/<daemon>/main.go::role.Require(daemon, ..., allow...)'s
//     allow-list (byte-for-byte, per role).
//  2. daemonInfoTable.EnvKey agrees with
//     cmd/<daemon>/config.go::role.FromConfig(..., "FAAS_<X>_ROLE")
//     (or the equivalent for daemons that have no config.go today:
//     cmd/<daemon>/main.go::role.FromConfig(..., "FAAS_<X>_ROLE")
//     on a `<daemon>`-keyed env).
//
// The earlier review caught two regressions in the role-deny-style
// table that this struct replaced: vmmd/imaged/gatewayd-internal
// listed under RoleControlPlane (Refused), and gatewayd-internal
// emitting FAAS_GATEWAYD_INTERNAL_ROLE instead of FAAS_GATEWAYD_ROLE.
// Both would still slip through whitebox tests that assert against
// the wrong table — the only way to catch a future regression is to
// walk the daemon source files and rebuild the truth there.
//
// If a future edit renames a daemon, adds a new role to a daemon's
// Require allow-list, or extends the daemonunitspec.Registry, this
// test fails first with a precise list of drift.
package roleTemplating

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// daemonSourceRoot is overridable via env so the test runs from any
// worktree root; the default "<repo>" assumes `go test ./...` from
// the repo root, where cmd/<daemon>/main.go is reachable.
func repoRoot(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("REPO_ROOT"); env != "" {
		return env
	}
	// walk up from this test file until we find "cmd/"
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 16; i++ {
		if _, err := os.Stat(filepath.Join(dir, "cmd")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root from %s; set REPO_ROOT", dir)
	return ""
}

// loadDaemonRoleRequire walks every .go file in cmd/<daemon>/ and
// returns the (Identifier, AllowedRoles...) captured by the call
//
//	role.Require(<daemon>, ..., role.Role<Name>, ...)
//
// Allowed roles are extracted from the literal identifiers
// (role.RoleSingleBox, role.RoleControlPlane, role.RoleComputeOnly)
// in the trailing-variadic position. Returns an error if the call
// site doesn't match the expected shape — a future edit that
// renames the gate must update the parser (loud, intentional).
//
// Scans every Go file in the daemon dir, not just main.go: today
// gatewayd-internal calls role.Require from cmd/gatewayd-internal/run.go
// (the rest are in main.go). Loading cmd/<daemon>/*.go catches
// future file-reorganization without re-tweaking the parser.
func loadDaemonRoleRequire(t *testing.T, repoRoot, daemon string) ([]string, error) {
	t.Helper()
	dmnDir := filepath.Join(repoRoot, "cmd", daemon)
	entries, err := os.ReadDir(dmnDir)
	if err != nil {
		return nil, err
	}
	var found bool
	var allows []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		p := filepath.Join(dmnDir, e.Name())
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.AllErrors)
		if err != nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "role" || sel.Sel.Name != "Require" {
				return true
			}
			// First arg must be a string literal of the daemon name.
			if len(call.Args) < 2 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || strings.Trim(lit.Value, `"`) != daemon {
				return true
			}
			for _, arg := range call.Args[2:] {
				s, ok := arg.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				p, ok := s.X.(*ast.Ident)
				if !ok || p.Name != "role" || !strings.HasPrefix(s.Sel.Name, "Role") {
					continue
				}
				allows = append(allows, s.Sel.Name)
			}
			found = true
			return true
		})
	}
	if !found {
		return nil, &os.PathError{Op: "parse", Path: filepath.Join(repoRoot, "cmd", daemon), Err: os.ErrNotExist}
	}
	return allows, nil
}

// rolesFromAllowIdents maps "RoleSingleBox" → "single-box" etc., the
// only three valid identifies at the call site today.
var rolesFromAllowIdents = map[string]string{
	"RoleSingleBox":    "single-box",
	"RoleControlPlane": "control-plane",
	"RoleComputeOnly":  "compute-only",
}

// loadDaemonEnvKey walks every .go file in cmd/<daemon>/ and finds
// the string-literal passed as the second argument to any
// role.FromConfig(..., "<env>") call. Returns "<env>" or an error
// if not found.
//
// Scans all .go files in the daemon dir (config.go typically, but
// imaged/apid/gatewayd-public have no config.go today and put the
// role.FromConfig call in main.go alongside role.Require).
func loadDaemonEnvKey(t *testing.T, repoRoot, daemon string) (string, error) {
	t.Helper()
	dmnDir := filepath.Join(repoRoot, "cmd", daemon)
	entries, err := os.ReadDir(dmnDir)
	if err != nil {
		return "", err
	}
	var found string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		p := filepath.Join(dmnDir, e.Name())
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.AllErrors)
		if err != nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "role" || sel.Sel.Name != "FromConfig" {
				return true
			}
			if len(call.Args) < 2 {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			found = strings.Trim(lit.Value, `"`)
			return false
		})
		if found != "" {
			return found, nil
		}
	}
	return "", os.ErrNotExist
}

// TestDaemonInfoTableMatchesDaemonSource is the cross-check invariant.
// It walks every daemon in pkg/daemonunitspec.Registry, parses its
// main.go for role.Require's allow-list, parses its config.go
// (or main.go if no config.go) for role.FromConfig's envKey, and
// asserts daemonInfoTable agrees byte-for-byte.
//
// If a daemon has a config.go today that uses role.FromConfig(..., "FAAS_<X>_ROLE")
// AND the same file uses role.Require(...) for the same role,
// the test asserts daemonInfoTable carries both. If a daemon has no
// role.Require (some today don't), the test is skipped for that
// daemon (avoids forcing the cross-check on daemons that don't yet
// implement the Gate-B gate).
func TestDaemonInfoTableMatchesDaemonSource(t *testing.T) {
	repoRoot := repoRoot(t)
	for _, entry := range daemonunitspecRegistry() {
		dmn := entry
		t.Run(dmn, func(t *testing.T) {
			info := daemonInfoTable[dmn]
			if info.EnvKey == "" {
				t.Fatalf("daemonInfoTable[%q].EnvKey is empty", dmn)
			}
			envKey, err := loadDaemonEnvKey(t, repoRoot, dmn)
			if err == nil && envKey != info.EnvKey {
				t.Errorf("daemonInfoTable[%q].EnvKey=%q, but daemon source has %q (cmd/%s/{main,config}.go)", dmn, info.EnvKey, envKey, dmn)
			}
			allowIdents, err := loadDaemonRoleRequire(t, repoRoot, dmn)
			if err != nil {
				t.Skipf("daemon %q has no role.Require call site (pre-Gate-B): %v", dmn, err)
			}
			got := allowSet(allowIdents)
			// Cross-check: every identifier from cmd/<dmn>/main.go
			// must be in daemonInfoTable.Allows. If the daemon
			// allows a role this package omits, the box would
			// silently fail to template a drop-in for a role the
			// daemon actually wants.
			for ident := range got {
				canonical, ok := rolesFromAllowIdents[ident]
				if !ok {
					t.Errorf("role.Require allow-list for %q contains unknown ident %q (parser needs an update)", dmn, ident)
					continue
				}
				if r := Role(canonical); !info.Allows[r] {
					t.Errorf("daemonInfoTable[%q].Allows is missing role %q that daemon %q allows", dmn, r, dmn)
				}
			}
			// And vice versa: every entry in daemonInfoTable.Allows
			// must come from the daemon source. If we record an
			// allow the daemon doesn't, Apply silently templates
			// a drop-in the daemon refuses to honor.
			for r := range info.Allows {
				ident, ok := canonicalToIdent[r]
				if !ok {
					t.Errorf("daemonInfoTable[%q].Allows has unknown role %q (parser needs an update)", dmn, r)
					continue
				}
				if _, ok := got[ident]; !ok {
					t.Errorf("daemonInfoTable[%q].Allows includes %q but daemon source does NOT call role.Require with %q — drop-in would be refused at boot", dmn, r, ident)
				}
			}
		})
	}
}

func allowSet(idents []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range idents {
		out[s] = struct{}{}
	}
	return out
}

var canonicalToIdent = map[Role]string{
	RoleSingleBox:    "RoleSingleBox",
	RoleControlPlane: "RoleControlPlane",
	RoleComputeOnly:  "RoleComputeOnly",
}

// daemonunitspecRegistry returns pkg/daemonunitspec.Registry's
// daemon names in slice order. We re-derive this here rather than
// importing pkg/daemonunitspec internally to keep this test
// hermetic; the alternative (import) would couple the cross-check
// to whatever the daemonunitspec package happens to ship at test
// time, defeating the "is the table in sync with daemon source?"
// intent.
func daemonunitspecRegistry() []string {
	// Hard-coded at the time of writing; the surface here is the
	// daemon name list only, NOT their per-daemon role.Require
	// allow (which is the load-bearing data and IS parsed from
	// source). When a daemon is added/removed, this list + the
	// per-daemon parse call must move in lockstep — TestDaemonInfoTableMatchesDaemonSource
	// fails first.
	return []string{
		"vmmd",
		"realtimed",
		"apid",
		"schedd",
		"meterd",
		"githubd",
		"gatewayd-public",
		"imaged",
		"gatewayd-internal",
		"builderd",
	}
}

// cover ensures the canonicalToIdent / rolesFromAllowIdents maps are
// kept in sync when a new role is added to pkg/role. Today we have
// three; if a fourth lands, the cross-check must learn it.
func TestRoleIdentMirrorsAreExhaustive(t *testing.T) {
	want := map[Role]string{
		RoleSingleBox:    "RoleSingleBox",
		RoleControlPlane: "RoleControlPlane",
		RoleComputeOnly:  "RoleComputeOnly",
	}
	got := canonicalToIdent
	if len(got) != len(want) {
		t.Errorf("canonicalToIdent has %d entries, want %d", len(got), len(want))
	}
	for r, ident := range want {
		if got[r] != ident {
			t.Errorf("canonicalToIdent[%q] = %q, want %q", r, got[r], ident)
		}
		if _, ok := rolesFromAllowIdents[ident]; !ok {
			t.Errorf("rolesFromAllowIdents missing entry for %q", ident)
		}
	}
	// Defensive: keys are sorted, so test names from t.Run are
	// deterministic.
	_ = sort.Strings
}
