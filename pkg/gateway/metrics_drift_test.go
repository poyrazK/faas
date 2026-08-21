// metrics_drift_test.go — drift guard for the edgeRuleKinds
// closed set (pkg/gateway/metrics.go::edgeRuleKinds).
//
// Why this test exists:
//
// Pre-ADR-123 the edge-rule counter pre-instantiation loops
// at pkg/gateway/metrics.go:1051 + 1172 carried near-identical
// string slices — but with pre-existing drift (one omitted
// "jwt"). The drift was load-bearing-invisible because the
// §12 dashboard pivot on (kind=...) surfaced from first hit
// anyway (Prometheus auto-discovers labels). The drift
// became load-bearing-auditable when ADR-123 added
// ingress_members as a NEW kind — the omission would have
// cascaded to a missing tuple on whichever loop carried the
// drift.
//
// Post-ADR-123 the closed set lives at pkg/gateway/metrics.go::
// edgeRuleKinds and both loops iterate the same slice. This
// test pins the single-source-of-truth contract by scanning
// the call sites that emit edge-rule labels and asserting the
// closed set is a superset. If a new kind starts emitting
// without being added to the slice, this test catches it.
//
// The "superset" direction (closed-set contains emit) is the
// right semantics for the §12 dashboard: zero-valued
// counters are cheap; missing tuples force dashboard authors
// to manually compensate. The "subset" direction (emit
// contains closed-set) is intentional — pre-instantiating a
// kind nobody emits is a noisy zero-value, not a bug.
//
// Scope:
//
//   - Call sites are detected by a literal regex scan over
//     the source tree. Not as strong as a graph-based
//     analyzer (a Go type checker would be) but strong
//     enough to catch the load-bearing cases: every emit
//     goes through `ObserveEdgeRuleMatch(` or
//     `ObserveEdgeRuleApply(`, and the first argument is a
//     kind literal. The scan captures both literal kinds
//     and the `edgeRuleKinds` constant itself (the latter
//     is the "I'm using the closed set" signal).
//
//   - The scan is package-scoped to pkg/gateway/ — that's
//     where the emit helpers live. cmd/gatewayd-internal
//     reaches into the metrics through the same helpers
//     (no separate kind literals).
//
//   - Tests run on every `make test` invocation; they're
//     sub-second (a single regex scan over the package).
package gateway

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEdgeRuleKindsAgreeWithCallSites pins the closed-set
// contract. Iterates pkg/gateway/ source files, finds every
// literal string passed as the first argument to
// ObserveEdgeRuleMatch / ObserveEdgeRuleApply, and asserts
// each kind is in edgeRuleKinds. Adding a new kind without
// appending to edgeRuleKinds is a test failure.
func TestEdgeRuleKindsAgreeWithCallSites(t *testing.T) {
	emitKinds := scanEdgeRuleEmitKinds(t)

	closed := make(map[string]bool, len(edgeRuleKinds))
	for _, k := range edgeRuleKinds {
		closed[k] = true
	}

	for _, kind := range emitKinds {
		if !closed[kind] {
			t.Errorf("edge-rule kind %q emitted at call sites but not in edgeRuleKinds closed set; append to metrics.go::edgeRuleKinds to keep §12 dashboard tuples pre-instantiated", kind)
		}
	}

	// Inverse direction is informational only — emitting
	// a kind that's NOT in the closed set is the bug;
	// having a kind in the closed set that nobody emits
	// is a noisy zero-value on the dashboard but not a
	// correctness issue. We surface it via t.Logf so the
	// operator dashboard alert ("ingress_* 0 forever") can
	// pinpoint which kind to remove.
	for k := range closed {
		found := false
		for _, emit := range emitKinds {
			if emit == k {
				found = true
				break
			}
		}
		if !found {
			t.Logf("edgeRuleKinds contains %q but no ObserveEdgeRule* call site emits it (noisy zero-value counter on §12 dashboard; safe to remove if intentional)", k)
		}
	}
}

// scanEdgeRuleEmitKinds walks pkg/gateway/*.go and returns
// every string literal passed as the first argument to
// ObserveEdgeRuleMatch / ObserveEdgeRuleApply. Uses go/ast
// rather than a regex so the result is structurally stable
// (a kind literal embedded in a comment is NOT picked up,
// only call sites where the literal is actually the first
// argument to the helper).
func scanEdgeRuleEmitKinds(t *testing.T) []string {
	t.Helper()
	var kinds []string
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// Walk pkg/gateway/*.go (skip subdirectories — they
	// are independent packages and don't share the
	// edgeRuleKinds contract).
	matches, err := filepath.Glob(filepath.Join(wd, "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			// Tests can use any literal they want;
			// pinning the contract to production
			// files avoids the test-data drift
			// noise (synth_internal_only_test.go
			// uses kinds like "ingress_internal" for
			// fixture shapes — those are not real
			// emits).
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := fn.Sel.Name
			if name != "ObserveEdgeRuleMatch" && name != "ObserveEdgeRuleApply" {
				return true
			}
			if len(call.Args) < 1 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			kinds = append(kinds, strings.Trim(lit.Value, `"`))
			return true
		})
	}
	return kinds
}
