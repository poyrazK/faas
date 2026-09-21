package db

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDirectPoolDefaultsToTheOrdinaryPool(t *testing.T) {
	// A nil pool stays nil rather than panicking: callers resolve through
	// DirectPool unconditionally and some hold a lazily-opened pool.
	if got := DirectPool(nil); got != nil {
		t.Errorf("DirectPool(nil) = %v, want nil", got)
	}

	// Unregistered pool → itself. This is every deployment without a pooler,
	// and it is what makes the routing changes a no-op today.
	p := &pgxpool.Pool{}
	if got := DirectPool(p); got != p {
		t.Error("DirectPool returned a different pool for an unregistered pool; " +
			"without a direct DSN the ordinary pool must be used unchanged")
	}
}

func TestRegisterDirectPoolRoutesAndCleansUp(t *testing.T) {
	ordinary := &pgxpool.Pool{}
	direct := &pgxpool.Pool{}

	registerDirectPool(ordinary, direct)
	if got := DirectPool(ordinary); got != direct {
		t.Fatal("DirectPool did not return the registered sibling")
	}

	// Registering a pool as its own sibling must be a no-op, not a
	// self-reference that later closes the ordinary pool.
	self := &pgxpool.Pool{}
	registerDirectPool(self, self)
	if got := DirectPool(self); got != self {
		t.Error("self-registration changed resolution")
	}
	directMu.RLock()
	_, recorded := directPools[self]
	directMu.RUnlock()
	if recorded {
		t.Error("self-registration was recorded; close would then close the ordinary pool")
	}

	// Unregistering restores the default. Not calling direct.Close() here:
	// these are zero-value pools, and the registry behaviour is what's under
	// test.
	directMu.Lock()
	delete(directPools, ordinary)
	directMu.Unlock()
	if got := DirectPool(ordinary); got != ordinary {
		t.Error("after unregistering, DirectPool must fall back to the ordinary pool")
	}
}

func TestPooledExecModeOnlyWhenPoolingIsConfigured(t *testing.T) {
	cfg, err := pgxpool.ParseConfig("postgres://u@localhost/d")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	before := cfg.ConnConfig.DefaultQueryExecMode

	// No direct DSN means no pooler, so the faster default exec mode stays.
	t.Setenv(DirectDSNEnv, "")
	applyPooledExecMode(cfg)
	if cfg.ConnConfig.DefaultQueryExecMode != before {
		t.Errorf("exec mode changed with %s unset; a deployment without a pooler must keep today's behaviour", DirectDSNEnv)
	}

	// With a direct DSN configured the ordinary pool is presumed to be behind
	// a transaction pooler, where named prepared statements break: the next
	// transaction can land on a server connection that never prepared them.
	t.Setenv(DirectDSNEnv, "postgres://u@localhost:5432/d")
	applyPooledExecMode(cfg)
	if cfg.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeExec {
		t.Errorf("exec mode = %v, want QueryExecModeExec when %s is set",
			cfg.ConnConfig.DefaultQueryExecMode, DirectDSNEnv)
	}
}

// sessionScopedAcquire records every Acquire that pins a connection for
// session-scoped work, with the reason it cannot be pooled. Each MUST resolve
// through DirectPool.
//
// This map is the point of the gate below. Transaction pooling breaks
// silently — a session advisory lock taken on a pooled connection is released
// at the end of the enclosing transaction and may be handed to another
// client, and nothing fails loudly — so a new pinned Acquire has to be a
// deliberate decision rather than something a reviewer has to notice.
var sessionScopedAcquire = map[string]string{
	"pkg/db/notify_hub.go":       "LISTEN: the hub parks one connection per daemon",
	"pkg/db/notify.go":           "LISTEN: legacy per-subscriber path",
	"pkg/db/wait_for.go":         "LISTEN: short-lived per-request wait",
	"pkg/db/migrate_advisory.go": "session pg_advisory_lock held across statements",
	"pkg/state/pgstore.go":       "session pg_advisory_lock for the edge-rule mutation fence",
}

// pooledAcquireOK records Acquires that are deliberately on the ordinary
// pool. Acquiring a connection is not by itself session-scoped; what matters
// is whether anything session-scoped happens on it before release.
var pooledAcquireOK = map[string]string{
	"pkg/db/warmup.go":          "pool warm-up: acquires and releases N connections to prove capacity; must exercise the POOLED path",
	"pkg/db/pgtest/pgtest.go":   "test harness; never runs against a pooler",
	"pkg/db/pgtest/template.go": "test harness; never runs against a pooler",
}

// TestSessionScopedAcquiresRouteThroughDirectPool is the gate.
//
// It parses the tree (rather than grepping, which matched Acquire inside doc
// comments on its first run) and requires every real pgxpool acquisition to
// be classified: routed through DirectPool because it is session-scoped, or
// listed as deliberately pooled. The failure it guards is the quiet kind — a
// new LISTEN or pg_advisory_lock on a pooled connection works perfectly until
// a pooler is switched on, then misbehaves in a way that reads as a lock bug
// rather than a configuration change.
func TestSessionScopedAcquiresRouteThroughDirectPool(t *testing.T) {
	root := repoRootForDirectTest(t)
	var offenders []string

	for _, dir := range []string{"pkg", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0) // 0: drop comments
			if parseErr != nil {
				return fmt.Errorf("parse %s: %w", path, parseErr)
			}
			var acquires []ast.Expr
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil || sel.Sel.Name != "Acquire" {
					return true
				}
				acquires = append(acquires, sel.X)
				return true
			})
			if len(acquires) == 0 {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)

			if _, ok := pooledAcquireOK[rel]; ok {
				return nil
			}
			if _, ok := sessionScopedAcquire[rel]; ok {
				// An entry asserts the site was routed, not that it is waived.
				for _, recv := range acquires {
					if !usesDirectPool(recv) {
						offenders = append(offenders, rel+
							": listed as session-scoped but this Acquire does not resolve through DirectPool")
						break
					}
				}
				return nil
			}
			// Unclassified. Only pgxpool acquisitions matter; lease and
			// semaphore Acquires are unrelated.
			if strings.Contains(string(mustRead(t, path)), "pgxpool") {
				offenders = append(offenders, rel+": unclassified pgxpool acquisition")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("every pgxpool acquisition must be classified (see pkg/db/direct.go). "+
			"Session-scoped work (LISTEN, session pg_advisory_lock) goes in sessionScopedAcquire "+
			"and must call db.DirectPool; anything else goes in pooledAcquireOK with a reason:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// usesDirectPool reports whether an Acquire receiver is a DirectPool(...) call.
func usesDirectPool(recv ast.Expr) bool {
	call, ok := recv.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "DirectPool"
	case *ast.SelectorExpr:
		return fn.Sel != nil && fn.Sel.Name == "DirectPool"
	}
	return false
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func repoRootForDirectTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("module root not reachable")
	return ""
}
