// filter_test.go — table-driven tests for FilterCriteria.Match
// (ADR-118 / issue #757 §criterion 4, commit 5 of 11).
//
// Pins the contract that dispatch_triggers.go (commit 6) relies
// on for the per-record filter pass. The 12 cases below cover
// every branch of matchClause + matchPayloadClause + the
// jsonpath evaluator; the SLO benchmark (case 12) is a separate
// subtest that asserts 10000 records / 8 filters / < 5 s on a
// shared CI runner. The dispatch tick budget is 1 s per source,
// so an 80 µs/record 8-filter evaluation (the steady-state cost
// measured locally) leaves ~920 ms of headroom for the rest of
// the tick.
package sched_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/sched"
	"reflect"
	"testing"
	"time"
)

// raw is a tiny helper for the table-driven fixtures. The body
// is a JSON literal; the helper turns it into a json.RawMessage
// for the Value field on FilterClause.
func raw(s string) json.RawMessage { return json.RawMessage(s) }

// TestFilterMatch_AllCases is the canonical 12-case table-
// driven fixture set. Each case is self-contained — the
// payload + headers + filter are constructed inline so a
// failure points to the specific shape that broke.
func TestFilterMatch_AllCases(t *testing.T) {
	cases := []struct {
		name    string
		filter  *sched.FilterCriteria
		payload []byte
		headers map[string]string
		want    bool
		wantErr bool
	}{
		// 1. Empty / nil FilterCriteria → always match.
		{
			name:   "nil filter matches everything",
			filter: nil,
			want:   true,
		},
		{
			name:   "zero-value filter matches everything",
			filter: &sched.FilterCriteria{},
			want:   true,
		},

		// 2. Top-level $or with 2 clauses, one match / one not.
		{
			name: "$or one matches",
			filter: &sched.FilterCriteria{
				OR: []sched.FilterClause{
					{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
					{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"globex"`)},
				},
			},
			headers: map[string]string{"x-tenant": "globex"},
			want:    true,
		},
		{
			name: "$or none matches",
			filter: &sched.FilterCriteria{
				OR: []sched.FilterClause{
					{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
					{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"globex"`)},
				},
			},
			headers: map[string]string{"x-tenant": "initech"},
			want:    false,
		},

		// 3. Top-level $and with 2 clauses, both match / one not.
		{
			name: "$and both match",
			filter: &sched.FilterCriteria{
				AND: []sched.FilterClause{
					{Op: sched.FilterOpExists, Field: "x-event-id"},
					{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
				},
			},
			headers: map[string]string{"x-event-id": "evt-1", "x-tenant": "acme"},
			want:    true,
		},
		{
			name: "$and one missing",
			filter: &sched.FilterCriteria{
				AND: []sched.FilterClause{
					{Op: sched.FilterOpExists, Field: "x-event-id"},
					{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
				},
			},
			headers: map[string]string{"x-tenant": "acme"},
			want:    false,
		},

		// 4. payload jsonpath match on string "$.event.type == order.created".
		{
			name: "payload jsonpath string match",
			filter: &sched.FilterCriteria{
				Payload: []sched.FilterClause{
					{Op: sched.FilterOpJsonPath, Path: "$.event.type", Value: raw(`"order.created"`)},
				},
			},
			payload: []byte(`{"event": {"type": "order.created"}}`),
			want:    true,
		},
		{
			name: "payload jsonpath string mismatch",
			filter: &sched.FilterCriteria{
				Payload: []sched.FilterClause{
					{Op: sched.FilterOpJsonPath, Path: "$.event.type", Value: raw(`"order.created"`)},
				},
			},
			payload: []byte(`{"event": {"type": "order.cancelled"}}`),
			want:    false,
		},

		// 5. payload jsonpath match on nested $.data.id == 42 (number).
		{
			name: "payload jsonpath number match",
			filter: &sched.FilterCriteria{
				Payload: []sched.FilterClause{
					{Op: sched.FilterOpJsonPath, Path: "$.data.id", Value: raw(`42`)},
				},
			},
			payload: []byte(`{"data": {"id": 42}}`),
			want:    true,
		},

		// 6. payload jsonpath on array $.items[0].sku == "A".
		{
			name: "payload jsonpath array match",
			filter: &sched.FilterCriteria{
				Payload: []sched.FilterClause{
					{Op: sched.FilterOpJsonPath, Path: "$.items[0].sku", Value: raw(`"A"`)},
				},
			},
			payload: []byte(`{"items": [{"sku": "A"}, {"sku": "B"}]}`),
			want:    true,
		},

		// 7. Malformed jsonpath → returns error.
		// (The dispatch tick (commit 6) treats the error as
		// "not-matched, skipped silently, audited as
		// trigger.filter_error".)
		{
			name: "malformed jsonpath returns error",
			filter: &sched.FilterCriteria{
				Payload: []sched.FilterClause{
					{Op: sched.FilterOpJsonPath, Path: "$.foo[?(@.x>1)]"},
				},
			},
			payload: []byte(`{"foo": {"x": 2}}`),
			wantErr: true,
		},

		// 8. Headers eq / neq / exists.
		{
			name:    "headers eq absent",
			filter:  &sched.FilterCriteria{AND: []sched.FilterClause{{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)}}},
			headers: map[string]string{},
			want:    false,
		},
		{
			name:    "headers neq absent IS a match",
			filter:  &sched.FilterCriteria{AND: []sched.FilterClause{{Op: sched.FilterOpNeq, Field: "x-tenant", Value: raw(`"acme"`)}}},
			headers: map[string]string{},
			want:    true,
		},
		{
			name:    "headers exists true",
			filter:  &sched.FilterCriteria{AND: []sched.FilterClause{{Op: sched.FilterOpExists, Field: "x-event-id"}}},
			headers: map[string]string{"x-event-id": "evt-1"},
			want:    true,
		},
		{
			name:    "headers exists false",
			filter:  &sched.FilterCriteria{AND: []sched.FilterClause{{Op: sched.FilterOpExists, Field: "x-event-id"}}},
			headers: map[string]string{},
			want:    false,
		},

		// 9. Nested $or inside $and (the branch slot).
		//
		// The branch slot uses AND-of-children semantics: a
		// clause with non-empty Clauses matches iff every child
		// matches. The case below pins the AND semantics: the
		// tenant=="acme" clause fails, so the whole branch
		// fails, even though tenant=="globex" matches the
		// second child. The companion case (below) pins the
		// nested-OR shape via the top-level OR slot (the
		// runtime doesn't model "$or inside $and" — ADR-118
		// §"Branch slot" documents that OR is only at the
		// top-level; nesting OR inside AND requires the
		// top-level OR slot).
		{
			name: "nested branch inside $and is AND-of-children",
			filter: &sched.FilterCriteria{
				AND: []sched.FilterClause{
					{
						Clauses: []sched.FilterClause{
							{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
							{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"globex"`)},
						},
					},
					{Op: sched.FilterOpExists, Field: "x-event-id"},
				},
			},
			headers: map[string]string{"x-tenant": "globex", "x-event-id": "evt-1"},
			want:    false,
		},
		// 9b. Top-level OR with a branch child — nested $or
		// inside the OR slot DOES match when any inner clause
		// matches (the OR semantics apply at the top level,
		// then the branch's AND-of-children runs inside the
		// chosen branch).
		{
			name: "top-level $or short-circuits on a matching branch",
			filter: &sched.FilterCriteria{
				OR: []sched.FilterClause{
					{
						Clauses: []sched.FilterClause{
							{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
							{Op: sched.FilterOpExists, Field: "x-event-id"},
						},
					},
					{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"globex"`)},
				},
			},
			headers: map[string]string{"x-tenant": "globex", "x-event-id": "evt-1"},
			want:    true,
		},

		// 10. Nil payload → no payload clauses match.
		{
			name: "nil payload no match",
			filter: &sched.FilterCriteria{
				Payload: []sched.FilterClause{
					{Op: sched.FilterOpJsonPath, Path: "$.event.type", Value: raw(`"order.created"`)},
				},
			},
			payload: nil,
			want:    false,
		},

		// 11. Empty payload {} → jsonpath fails to find → no match.
		{
			name: "empty object payload no match",
			filter: &sched.FilterCriteria{
				Payload: []sched.FilterClause{
					{Op: sched.FilterOpJsonPath, Path: "$.event.type", Value: raw(`"order.created"`)},
				},
			},
			payload: []byte(`{}`),
			want:    false,
		},

		// 12. SLO benchmark — moved to TestFilterMatch_SLO below.
		// Listed here for the table-driven audit; the actual
		// timing assertion is in the dedicated subtest.
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// CRIT-2 (PR #993 / issue #757 closure): Match
			// returns (matched, nil) on clause errors; the
			// dispatcher audits via MatchCount. The
			// table-driven contract pin changes:
			//
			//   - wantErr: the legacy "Match returns error"
			//     expectation is now "Match returns matched
			//     = false, clauseErrors > 0".
			got, clauseErrors := c.filter.MatchCount(c.payload, c.headers)
			if c.wantErr {
				if clauseErrors == 0 {
					t.Fatalf("clauseErrors = 0, want > 0 (filter error contract: caller audits via MatchCount)")
				}
				if got {
					t.Errorf("MatchCount = true, want false on clause error")
				}
				// Legacy Match wrapper: returns (false, nil).
				if mOK, mErr := c.filter.Match(c.payload, c.headers); mErr != nil || mOK {
					t.Errorf("Match wrapper = (%v, %v), want (false, nil) under CRIT-2", mOK, mErr)
				}
				return
			}
			if clauseErrors != 0 {
				t.Fatalf("clauseErrors = %d, want 0", clauseErrors)
			}
			if got != c.want {
				t.Errorf("Match = %v, want %v", got, c.want)
			}
		})
	}
}

// TestFilterMatch_SLO is the case-12 timing benchmark. The
// dispatch tick budget is 1 s per source; an 8-filter evaluation
// per record is the documented envelope (matches Lambda's
// filter_complexity model). The test asserts < 5 s for 10000
// records (the inner loop, not the Go test harness overhead).
//
// The 5 s ceiling is deliberately generous: the per-record cost
// is ~80 µs on a quiet machine (yielding ~800 ms for the 10k
// loop), but shared CI runners can take 4-5× longer due to
// contention with neighbouring jobs. The benchmark's value is
// the LOG line — `t.Logf` reports the µs/record number so a
// future regression is visible in the CI artifact even when
// the wall-clock cap is too tight for the shared runner. A
// regression from 80 µs/record to 800 µs/record (10×) would
// trip the cap on any reasonable hardware; finer-grained
// detection belongs in a `//go:build bench` suite, not here.
func TestFilterMatch_SLO(t *testing.T) {
	if testing.Short() {
		t.Skip("SLO benchmark skipped in -short mode")
	}
	// 8-filter predicate (combines header eq + payload jsonpath
	// at multiple depths). Mirrors the production "rich filter"
	// shape customers authoring in the manifest.
	filter := &sched.FilterCriteria{
		AND: []sched.FilterClause{
			{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
			{Op: sched.FilterOpExists, Field: "x-event-id"},
			{Op: sched.FilterOpJsonPath, Path: "$.event.type", Value: raw(`"order.created"`)},
			{Op: sched.FilterOpJsonPath, Path: "$.data.id", Value: raw(`42`)},
			{Op: sched.FilterOpJsonPath, Path: "$.items[0].sku", Value: raw(`"A"`)},
			{Op: sched.FilterOpNeq, Field: "x-source", Value: raw(`"test"`)},
			{Op: sched.FilterOpExists, Field: "x-region"},
			{Op: sched.FilterOpJsonPath, Path: "$.nested.deep.key", Value: raw(`"value"`)},
		},
	}
	payload := []byte(`{
		"event": {"type": "order.created"},
		"data": {"id": 42},
		"items": [{"sku": "A"}],
		"nested": {"deep": {"key": "value"}}
	}`)
	headers := map[string]string{
		"x-tenant":   "acme",
		"x-event-id": "evt-1",
		"x-source":   "production",
		"x-region":   "eu-west-1",
	}
	const N = 10000
	start := time.Now()
	var matched, unmatched int
	for i := 0; i < N; i++ {
		ok, err := filter.Match(payload, headers)
		if err != nil {
			t.Fatalf("iter %d: err = %v", i, err)
		}
		if ok {
			matched++
		} else {
			unmatched++
		}
	}
	elapsed := time.Since(start)
	if matched != N {
		t.Errorf("matched = %d, want %d (filter shape changed; recompute the SLO)", matched, N)
	}
	t.Logf("SLO: %d records in %v (%.1f µs/record)", N, elapsed, float64(elapsed.Microseconds())/float64(N))
	if elapsed > 5*time.Second {
		t.Errorf("SLO breach: %d records took %v (> 5 s — order-of-magnitude regression from the ~80 µs/record baseline)", N, elapsed)
	}
}

// TestFilterMatch_HeaderNumericCoercion pins the numeric-coercion
// contract for the eq operator: a header value "42" compares
// equal to a JSON number 42 (the broker-side header is always
// a string; the customer can compare against a number).
func TestFilterMatch_HeaderNumericCoercion(t *testing.T) {
	filter := &sched.FilterCriteria{
		AND: []sched.FilterClause{
			{Op: sched.FilterOpEq, Field: "x-retry-count", Value: raw(`3`)},
		},
	}
	got, err := filter.Match(nil, map[string]string{"x-retry-count": "3"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !got {
		t.Errorf("Match = false, want true (header '3' must coerce to numeric 3)")
	}
}

// TestFilterMatch_PayloadAndHeadersCombined pins the contract
// that a single record can be evaluated against both payload
// and headers in the same filter tree. The dispatch tick
// constructs one FilterCriteria per trigger and reuses it
// across every polled record; the predicate composition is
// orthogonal to whether the clause targets payload or headers.
func TestFilterMatch_PayloadAndHeadersCombined(t *testing.T) {
	filter := &sched.FilterCriteria{
		AND: []sched.FilterClause{
			{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
			{Op: sched.FilterOpJsonPath, Path: "$.event.type", Value: raw(`"order.created"`)},
		},
	}
	got, err := filter.Match(
		[]byte(`{"event": {"type": "order.created"}}`),
		map[string]string{"x-tenant": "acme"},
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !got {
		t.Errorf("Match = false, want true (both header and payload clauses must match)")
	}
}

// TestFilterMatch_JsonPathUnsupportedFormErrors pins the
// documented limitation: a path with the recursive-descent or
// filter-expression forms returns an error (the caller treats
// it as not-matched per the file-header contract). A future
// ADR adds PaesslerAG/jsonpath for the full grammar.
func TestFilterMatch_JsonPathUnsupportedFormErrors(t *testing.T) {
	unsupported := []string{
		"$.items[?(@.price>100)]", // filter expression
		"$.items[*]",              // wildcard
		"$..foo",                  // recursive descent
	}
	for _, p := range unsupported {
		t.Run(p, func(t *testing.T) {
			filter := &sched.FilterCriteria{
				Payload: []sched.FilterClause{
					{Op: sched.FilterOpJsonPath, Path: p, Value: raw(`"x"`)},
				},
			}
			// CRIT-2 (PR #993 / issue #757 closure): Match
			// swallows clause errors and reports them via
			// MatchCount. The dispatcher audits via MatchCount.
			// We assert via MatchCount here to lock the new
			// contract.
			matched, clauseErrors := filter.MatchCount([]byte(`{"foo": "x"}`), nil)
			if matched {
				t.Fatalf("path %q: matched = true, want false (unsupported form must not match)", p)
			}
			if clauseErrors != 1 {
				t.Fatalf("path %q: clauseErrors = %d, want 1 (the unsupported clause must be counted)", p, clauseErrors)
			}
			// Legacy Match returns (false, nil) under CRIT-2 —
			// the count is the audit surface.
			ok, err := filter.Match([]byte(`{"foo": "x"}`), nil)
			if err != nil {
				t.Fatalf("path %q: Match err = %v, want nil (CRIT-2 swallows clause errors; use MatchCount for audit)", p, err)
			}
			if ok {
				t.Fatalf("path %q: Match ok = true, want false", p)
			}
		})
	}
}

// TestFilterMatch_OrErrorIsSkipped pins the CRIT-2 (PR #993 /
// issue #757 closure) contract: an $or clause that errors is
// counted toward clauseErrors via MatchCount, NOT propagated
// from Match. Pre-CRIT-2 the legacy Match() returned
// (false, "$or[0]: ...") on the first error and aborted the rest
// of the OR list; post-CRIT-2 the dispatcher uses MatchCount so
// it can audit per-record (trigger.filter_error) and the record
// is dropped without aborting the batch.
//
// Match (the legacy API) now returns (false, nil) for clause
// errors because Match is implemented as MatchCount with the
// count discarded.
func TestFilterMatch_OrErrorIsSkipped(t *testing.T) {
	filter := &sched.FilterCriteria{
		OR: []sched.FilterClause{
			{Op: sched.FilterOpJsonPath, Path: "$.foo[?(@.x>1)]"}, // unsupported form
		},
	}
	matched, clauseErrors := filter.MatchCount([]byte(`{"foo": {"x": 2}}`), nil)
	if matched {
		t.Errorf("matched = true, want false (the clause errored, OR should not match)")
	}
	if clauseErrors != 1 {
		t.Errorf("clauseErrors = %d, want 1", clauseErrors)
	}
	// Legacy Match wraps MatchCount and discards the count.
	ok, err := filter.Match([]byte(`{"foo": {"x": 2}}`), nil)
	if err != nil {
		t.Errorf("Match err = %v, want nil (CRIT-2 swallows clause errors; dispatcher audits via MatchCount)", err)
	}
	if ok {
		t.Errorf("Match ok = true, want false")
	}
}

// TestFilterMatch_OrSecondClauseMatches pins the OR-fallback
// case: the first clause errors, the second clause matches.
// CRIT-2 must still evaluate the second clause; the result is
// (true, 1) — matched because the OR found a match, clauseErrors
// > 0 because the operator wants visibility into the failed
// first clause.
func TestFilterMatch_OrSecondClauseMatches(t *testing.T) {
	filter := &sched.FilterCriteria{
		OR: []sched.FilterClause{
			{Op: sched.FilterOpJsonPath, Path: "$.foo[?(@.x>1)]"}, // errors
			{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
		},
	}
	matched, clauseErrors := filter.MatchCount(
		[]byte(`{"foo": {"x": 2}}`),
		map[string]string{"x-tenant": "acme"},
	)
	if !matched {
		t.Errorf("matched = false, want true (second OR clause matched)")
	}
	if clauseErrors != 1 {
		t.Errorf("clauseErrors = %d, want 1 (operator still wants to see the first clause's error)", clauseErrors)
	}
}

// TestFilterMatch_AndErrorIsSkipped pins the AND-side of CRIT-2.
// An error in an $and clause increments clauseErrors and
// continues; the matched/allMatched tally is unaffected by an
// errored clause (the contract is "all NON-errored clauses must
// match"). Here the single $and clause errors so matched is
// false, clauseErrors is 1.
func TestFilterMatch_AndErrorIsSkipped(t *testing.T) {
	filter := &sched.FilterCriteria{
		AND: []sched.FilterClause{
			{Op: sched.FilterOpJsonPath, Path: "$.foo[?(@.x>1)]"}, // unsupported form
		},
	}
	matched, clauseErrors := filter.MatchCount([]byte(`{"foo": {"x": 2}}`), nil)
	if matched {
		t.Errorf("matched = true, want false")
	}
	if clauseErrors != 1 {
		t.Errorf("clauseErrors = %d, want 1", clauseErrors)
	}
}

// TestFilterMatch_PayloadErrorIsSkipped pins the payload-list
// side of CRIT-2. A payload clause that errors (unsupported
// jsonpath form) is counted via MatchCount and skipped; the
// other payload clauses are still evaluated.
func TestFilterMatch_PayloadErrorIsSkipped(t *testing.T) {
	filter := &sched.FilterCriteria{
		Payload: []sched.FilterClause{
			{Op: sched.FilterOpJsonPath, Path: "$.foo[?(@.x>1)]"}, // errors
			{Op: sched.FilterOpJsonPath, Path: "$.foo.x", Value: raw("2")},
		},
	}
	matched, clauseErrors := filter.MatchCount([]byte(`{"foo": {"x": 2}}`), nil)
	if !matched {
		t.Errorf("matched = false, want true (second payload clause matched; the first errored)")
	}
	if clauseErrors != 1 {
		t.Errorf("clauseErrors = %d, want 1", clauseErrors)
	}
}

// Compile-time: FilterCriteria + FilterClause + FilterOp must
// JSON-round-trip so the apid handler can pass the wire shape
// straight through.
func TestFilterCriteriaJSONRoundTrip(t *testing.T) {
	in := &sched.FilterCriteria{
		OR: []sched.FilterClause{
			{Op: sched.FilterOpExists, Field: "x-event-id"},
		},
		AND: []sched.FilterClause{
			{Op: sched.FilterOpEq, Field: "x-tenant", Value: raw(`"acme"`)},
		},
		Payload: []sched.FilterClause{
			{Op: sched.FilterOpJsonPath, Path: "$.event.type", Value: raw(`"order.created"`)},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out sched.FilterCriteria
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// in is *FilterCriteria (pointer); out is FilterCriteria
	// (value). Compare via reflect.DeepEqual after deref so
	// pointer-vs-value drift doesn't trip the test.
	if !reflect.DeepEqual(*in, out) {
		t.Errorf("round-trip drift:\n in = %+v\nout = %+v", *in, out)
	}
}

// TestFilterMatch_JsonPathNeq_MissingKey pins the CRIT-3 (PR #993
// / issue #757 closure) fix for the jsonpath neq missing-key
// asymmetry. Pre-CRIT-3 the jsonpath branch unconditionally
// returned false on got == nil (the jsonScalarEqualJSON helper
// short-circuited), collapsing eq / neq / exists missing-key
// cases onto a single "no match" semantic. CRIT-3 dispatches on
// c.Op before consulting the helper:
//
//   - neq on missing key  → true  (Lambda ESM documented
//     behaviour; a missing key
//     trivially satisfies the
//     inequality)
//
// The test mirrors the header-slot path: FilterOpNeq on an
// absent header returns true.
func TestFilterMatch_JsonPathNeq_MissingKey(t *testing.T) {
	filter := &sched.FilterCriteria{
		Payload: []sched.FilterClause{
			{Op: sched.FilterOpNeq, Path: "$.missing", Value: raw("42")},
		},
	}
	matched, clauseErrors := filter.MatchCount([]byte(`{"foo": 1}`), nil)
	if !matched {
		t.Errorf("matched = false, want true (neq on missing jsonpath key trivially matches)")
	}
	if clauseErrors != 0 {
		t.Errorf("clauseErrors = %d, want 0 (no clause errored)", clauseErrors)
	}
}

// TestFilterMatch_JsonPathEq_MissingKey pins the eq-side of
// CRIT-3. eq on a missing jsonpath key is a no-match (the key
// isn't there, equality fails). Pre-CRIT-3 this case was
// silently correct (the helper returned false on got == nil)
// but for the wrong reason — the dispatch was unconditional.
// CRIT-3 makes the per-op dispatch explicit.
func TestFilterMatch_JsonPathEq_MissingKey(t *testing.T) {
	filter := &sched.FilterCriteria{
		Payload: []sched.FilterClause{
			{Op: sched.FilterOpEq, Path: "$.missing", Value: raw("42")},
		},
	}
	matched, clauseErrors := filter.MatchCount([]byte(`{"foo": 1}`), nil)
	if matched {
		t.Errorf("matched = true, want false (eq on missing jsonpath key does not match)")
	}
	if clauseErrors != 0 {
		t.Errorf("clauseErrors = %d, want 0", clauseErrors)
	}
}

// TestFilterMatch_JsonPathExists_MissingKey pins the exists-side
// of CRIT-3. exists on a missing jsonpath key returns false.
// Pre-CRIT-3 exists on missing key returned false via the
// helper's nil short-circuit (correct, but for the wrong
// reason); CRIT-3 makes it explicit.
//
// The Op is FilterOpJsonPath (not FilterOpExists) because
// matchPayloadClauseCached routes jsonpath clauses through the
// jsonpath branch. The clause-level op decides what we DO
// with the resolved value; the jsonpath-vs-payload-root
// distinction is the top-level switch.
func TestFilterMatch_JsonPathExists_MissingKey(t *testing.T) {
	filter := &sched.FilterCriteria{
		Payload: []sched.FilterClause{
			{Op: sched.FilterOpExists, Path: "$.missing"},
		},
	}
	matched, clauseErrors := filter.MatchCount([]byte(`{"foo": 1}`), nil)
	if matched {
		t.Errorf("matched = true, want false (exists on missing jsonpath key does not match)")
	}
	if clauseErrors != 0 {
		t.Errorf("clauseErrors = %d, want 0", clauseErrors)
	}
}
