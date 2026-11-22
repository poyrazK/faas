// Issue #578 / PR-B: dependency-ordered daemon restart.
//
// The control-plane restart loop (cmd/deployctl/runtime.go:240-254 +
// the GitHub Actions cd-controlplane workflow) previously walked
// Registry in slice order, ignoring Lifecycle.After declarations on 6 of
// 8 daemons (only vmmd and apid lack After — both are root-of-DAG).
// Slice order is human-readable but operationally wrong: gatewayd-public
// is at index 4, schedd is at index 2, so a plain slice restart would
// bounce gatewayd-public before schedd is even back up.
//
// RestartOrder() topologically sorts Registry by Lifecycle.After with
// a deterministic tiebreaker (Registry slice order on equal depth) so
// two runs with the same registry return the same order. Cycles are
// impossible today (every After edge names a registered daemon with no
// back-edges) but the implementation returns an error so a future
// registry edit can't silently produce a partial topological pass.
//
// Why Kahn's algorithm, not DFS:
//   - DFS produces a REVERSE post-order that is correct but the
//     reverse step adds cognitive overhead at the call site. Kahn's
//     emits the order directly (process roots → decrement indegrees →
//     process next-available roots), so the resulting slice is the
//     restart order without a trailing []reverse{} pass.
//   - DFS is also "loud" on cycles (recursion + back-edge set) but Go
//     has no iterative DFS that is shorter than iterative Kahn's.
//   - Tiebreaker on equal-in-degree: prefer the daemon with the lowest
//     Registry slice index. Stable across shuffles within a single
//     process.
//
// Pre-condition: every name in Lifecycle.After MUST exist in Registry.
// The constructor below returns ErrUnknownDependency on a typo so an
// editor catches it at first deployctl run, not at runtime when the
// RESTART call silently skips a daemon.

package daemonunitspec

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnknownDependency is returned when Lifecycle.After names a daemon
// that is not in the Registry. Today the Registry is hand-curated
// (registry.go) so a typo would slip through `go vet` — surfacing it
// at RestartOrder() time is the cheap gate.
type ErrUnknownDependency struct {
	Daemon    string
	DependsOn string
}

func (e *ErrUnknownDependency) Error() string {
	return fmt.Sprintf("daemonunitspec: %q depends on %q which is not in Registry", e.Daemon, e.DependsOn)
}

// ErrCycle is returned when the Lifecycle.After graph contains a cycle
// (A→B, B→C, C→A). Today the graph is a DAG (every After is a
// forward-only edge to a root or middle-tier daemon with no outgoing
// After); the error is defensive against a future registry edit that
// introduces a back-edge. When returned, the partial order processed
// before the cycle was detected is in Partial.
type ErrCycle struct {
	Partial []string
	Back    []string // edges that close the cycle, in the order they were discovered
}

func (e *ErrCycle) Error() string {
	return fmt.Sprintf("daemonunitspec: cycle in Lifecycle.After (processed %d of %d daemons): back=%v",
		len(e.Partial), len(e.Partial)+len(e.Back), e.Back)
}

// RestartOrder returns the Registry's daemons in dependency order:
// every daemon appears AFTER every daemon it lists in Lifecycle.After.
// Two daemons with no ordering relationship between them appear in
// their Registry slice order (stable tiebreaker).
//
// Returns an error if any Lifecycle.After name is not in the Registry
// or if a cycle is detected. The error's typed value can be checked
// with errors.As for caller-side policy (e.g. abort vs. fall back to
// ActivationOrder).
//
// Empty Registry returns (nil, nil). Single-element Registry returns
// ([name], nil).
func RestartOrder() ([]string, error) {
	if len(Registry) == 0 {
		return nil, nil
	}

	// name → slice-index. Built once; used for tiebreaker + to map
	// After strings back to indices.
	index := make(map[string]int, len(Registry))
	for i, entry := range Registry {
		index[entry.Name] = i
	}

	// indegree[name] = how many distinct registered daemons the
	// entry's Lifecycle.After names. Edges are stored as a set so a
	// future registry edit that double-lists the same dependency
	// doesn't double-count.
	indegree := make(map[string]int, len(Registry))
	// deps[name] = the set of registered daemons this daemon depends on.
	deps := make(map[string]map[string]struct{}, len(Registry))
	for _, entry := range Registry {
		names := make(map[string]struct{}, len(entry.Lifecycle.After))
		for _, after := range entry.Lifecycle.After {
			_, ok := index[after]
			if !ok {
				return nil, &ErrUnknownDependency{Daemon: entry.Name, DependsOn: after}
			}
			if after == entry.Name {
				return nil, &ErrUnknownDependency{Daemon: entry.Name, DependsOn: after + " (self)"}
			}
			names[after] = struct{}{}
		}
		deps[entry.Name] = names
		indegree[entry.Name] = len(names)
	}

	// Kahn's: seed with zero-indegree (i.e. no After dependencies).
	// Tiebreaker: process the lowest-slice-index first.
	ready := make([]string, 0, len(Registry))
	for _, entry := range Registry {
		if indegree[entry.Name] == 0 {
			ready = append(ready, entry.Name)
		}
	}
	sortStableByIndex(ready, index)

	out := make([]string, 0, len(Registry))
	processed := 0
	for len(ready) > 0 {
		// pop the lowest-index name.
		head := ready[0]
		ready = ready[1:]
		out = append(out, head)
		processed++

		// decrement the indegree of every daemon that lists `head`
		// in its After. We don't pre-build a reverse adjacency; the
		// O(n²) full scan is fine at n=8 — the Registry is hand-
		// curated and is not going to blow the buffer. If a future
		// expansion pushes n up (cross-cluster orchestration), this
		// is the obvious thing to memoize.
		changed := false
		for _, entry := range Registry {
			d := deps[entry.Name]
			_, dependsOnHead := d[head]
			if !dependsOnHead {
				continue
			}
			indegree[entry.Name]--
			if indegree[entry.Name] == 0 {
				ready = append(ready, entry.Name)
			}
			changed = true
		}
		if changed {
			sortStableByIndex(ready, index)
		}
	}

	if processed != len(Registry) {
		// A cycle left some daemons with indegree > 0. Collect them
		// for the caller. Order doesn't matter — the caller is going
		// to abort.
		remaining := make([]string, 0, len(Registry)-processed)
		for _, entry := range Registry {
			if indegree[entry.Name] > 0 {
				remaining = append(remaining, entry.Name)
			}
		}
		return nil, &ErrCycle{Partial: out, Back: remaining}
	}
	return out, nil
}

// sortStableByIndex is the deterministic tiebreaker: when two daemons
// are both in the ready set, the one with the lower Registry slice
// index goes first. Stable sort preserves Go's order tiebreak so two
// daemons at equal depth with a stable sort pick the input order —
// which here is Registry slice order anyway, but the explicit Index
// lookup makes the contract auditable from the function name.
func sortStableByIndex(names []string, index map[string]int) {
	sort.SliceStable(names, func(i, j int) bool {
		return index[names[i]] < index[names[j]]
	})
}

// IsCycle returns true if err is or wraps *ErrCycle. Convenience for
// the deployctl runtime so the call site reads:
//
//	order, err := daemonunitspec.RestartOrder()
//	if daemonunitspec.IsCycle(err) { /* abort */ }
func IsCycle(err error) bool {
	var c *ErrCycle
	return errors.As(err, &c)
}

// IsUnknownDependency returns true if err is or wraps *ErrUnknownDependency.
func IsUnknownDependency(err error) bool {
	var u *ErrUnknownDependency
	return errors.As(err, &u)
}

// RestartDepth returns each registered daemon's depth in the Lifecycle.After
// graph: 0 for a daemon with no registered dependency, otherwise one more
// than its deepest dependency. Two daemons of equal depth have no ordering
// path between them, so deployctl may restart them together
// (docs/adr/20261122170000-continuous-unit-lane.md, "Parallel activation").
func RestartDepth() (map[string]int, error) {
	order, err := RestartOrder()
	if err != nil {
		return nil, err
	}
	after := make(map[string][]string, len(Registry))
	for _, entry := range Registry {
		after[entry.Name] = entry.Lifecycle.After
	}
	depth := make(map[string]int, len(order))
	// RestartOrder puts every daemon after its dependencies, so one pass
	// in that order sees each dependency's depth first.
	for _, name := range order {
		d := 0
		for _, dep := range after[name] {
			if depth[dep]+1 > d {
				d = depth[dep] + 1
			}
		}
		depth[name] = d
	}
	return depth, nil
}
