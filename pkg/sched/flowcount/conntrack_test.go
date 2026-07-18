// Tests for the conntrack-backed flow counter. The parser is exercised
// directly against canned conntrack -L output (the part most likely to break
// on Ubuntu updates); the Reader is exercised end-to-end with a fakeRunner so
// we can pin TTL behavior, fail-open semantics, and the warm-list reindex
// without shelling out.

package flowcount

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// fakeRunner returns canned conntrack output (or an error) and records how
// many times it was invoked. Used by the Reader tests below.
type fakeRunner struct {
	out   []byte
	err   error
	calls atomic.Int32
}

func (f *fakeRunner) Output(_ context.Context, _ []string) ([]byte, error) {
	f.calls.Add(1)
	return f.out, f.err
}

// cannedConntrack is a realistic conntrack -L -p tcp -n snippet with one
// flow per (src,dst) combination the tests need.
const cannedConntrack = `tcp      6 431999 ESTABLISHED src=10.100.0.5 dst=93.184.216.34 sport=42301 dport=443 [ASSURED] src=93.184.216.34 dst=10.100.0.5 sport=443 dport=42301
tcp      6 120 TIME_WAIT src=10.100.0.5 dst=93.184.216.34 sport=42302 dport=80 [ASSURED] src=93.184.216.34 dst=10.100.0.5 sport=80 dport=42302
tcp      6 431999 ESTABLISHED src=10.100.0.7 dst=8.8.8.8 sport=51234 dport=53 src=8.8.8.8 dst=10.100.0.7 sport=53 dport=51234
tcp      6 431999 ESTABLISHED src=10.100.0.5 dst=10.100.0.6 sport=6000 dport=7000 src=10.100.0.6 dst=10.100.0.5 sport=7000 dport=6000
`

// makeInstances builds a warm list with the given IP -> ID pairs. Empty IPs
// are skipped (the production path skips WAKING instances without a veth).
func makeInstances(pairs ...[2]string) []state.Instance {
	out := make([]state.Instance, 0, len(pairs))
	for _, p := range pairs {
		ip, id := p[0], p[1]
		if ip == "" {
			continue
		}
		out = append(out, state.Instance{ID: id, HostIP: ip})
	}
	return out
}

func TestParseConntrack_CountsBidirectionalFlows(t *testing.T) {
	// Canned output has four lines. The parser finds every src= and dst=
	// token per line (including the reply-direction tuple after [ASSURED])
	// and increments for each one that matches a known host.
	//
	//   Line 1: ESTABLISHED, 10.100.0.5 <-> 93.184.216.34 — inst-A matches
	//           twice (src= + dst=) = +2
	//   Line 2: TIME_WAIT, same pair — inst-A +2
	//   Line 3: ESTABLISHED, 10.100.0.7 <-> 8.8.8.8 — inst-B +2
	//   Line 4: ESTABLISHED, 10.100.0.5 <-> 10.100.0.6 — inst-A +2, inst-C +2
	//
	// Expected: inst-A=6, inst-B=2, inst-C=2.
	idx := map[string]string{
		"10.100.0.5": "inst-A",
		"10.100.0.7": "inst-B",
		"10.100.0.6": "inst-C",
	}
	counts := parseConntrack([]byte(cannedConntrack), idx)
	want := map[string]int64{
		"inst-A": 6,
		"inst-B": 2,
		"inst-C": 2,
	}
	for id, w := range want {
		if got := counts[id]; got != w {
			t.Errorf("counts[%q] = %d, want %d", id, got, w)
		}
	}
	if len(counts) != len(want) {
		t.Errorf("unexpected extra entries: %v", counts)
	}
}

func TestParseConntrack_EmptyInputs(t *testing.T) {
	if got := parseConntrack(nil, map[string]string{"10.0.0.1": "x"}); len(got) != 0 {
		t.Errorf("empty input should produce empty counts, got %v", got)
	}
	if got := parseConntrack([]byte("tcp 6 1 ESTABLISHED src=10.0.0.1 dst=2.2.2.2"), nil); len(got) != 0 {
		t.Errorf("empty hostIndex should produce empty counts, got %v", got)
	}
}

func TestParseConntrack_TolerantOfUnknownLines(t *testing.T) {
	// Mixed bag: a valid line, a malformed line, and an empty line. The
	// parser must not panic on malformed input; valid tokens are still
	// counted.
	data := "tcp 6 1 ESTABLISHED src=10.100.0.5 dst=1.1.1.1 sport=1 dport=80\n" +
		"garbage that conntrack would never emit\n" +
		"\n"
	idx := map[string]string{"10.100.0.5": "A"}
	counts := parseConntrack([]byte(data), idx)
	// One valid line, one src=10.100.0.5 hit + one dst= (1.1.1.1, miss) = +1.
	if counts["A"] != 1 {
		t.Errorf("counts[A] = %d, want 1 (only the valid line should increment)", counts["A"])
	}
}

func TestExtractAllAddrs(t *testing.T) {
	tests := []struct {
		line, marker string
		want         []string
	}{
		// conntrack's reply-direction tuple after [ASSURED] is included —
		// the parser counts a flow in both directions.
		{"tcp 6 1 ESTABLISHED src=10.0.0.1 dst=1.1.1.1 [ASSURED] src=1.1.1.1 dst=10.0.0.1", "src=", []string{"10.0.0.1", "1.1.1.1"}},
		{"src=10.0.0.1 dst=1.1.1.1", "dst=", []string{"1.1.1.1"}},
		{"src=10.0.0.1 [ASSURED] mark=0", "src=", []string{"10.0.0.1"}},
		{"tcp 6 1 src=10.0.0.1 dst=1.1.1.1 sport=80", "sport=", []string{"80"}},
		{"no markers here", "src=", nil},
		{"", "src=", nil},
	}
	for _, tt := range tests {
		got := extractAllAddrs([]byte(tt.line), tt.marker)
		if !equalStringSlice(got, tt.want) {
			t.Errorf("extractAllAddrs(%q, %q) = %v, want %v", tt.line, tt.marker, got, tt.want)
		}
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReader_OpenReturnsCountAfterWarm(t *testing.T) {
	runner := &fakeRunner{out: []byte(cannedConntrack)}
	r := NewReader(runner, WithTTL(time.Hour))
	insts := makeInstances(
		[2]string{"10.100.0.5", "inst-A"},
		[2]string{"10.100.0.7", "inst-B"},
	)
	if err := r.Warm(context.Background(), insts); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	if got, _ := r.Open(context.Background(), "inst-A"); got != 6 {
		t.Errorf("inst-A count = %d, want 6", got)
	}
	if got, _ := r.Open(context.Background(), "inst-B"); got != 2 {
		t.Errorf("inst-B count = %d, want 2", got)
	}
	if calls := runner.calls.Load(); calls != 1 {
		t.Errorf("runner calls = %d, want 1", calls)
	}
}

func TestReader_OpenOnUnknownInstanceReturnsZero(t *testing.T) {
	runner := &fakeRunner{out: []byte(cannedConntrack)}
	r := NewReader(runner, WithTTL(time.Hour))
	if err := r.Warm(context.Background(), makeInstances([2]string{"10.100.0.5", "inst-A"})); err != nil {
		t.Fatalf("Warm: %v", err)
	}
	got, err := r.Open(context.Background(), "never-seen")
	if err != nil {
		t.Errorf("Open on unknown instance should not error, got %v", err)
	}
	if got != 0 {
		t.Errorf("Open on unknown instance = %d, want 0", got)
	}
}

func TestReader_OpenBeforeWarmReturnsZeroNoError(t *testing.T) {
	runner := &fakeRunner{out: []byte(cannedConntrack)}
	r := NewReader(runner, WithTTL(time.Hour))
	got, err := r.Open(context.Background(), "anything")
	if err != nil {
		t.Errorf("Open before Warm should not error, got %v", err)
	}
	if got != 0 {
		t.Errorf("Open before Warm = %d, want 0", got)
	}
	if calls := runner.calls.Load(); calls != 0 {
		t.Errorf("runner should not have been called (Warm hadn't run), calls = %d", calls)
	}
}

func TestReader_CacheTTLTriggersRefresh(t *testing.T) {
	runner := &fakeRunner{out: []byte(cannedConntrack)}
	r := NewReader(runner, WithTTL(10*time.Millisecond))
	insts := makeInstances([2]string{"10.100.0.5", "inst-A"})

	if err := r.Warm(context.Background(), insts); err != nil {
		t.Fatalf("Warm #1: %v", err)
	}
	// Second Warm within TTL: must NOT re-exec, just reindex.
	if err := r.Warm(context.Background(), insts); err != nil {
		t.Fatalf("Warm #2: %v", err)
	}
	if calls := runner.calls.Load(); calls != 1 {
		t.Errorf("calls after second Warm within TTL = %d, want 1", calls)
	}
	// Wait past TTL.
	time.Sleep(20 * time.Millisecond)
	if err := r.Warm(context.Background(), insts); err != nil {
		t.Fatalf("Warm #3: %v", err)
	}
	if calls := runner.calls.Load(); calls != 2 {
		t.Errorf("calls after Warm past TTL = %d, want 2", calls)
	}
}

func TestReader_FailedWarmLatchesUntilSuccess(t *testing.T) {
	runner := &fakeRunner{err: errors.New("conntrack timeout")}
	r := NewReader(runner, WithTTL(time.Hour))
	insts := makeInstances([2]string{"10.100.0.5", "inst-A"})

	warmErr := r.Warm(context.Background(), insts)
	if warmErr == nil {
		t.Fatal("Warm should have errored on fakeRunner err")
	}
	if !strings.Contains(warmErr.Error(), "conntrack timeout") {
		t.Errorf("Warm error should wrap underlying, got %v", warmErr)
	}
	// Open must fail-open: returns (0, err) per the fail-open contract.
	got, err := r.Open(context.Background(), "inst-A")
	if err == nil {
		t.Error("Open after failed Warm should return an error")
	}
	if got != 0 {
		t.Errorf("Open after failed Warm = %d, want 0", got)
	}
	// Subsequent Warm with the same failure must keep the latch: a single
	// mid-tick recovery would silently turn G7 on after a transient blip,
	// which is the wrong behavior — the reaper fails open to LastRequest.
	_ = r.Warm(context.Background(), insts)
	if _, err := r.Open(context.Background(), "inst-A"); err == nil {
		t.Error("Open should still error after repeated Warm failures")
	}

	// Now simulate recovery: fakeRunner starts returning data.
	runner.out = []byte(cannedConntrack)
	runner.err = nil
	if err := r.Warm(context.Background(), insts); err != nil {
		t.Fatalf("Warm after recovery: %v", err)
	}
	got, err = r.Open(context.Background(), "inst-A")
	if err != nil {
		t.Errorf("Open after recovered Warm should not error, got %v", err)
	}
	if got != 6 {
		t.Errorf("Open after recovered Warm = %d, want 6", got)
	}
}

func TestReader_WarmRebuildsHostIndex(t *testing.T) {
	// First Warm: instance A is present. Second Warm: A has parked
	// (no longer in the list), B has woken. Cache must reflect the new
	// warm list on the second call even within TTL, and a previously
	// matching IP that now belongs to nobody must NOT count for the
	// parked instance — that's the slot-reuse case.
	runner := &fakeRunner{out: []byte(cannedConntrack)}
	r := NewReader(runner, WithTTL(time.Hour))
	first := makeInstances([2]string{"10.100.0.5", "inst-A"})
	if err := r.Warm(context.Background(), first); err != nil {
		t.Fatalf("Warm #1: %v", err)
	}
	if got, _ := r.Open(context.Background(), "inst-A"); got != 6 {
		t.Errorf("Warm #1 inst-A = %d, want 6", got)
	}

	second := makeInstances([2]string{"10.100.0.7", "inst-B"})
	if err := r.Warm(context.Background(), second); err != nil {
		t.Fatalf("Warm #2: %v", err)
	}
	// Within TTL: re-parse the cached conntrack output against the new
	// hostIndex. inst-A is no longer in the warm list, so its flows
	// (which still match 10.100.0.5 in the conntrack dump) must NOT be
	// counted against inst-A. This pins the slot-reuse case: a park →
	// wake where the new wake reuses the IP slot would otherwise leak
	// flows to the parked instance. inst-B (10.100.0.7) appears in the
	// conntrack output (line 3) regardless of warm list, so it counts
	// its real flows.
	if got, _ := r.Open(context.Background(), "inst-A"); got != 0 {
		t.Errorf("stale inst-A count after reindex = %d, want 0 (slot reuse)", got)
	}
	if got, _ := r.Open(context.Background(), "inst-B"); got != 2 {
		t.Errorf("inst-B count after reindex = %d, want 2 (still in cached output)", got)
	}
}

func TestReader_NewReaderPanicsOnNilRunner(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewReader(nil) should panic")
		}
	}()
	_ = NewReader(nil)
}

func TestReader_WarmPropagatesContextCancel(t *testing.T) {
	// A cancelled ctx must surface as a Warm error and trigger the
	// failure latch — otherwise the reaper would silently fall back to
	// stale counts on a shutdown-bound tick (the most likely silent-
	// failure mode for the G7 path).
	runner := &fakeRunner{err: context.Canceled}
	r := NewReader(runner, WithTTL(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := r.Warm(ctx, makeInstances([2]string{"10.100.0.5", "inst-A"})); err == nil {
		t.Fatal("Warm should have errored on cancelled ctx")
	}
	if got, err := r.Open(context.Background(), "inst-A"); err == nil || got != 0 {
		t.Errorf("Open after cancelled Warm: got=(%d, %v), want (0, err)", got, err)
	}
}

func TestReader_WarmIdempotent(t *testing.T) {
	// Two back-to-back Warms with the same instance list produce the
	// same counts and re-parse without re-execing (cache fresh).
	runner := &fakeRunner{out: []byte(cannedConntrack)}
	r := NewReader(runner, WithTTL(time.Hour))
	insts := makeInstances(
		[2]string{"10.100.0.5", "inst-A"},
		[2]string{"10.100.0.7", "inst-B"},
	)
	if err := r.Warm(context.Background(), insts); err != nil {
		t.Fatalf("Warm #1: %v", err)
	}
	if err := r.Warm(context.Background(), insts); err != nil {
		t.Fatalf("Warm #2: %v", err)
	}
	if got, _ := r.Open(context.Background(), "inst-A"); got != 6 {
		t.Errorf("inst-A = %d, want 6", got)
	}
	if got, _ := r.Open(context.Background(), "inst-B"); got != 2 {
		t.Errorf("inst-B = %d, want 2", got)
	}
	if calls := runner.calls.Load(); calls != 1 {
		t.Errorf("runner calls = %d, want 1 (idempotent Warm within TTL)", calls)
	}
}

func TestBuildHostIndex_SkipsEmptyHostIP(t *testing.T) {
	idx := buildHostIndex([]state.Instance{
		{ID: "a", HostIP: "10.100.0.2"},
		{ID: "b", HostIP: ""}, // WAKING, no veth yet
		{ID: "c", HostIP: "10.100.0.3"},
	})
	if len(idx) != 2 {
		t.Errorf("len(idx) = %d, want 2", len(idx))
	}
	if idx["10.100.0.2"] != "a" || idx["10.100.0.3"] != "c" {
		t.Errorf("unexpected index: %v", idx)
	}
}
