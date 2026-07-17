package state

// Property-based tests for the instance state machine (spec §6.1) and its tie to
// the RAM/concurrency invariants (§6.2). schedd consults this graph before every
// write, so the machine's structural properties ARE correctness guarantees:
// closure (no edge escapes the known state set), reachability (every state is
// reachable from PARKED — no orphan states schedd could get stuck in or never
// reach), and the counting consistency the admission ledger relies on.
//
// CLAUDE.md: "Invariants — enforce with property-based tests, never delete."

import (
	"testing"
)

// TestTransitionGraphIsClosedAndConsistent checks structural properties over the
// whole graph (exhaustive, not sampled — the state set is tiny).
func TestTransitionGraphIsClosedAndConsistent(t *testing.T) {
	for from, tos := range transitions {
		if !from.Valid() {
			t.Errorf("source state %q not in transitions map", from)
		}
		for _, to := range tos {
			// Closure: every successor must itself be a known state, or schedd
			// could write a value the DB CHECK constraint rejects.
			if !to.Valid() {
				t.Errorf("edge %q→%q lands on an unknown state", from, to)
			}
			// CanTransition must agree with the raw edge set.
			if !CanTransition(from, to) {
				t.Errorf("edge %q→%q present but CanTransition returned false", from, to)
			}
		}
	}

	// States and the transitions map must describe exactly the same state set.
	if len(States) != len(transitions) {
		t.Fatalf("States has %d entries, transitions has %d", len(States), len(transitions))
	}
	for _, s := range States {
		if _, ok := transitions[s]; !ok {
			t.Errorf("state %q listed in States but absent from transitions", s)
		}
	}
}

// TestCountingConsistency asserts the ledger-facing predicates over every state:
// the RAM-counting set is a superset of the concurrency-counting set (an instance
// that occupies a concurrency slot is by definition resident), and PARKED counts
// for neither — a parked app holds zero resident RAM (§6.2-4).
func TestCountingConsistency(t *testing.T) {
	for _, s := range States {
		if s.CountsForConcurrency() && !s.CountsForRAM() {
			t.Errorf("state %q counts for concurrency but not RAM — impossible", s)
		}
	}
	if StateParked.CountsForRAM() || StateParked.CountsForConcurrency() {
		t.Error("PARKED must count for neither RAM nor concurrency (§6.2-4)")
	}
	// Terminal-cold states hold nothing either.
	for _, s := range []State{StateStopped, StateFailed} {
		if s.CountsForRAM() || s.CountsForConcurrency() {
			t.Errorf("cold state %q must not count toward RAM/concurrency", s)
		}
	}
}

// TestEveryStateReachableFromParked proves the graph has no orphan: from a
// freshly-deployed app's entry point (PARKED) a legal path exists to every state.
func TestEveryStateReachableFromParked(t *testing.T) {
	seen := map[State]bool{StateParked: true}
	queue := []State{StateParked}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range transitions[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	for _, s := range States {
		if !seen[s] {
			t.Errorf("state %q is unreachable from PARKED", s)
		}
	}
}

// FuzzStateWalkStaysLegal random-walks the graph using the fuzz bytes to pick an
// outgoing edge at each step. Every visited state must be Valid, every step must
// be a legal transition, and the counting invariants must hold at each state.
// This catches any future edit that introduces an edge into a bad state or breaks
// the RAM⊇concurrency relationship.
func FuzzStateWalkStaysLegal(f *testing.F) {
	f.Add([]byte{0, 1, 0, 1, 2, 0})
	f.Add([]byte{255, 128, 64, 32, 16, 8, 4, 2, 1})

	f.Fuzz(func(t *testing.T, choices []byte) {
		cur := StateParked
		for i, c := range choices {
			if !cur.Valid() {
				t.Fatalf("step %d: walked into invalid state %q", i, cur)
			}
			// Counting invariants hold at every state we visit.
			if cur.CountsForConcurrency() && !cur.CountsForRAM() {
				t.Fatalf("step %d: state %q counts concurrency but not RAM", i, cur)
			}

			outs := transitions[cur]
			if len(outs) == 0 {
				// A sink under this walk; restart from PARKED to keep exploring.
				cur = StateParked
				continue
			}
			next := outs[int(c)%len(outs)]
			if !CanTransition(cur, next) {
				t.Fatalf("step %d: chosen edge %q→%q rejected by CanTransition", i, cur, next)
			}
			cur = next
		}
	})
}
