package conformance

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// uncoveredFile lists every state.Store method the conformance cases do not
// exercise yet. It is a ratchet, not a wish list: the guard below fails when
// the list drifts in either direction.
const uncoveredFile = "uncovered.txt"

// TestConformanceCoverage keeps the gap between the state.Store contract and
// the cases that actually pin it VISIBLE.
//
// The suite covers a small fraction of the interface. That is not itself a
// defect — a contract test earns its keep on the methods where the two
// implementations can drift, not on every getter. The defect is not knowing
// which fraction, which is how PgStore and MemStore both ended up returning
// zero from every live-instance reader while the suite sat green (#1666,
// #1669, #1672).
//
// So: every method is either exercised by a case or listed in uncovered.txt.
// Adding a method to state.Store fails this test until you choose one. The
// list only ever shrinks, because covering a method while leaving it listed
// fails too.
func TestConformanceCoverage(t *testing.T) {
	contract := storeMethods()
	if len(contract) == 0 {
		t.Fatal("reflect found no methods on state.Store")
	}
	covered := calledStoreMethods(t, contract)
	listed := readUncovered(t)

	var missing, staleCovered, staleGone []string
	for name := range contract {
		_, isCovered := covered[name]
		_, isListed := listed[name]
		switch {
		case isCovered && isListed:
			staleCovered = append(staleCovered, name)
		case !isCovered && !isListed:
			missing = append(missing, name)
		}
	}
	for name := range listed {
		if _, ok := contract[name]; !ok {
			staleGone = append(staleGone, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(staleCovered)
	sort.Strings(staleGone)

	if len(missing) > 0 {
		t.Errorf("%d state.Store method(s) are neither exercised by a conformance case nor listed in %s.\n"+
			"Add a case, or add the name to %s if the method genuinely cannot drift between the two stores:\n  %s",
			len(missing), uncoveredFile, uncoveredFile, strings.Join(missing, "\n  "))
	}
	if len(staleCovered) > 0 {
		t.Errorf("%d method(s) are now exercised by a case but still listed in %s — delete these lines, the ratchet only tightens:\n  %s",
			len(staleCovered), uncoveredFile, strings.Join(staleCovered, "\n  "))
	}
	if len(staleGone) > 0 {
		t.Errorf("%d name(s) in %s are no longer on state.Store — delete these lines:\n  %s",
			len(staleGone), uncoveredFile, strings.Join(staleGone, "\n  "))
	}

	t.Logf("conformance covers %d of %d state.Store methods (%.1f%%); %d listed as uncovered",
		len(covered), len(contract), 100*float64(len(covered))/float64(len(contract)), len(listed))
}

// storeMethods is the state.Store method set, straight off the interface, so
// the contract side of the comparison cannot go stale.
func storeMethods() map[string]struct{} {
	typ := reflect.TypeOf((*state.Store)(nil)).Elem()
	out := make(map[string]struct{}, typ.NumMethod())
	for i := 0; i < typ.NumMethod(); i++ {
		out[typ.Method(i).Name] = struct{}{}
	}
	return out
}

// calledStoreMethods reports which contract methods this package calls.
//
// It reads the package source rather than instrumenting a store: intercepting
// 689 methods would need a generated wrapper, and any hand-written one would
// itself drift. A selector is counted when its name is on the interface,
// which is precise enough here because the cases only ever reach a Store
// through fx.Store or a local store variable.
func calledStoreMethods(t *testing.T, contract map[string]struct{}) map[string]struct{} {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		// Skip this guard: the names it mentions are data, not calls.
		return !strings.HasSuffix(fi.Name(), "coverage_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse conformance package: %v", err)
	}
	out := map[string]struct{}{}
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
				if _, ok := contract[sel.Sel.Name]; ok {
					out[sel.Sel.Name] = struct{}{}
				}
				return true
			})
		}
	}
	return out
}

func readUncovered(t *testing.T) map[string]struct{} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(uncoveredFile))
	if err != nil {
		t.Fatalf("read %s: %v", uncoveredFile, err)
	}
	out := map[string]struct{}{}
	var prev string
	for i, line := range strings.Split(string(raw), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if _, dup := out[name]; dup {
			t.Errorf("%s:%d: duplicate entry %q", uncoveredFile, i+1, name)
		}
		if prev != "" && name < prev {
			t.Errorf("%s:%d: %q sorts before %q — keep the file sorted so diffs stay readable", uncoveredFile, i+1, name, prev)
		}
		prev = name
		out[name] = struct{}{}
	}
	return out
}
