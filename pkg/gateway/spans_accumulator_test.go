// spans_accumulator_test.go — ADR-127 PR-D code-review #4.
//
// Verifies that buckets are scoped by (account, trace): spans
// of one account never land in another account's bucket, and
// two accounts sharing a trace ID keep separate buckets.

package gateway

import (
	"testing"

	"github.com/google/uuid"
)

func TestSpansAccumulator_Add_SameAccountCoalesces(t *testing.T) {
	s := NewSpansAccumulator()
	id := uuid.New()
	tid := "00000000000000000000000000000001"
	spans := []summarizedSpan{{TraceID: tid, SpanID: "aa", EndTimeUnixNano: 1}}

	n, err := s.Add(tid, id, spans)
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if n != 1 {
		t.Errorf("first Add n = %d, want 1", n)
	}
	// Same trace_id, same account — must succeed (the dedupe
	// set will drop the duplicate span, but no error).
	n, err = s.Add(tid, id, nil)
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if n != 0 {
		t.Errorf("second Add n = %d, want 0 (dedupe)", n)
	}
}

// Two accounts can carry one W3C trace ID (a caller forwards its traceparent
// to another account's app). Each keeps its own spans: the first account to
// send a trace used to own it, and the second account's spans were rejected.
func TestSpansAccumulator_Add_AccountsShareATraceID(t *testing.T) {
	s := NewSpansAccumulator()
	acctA := uuid.New()
	acctB := uuid.New()
	tid := "00000000000000000000000000000002"

	if _, err := s.Add(tid, acctA, []summarizedSpan{{TraceID: tid, SpanID: "aa", EndTimeUnixNano: 1}}); err != nil {
		t.Fatalf("Add(acctA): %v", err)
	}
	if _, err := s.Add(tid, acctB, []summarizedSpan{{TraceID: tid, SpanID: "bb", EndTimeUnixNano: 1}}); err != nil {
		t.Fatalf("Add(acctB) on the same trace: %v", err)
	}
	a, gotA := s.DrainAndRemove(tid, acctA)
	b, gotB := s.DrainAndRemove(tid, acctB)
	if gotA != acctA || len(a) != 1 || a[0].SpanID != "aa" {
		t.Fatalf("account A drained %+v for %s", a, gotA)
	}
	if gotB != acctB || len(b) != 1 || b[0].SpanID != "bb" {
		t.Fatalf("account B drained %+v for %s", b, gotB)
	}
}
