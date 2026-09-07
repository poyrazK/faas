// Package logbuf implements a per-instance byte-bounded line ring used to
// surface Firecracker guest stdout/stderr to vmmd's Logs gRPC (issue #254,
// Move 4). The same producer hands each completed line to every active
// subscriber channel and retains it in the snapshot until the byte budget is
// exhausted; the oldest lines are then evicted FIFO.
//
// The ring lives here (next to its only consumer in pkg/fcvm/vmm.go) rather
// than in pkg/wire because wire has no ring primitive and the only adjacent
// shape — pkg/sched/scaleup/ringbuf.go::RingBuffer — is a counter bucket for
// RPS windows, not a byte/line ring. Keeping the type co-located with its
// single read/write surface (the jailed firecracker process) lets the test
// suite pin ordering + wraparound without spinning up a real VM.
package logbuf

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultMaxBytes is the byte cap vmmd allocates per live instance when
// constructing a new ring (10 MiB; small enough to fit thousands of instances
// in the §6.2.2 tenant RAM budget, large enough to hold ~5k-50k lines).
const DefaultMaxBytes = 10 << 20

// MaxPartialLineBytes caps the partial-line tail Write buffers
// while it waits for a trailing '\n'. The tail is OUTSIDE the
// ring's byte budget (DefaultMaxBytes) because the ring only
// charges committed lines against its cap — a guest that emits
// bytes without ever sending a '\n' could otherwise grow the
// tail unbounded until vmmd OOMs (issue #309 / tier-2 DX, peer
// review of PR #728).
//
// 1 MiB is well below the 10 MiB ring cap (so a single Write
// burst cannot consume the entire ring budget in a partial line)
// and big enough for any plausible log line — the longest
// realistic log message is <64 KiB (a stack trace or a
// multi-KiB JSON request body). A guest that exceeds the cap
// is misbehaving; the ring drops the partial bytes and starts
// fresh on the next Write. The drop is silent at the line
// level — the slog path (caller's responsibility, see vmmd)
// surfaces the warning so an operator can identify the
// offending app.
const MaxPartialLineBytes = 1 << 20

// Line is one completed stdout/stderr line with a monotonic sequence number
// assigned at ring intake. Concretely a single Write call may carry zero, one,
// or many Lines depending on how many '\n' bytes it contained; partial lines
// (no trailing '\n') are buffered internally and only materialise as a Line
// when the next Write completes them.
type Line struct {
	Seq       int64     `json:"seq"`
	Stream    string    `json:"stream"` // "stdout" or "stderr"
	Line      string    `json:"line"`
	Level     string    `json:"level,omitempty"` // canonical JSON severity: info, warn, or error
	WrittenAt time.Time `json:"written_at"`
}

// Ring is a fixed-budget, line-fragmenting log buffer keyed by Firecracker
// instance id. It is safe for concurrent writers (one firecracker stdio
// goroutine per instance) and concurrent readers (every gRPC subscriber
// against Logs(req)).
//
// Memory model: totalBytes tracks the sum of retained Line.Line payload
// lengths; the per-line overhead is a fixed-size Line struct in a separate
// slice, so the figure is close to the actual heap cost of the buffer. The
// invariant totalBytes <= maxBytes is preserved by evicting the oldest
// complete line at intake; Write never blocks on a full ring.
//
// Subscriber semantics: Subscribe returns a buffered channel that receives
// every Line as it is committed. The cancel func detaches the subscription
// and closes the channel. A Write that races with Close still publishes to
// any channel that has not yet been detached; channels detached before
// Close are drained by Close's locked section so a single producer cannot
// block on a dead consumer.
type Ring struct {
	maxBytes int

	mu         sync.Mutex
	lines      []Line // ring slot array, oldest at head
	head       int    // index of the oldest retained line
	size       int    // number of retained lines (0..len(lines))
	totalBytes int    // sum of Line.Line payload lengths
	nextSeq    int64  // monotonic per-ring; the Seq assigned to the next Write that completes a line
	tail       []byte // partial-line buffer for Write's bytes without a '\n'
	subs       []chan Line

	// onSlowSubscriber (issue #309 / tier-2 DX) is invoked once
	// per dropped LINE (i.e. once per commitLocked call where
	// at least one subscriber's channel was full). A single
	// multi-line Write can therefore fire the callback many
	// times — that is intentional: the
	// apid_logs_dropped_total{reason="slow_subscriber"} counter
	// is meant to scale with log throughput, so an operator
	// looking at the dashboard sees a real drop rate rather
	// than a flat count of Write events. Nil = no callback
	// wired (the default in tests); production wires it to a
	// closure that calls
	// (*wire.OpsMetrics).IncLogDropped("slow_subscriber"). The
	// callback is read under r.mu, so the caller can swap it
	// out at runtime by calling SetSlowSubscriberCallback under
	// its own external synchronisation. Reading nil is fine —
	// the slow-drop branch skips the call.
	//
	// Why a callback and not a direct pkg/wire import: this
	// package is a leaf — pkg/fcvm owns the only consumer and
	// pkg/wire is several hops up. Importing pkg/wire from
	// logbuf would invert the dep direction (pkg/wire already
	// imports nothing from pkg/fcvm). The callback indirection
	// keeps the leaf clean and lets tests exercise the drop
	// path without spinning up a Prometheus registry.
	onSlowSubscriber func()

	// onEvict (issue #562 / log archive to object storage) is
	// invoked once per line evicted from the ring's byte
	// budget. The eviction loop in commitLocked calls it
	// while holding r.mu; the callback MUST NOT block or
	// allocate beyond the syscall-equivalent cost of a
	// bufio-flushed write — a slow callback stalls every
	// subsequent Write on this instance. Nil = no callback
	// wired (the default in tests); production wires it via
	// SetEvictCallback to a closure that hands the line to a
	// per-instance logarchive.Spool writer. The eviction
	// path is the load-bearing seam that closes the
	// "evicted = silently lost" hole the per-instance ring
	// contract had since Move 4 (issue #254) — without this
	// hook, every line that crosses the 10 MiB byte cap is
	// lost before the 5-min shipper can read it.
	//
	// Same callback indirection rationale as onSlowSubscriber:
	// pkg/logarchive imports pkg/wire, not the other way
	// around; the callback keeps logbuf a leaf.
	onEvict func(Line)

	closed bool
}

// New constructs a Ring with the given byte cap. maxBytes <= 0 falls back to
// DefaultMaxBytes so callers can wire a zero-default without a flag day.
func New(maxBytes int) *Ring {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Ring{
		maxBytes: maxBytes,
	}
}

// SetSlowSubscriberCallback installs (or removes, when nil) the
// per-drop callback the commitLocked Publish loop invokes when
// a subscriber's channel is full (issue #309 / tier-2 DX). The
// callback fires once per dropped line — exactly what the
// apid_logs_dropped_total{reason="slow_subscriber"} counter
// needs. Wire from a constructor closure that adapts the
// per-ring instance to the vmmd-wide wire.OpsMetrics, e.g.:
//
//	ring := logbuf.New(0)
//	ring.SetSlowSubscriberCallback(func() {
//	    metrics.IncLogDropped("slow_subscriber")
//	})
//
// The callback is read under r.mu; the caller does NOT need to
// hold a lock when installing — the mu acquire inside the read
// site provides the necessary synchronisation. Calling this
// after the ring has begun receiving writes is safe; the new
// callback takes effect on the next commit.
func (r *Ring) SetSlowSubscriberCallback(cb func()) {
	r.mu.Lock()
	r.onSlowSubscriber = cb
	r.mu.Unlock()
}

// SetEvictCallback installs (or removes, when nil) the per-line
// callback the commitLocked eviction loop invokes on every line
// that crosses the byte budget (issue #562 / log archive to
// object storage). Wire from a per-ring constructor closure
// that adapts the ring to the pkg/logarchive.Spool writer, e.g.:
//
//	ring := logbuf.New(0)
//	ring.SetEvictCallback(func(l Line) {
//	    spool.Write(instanceID, l)
//	})
//
// The callback is read under r.mu; the caller does NOT need to
// hold a lock when installing. Calling this after the ring has
// begun receiving writes is safe; the new callback takes effect
// on the next eviction. Nil-safe: commitLocked's evict branch
// skips the call when onEvict is nil, so tests that don't wire
// a spool see the original behaviour (silent eviction).
//
// The eviction callback runs WHILE the ring lock is held; the
// spool's writer is responsible for not blocking on disk I/O.
// pkg/logarchive.Spool uses a bufio.Writer with a 64 KiB buffer
// so the syscall count amortises across many evictions — a
// single direct Write per eviction would burn a syscall per
// line on a chatty app and stall the producer.
func (r *Ring) SetEvictCallback(cb func(Line)) {
	r.mu.Lock()
	r.onEvict = cb
	r.mu.Unlock()
}

// Write consumes a chunk of firecracker stdout/stderr bytes, splitting on
// '\n' and emitting one Line per completed line. Bytes without a trailing
// '\n' are buffered until the next Write completes them, so a chunk that
// carries N lines produces N Lines and the channel publish count matches
// the snapshot replay count exactly.
//
// Returns len(p) on success (we own the bytes; io.Copy contract). On a
// closed ring the bytes are dropped and (0, ErrClosed) is returned — keep
// the producer's exec.Cmd stdout going; fcvm.Kill is idempotent so a
// double-close at teardown does not double-return errors that the watchdog
// would log anyway.
func (r *Ring) Write(stream string, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	now := time.Now()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, ErrClosed
	}
	// Append the new chunk to the partial tail and scan for '\n' boundaries.
	// Most Writes carry at most a handful of lines, so a fresh copy with one
	// IndexByte pass per '\n' beats a bufio.Scanner heap allocation under the
	// per-instance hot path.
	//
	// Preallocation cap is len(p) only — NOT len(p)+64 — because the +64
	// could overflow on a near-max-byte slice (CodeQL go/integer-conversion-
	// bounds-check flagged this as a high-severity alloc-overflow risk). The
	// next Write's append will grow the slice if a single chunk exceeds the
	// current cap, so dropping the +64 has no observable cost.
	if r.tail == nil {
		r.tail = make([]byte, 0, len(p))
	}
	// Partial-line DoS guard (issue #309 / tier-2 DX, peer review
	// of PR #728): without this cap, a guest that never sends a
	// '\n' grows r.tail unbounded. Drop the partial bytes and
	// start fresh — the next Write's leading bytes are the new
	// partial-line beginning. We do NOT return an error (the
	// io.Copy contract wants len(p), nil on success) and we do
	// NOT increment apid_logs_dropped_total (this is a ring
	// integrity event, not a customer log filtering event;
	// vmmd logs the warning at the call site).
	if len(r.tail)+len(p) > MaxPartialLineBytes {
		r.tail = r.tail[:0]
		if len(p) > MaxPartialLineBytes {
			// Single Write alone exceeds the cap. Keep the
			// tail empty and treat the bytes as dropped;
			// the next Write starts a fresh partial.
			r.mu.Unlock()
			return len(p), nil
		}
	}
	r.tail = append(r.tail, p...)
	// Pull every complete line off the tail into committed Lines.
	for {
		i := bytes.IndexByte(r.tail, '\n')
		if i < 0 {
			break
		}
		line := string(r.tail[:i])
		r.tail = r.tail[i+1:]
		r.commitLocked(stream, line, now)
	}
	r.mu.Unlock()
	return len(p), nil
}

// commitLocked appends one completed line to the ring, evicting the oldest
// line(s) if totalBytes would exceed the budget. Caller holds r.mu.
//
// A line longer than r.maxBytes is REJECTED outright: evicting the entire
// retained slice would not bring totalBytes below the budget (a single
// pathological line can be larger than 10 MiB of accumulated history), and
// admitting the line would silently inflate totalBytes past the configured
// cap. The drop is silent at the line level — vmmd logs the warning at the
// call site so an operator can identify the offending app. This closes the
// "tenant-controlled output bypasses the per-instance memory bound" hole
// the peer review of PR #728 surfaced (the size > 0 evict loop in the
// original commit could not compensate for a single line > maxBytes).
func (r *Ring) commitLocked(stream, line string, now time.Time) {
	if len(line) > r.maxBytes {
		// Pathological: a single line larger than the entire ring
		// budget. Don't accept it; the call site (vmmd) logs at
		// warn level so the operator sees it. We do NOT increment
		// apid_logs_dropped_total — this is a ring-integrity
		// event, not a customer log filtering event.
		return
	}
	r.nextSeq++
	ln := Line{
		Seq:       r.nextSeq,
		Stream:    stream,
		Line:      line,
		Level:     detectLevel(line),
		WrittenAt: now,
	}
	// Evict from head until totalBytes + len(line) <= maxBytes. We must
	// evict BEFORE assigning to the slot because the slot overwrites the
	// head slot on full ring; the per-line overhead in the slice is
	// constant so we don't need to charge it here.
	//
	// Issue #562 / log archive to object storage: each evicted line is
	// forwarded to the optional onEvict callback before being dropped
	// from the slot. The callback is the load-bearing seam that
	// converts "evicted = silently lost" into "evicted = persisted
	// locally + shipped async". The callback runs under r.mu, so it
	// must not block — pkg/logarchive.Spool amortises syscalls via a
	// 64 KiB bufio buffer. We capture the callback reference at the
	// top of the loop so a concurrent SetEvictCallback can't tear the
	// callback mid-eviction (the SetEvictCallback setter acquires r.mu
	// to swap the field; reading the field here under the same lock
	// gives us a stable handoff).
	evictCb := r.onEvict
	for r.totalBytes+len(line) > r.maxBytes && r.size > 0 {
		evicted := r.lines[r.head]
		r.totalBytes -= len(evicted.Line)
		r.head = (r.head + 1) % cap(r.lines)
		r.size--
		if evictCb != nil {
			evictCb(evicted)
		}
	}
	// Grow the underlying slice on demand. We choose capacity over exact
	// fit so a chatty app doesn't reallocate every line; maxBytes caps the
	// working set regardless of cap(lines).
	if r.size == cap(r.lines) {
		newCap := 64
		if c := cap(r.lines); c > 0 {
			newCap = c * 2
		}
		grown := make([]Line, newCap)
		if r.size > 0 {
			// Copy retained slice in head→tail order so the new layout is
			// contiguous; subsequent appends/evictions are O(1).
			for k := 0; k < r.size; k++ {
				grown[k] = r.lines[(r.head+k)%cap(r.lines)]
			}
			r.head = 0
		}
		r.lines = grown
	}
	idx := (r.head + r.size) % cap(r.lines)
	r.lines[idx] = ln
	r.size++
	r.totalBytes += len(line)
	// Publish to every active subscriber. We snapshot subs under the lock
	// to release it before sending so a slow subscriber cannot stall a
	// producer that's still consuming firecracker stdout.
	subs := r.subs
	dropped := false
	for _, ch := range subs {
		select {
		case ch <- ln:
		default:
			// Slow consumer — drop. The drop counter is
			// surfaced at the apid layer via
			// apid_logs_dropped_total so the §12 panel
			// reflects the per-instance slow-consumer
			// rate (issue #309 / tier-2 DX) — the
			// onSlowSubscriber callback is the
			// load-bearing wire hook; nil-safe for tests
			// that don't wire metrics. The local flag
			// (not a counter) records "any channel was
			// full this Write" — the callback fires once
			// per Write regardless of how many
			// subscribers were slow, so the counter rate
			// scales with Write events, not with
			// subscriber count (see the
			// TestRing_SlowSubscriberCallbackFires /
			// MultipleFullSubscribersOneIncrement
			// subtest in ring_test.go).
			dropped = true
		}
	}
	if dropped && r.onSlowSubscriber != nil {
		r.onSlowSubscriber()
	}
}

// detectLevel extracts a canonical severity from a structured JSON log line.
// Plain text, malformed JSON, and JSON without a recognised level are left
// unclassified (the empty string). The original line is never rewritten: the
// level is additive metadata for the log stream and existing consumers keep
// seeing the exact bytes emitted by the workload.
//
// Both the common `level` key (Pino, Zap, Logrus) and Cloud Logging's
// `severity` key are accepted. Numeric Pino levels are mapped using its
// conventional scale (10 trace, 20 debug, 30 info, 40 warn, 50 error,
// 60 fatal). The public stream intentionally uses the existing three-level
// vocabulary: trace/debug/notice become info, warning becomes warn, and
// critical/alert/emergency/fatal become error.
func detectLevel(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed[0] != '{' {
		return ""
	}
	var record struct {
		Level    json.RawMessage `json:"level"`
		Severity json.RawMessage `json:"severity"`
	}
	if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
		return ""
	}
	best := ""
	for _, raw := range []json.RawMessage{record.Level, record.Severity} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		if raw[0] == '"' {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				continue
			}
			if level := normalizeLevel(value); level != "" && levelRank(level) > levelRank(best) {
				best = level
			}
			continue
		}
		if number, err := strconv.Atoi(string(raw)); err == nil {
			if level := normalizeNumericLevel(number); level != "" && levelRank(level) > levelRank(best) {
				best = level
			}
		}
	}
	return best
}

func normalizeLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "trace", "debug", "info", "notice":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "err", "fatal", "critical", "alert", "emergency":
		return "error"
	default:
		return ""
	}
}

func normalizeNumericLevel(value int) string {
	switch {
	case value >= 10 && value <= 30:
		return "info"
	case value == 30:
		return "info"
	case value == 40:
		return "warn"
	case value >= 50:
		return "error"
	default:
		return ""
	}
}

func levelRank(level string) int {
	switch level {
	case "error":
		return 2
	case "warn":
		return 1
	case "info":
		return 0
	default:
		return -1
	}
}

// Snapshot returns a copy of every Line whose Seq is >= sinceSeq, in write
// order. A sinceSeq <= 0 means "tail from now" — return nil so the
// consumer (vmmd's Logs handler) opens the live Subscribe channel
// without replaying history. A sinceSeq > the highest assigned Seq also
// returns nil (the cursor is past the high-water mark; nothing to replay
// yet). The result is a fresh slice — callers may retain, marshal, or
// modify it without aliasing the ring.
func (r *Ring) Snapshot(sinceSeq int64) []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(sinceSeq)
}

func (r *Ring) snapshotLocked(sinceSeq int64) []Line {
	if r.size == 0 || sinceSeq <= 0 {
		return nil
	}
	// Find the first retained Line whose Seq >= sinceSeq. A linear scan is
	// fine — size is bounded by maxBytes/avgLineLen and the snapshot path
	// runs on gRPC attach, not on every line.
	start := 0
	for k := 0; k < r.size; k++ {
		if r.lines[(r.head+k)%cap(r.lines)].Seq >= sinceSeq {
			start = k
			break
		}
		if k == r.size-1 {
			return nil
		}
	}
	out := make([]Line, r.size-start)
	for k := 0; k < len(out); k++ {
		out[k] = r.lines[(r.head+start+k)%cap(r.lines)]
	}
	return out
}

func (r *Ring) SnapshotAndSubscribe(sinceSeq int64) ([]Line, <-chan Line, func()) {
	ch := make(chan Line, 64)
	r.mu.Lock()
	snapshot := r.snapshotLocked(sinceSeq)
	if r.closed {
		r.mu.Unlock()
		close(ch)
		return snapshot, ch, func() {}
	}
	r.subs = append(r.subs, ch)
	r.mu.Unlock()
	return snapshot, ch, r.cancelSubscription(ch)
}

// Subscribe returns a channel that receives every committed Line from now
// on, plus a cancel func that detaches the subscription and closes the
// channel. Each Subscribe call returns a fresh channel — concurrent
// subscribers do not share their backpressure.
//
// The channel is buffered (capacity 64) so a momentary stall on the network
// doesn't drop the producer. A subscriber that falls further behind causes
// the ring to drop on the next commit (see Write) and increment the
// apid_logs_dropped_total counter at the apid layer.
func (r *Ring) Subscribe() (<-chan Line, func()) {
	ch := make(chan Line, 64)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	r.subs = append(r.subs, ch)
	r.mu.Unlock()
	return ch, r.cancelSubscription(ch)
}

func (r *Ring) cancelSubscription(ch chan Line) func() {
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed {
			return
		}
		for i, c := range r.subs {
			if c != ch {
				continue
			}
			r.subs = append(r.subs[:i], r.subs[i+1:]...)
			select {
			case <-ch:
			default:
			}
			close(ch)
			return
		}
	}
}

// LowestRetainedSeq reports the Seq of the oldest line currently
// retained by the ring, or 0 when the ring is empty (mirrors the
// Snapshot "tail from now" sentinel).
//
// Used by vmmdgrpc.Server.Logs (issue #517 / PR-B acceptance #4) to
// detect a cursor that fell below the ring's high-water mark and
// surface an `event: gap` SSE frame instead of silently replaying
// from a non-existent point. The attach-time lock is microseconds vs.
// the gRPC dial round-trip; the value is consumed once per
// StreamAppLogs attach.
//
// Locked under r.mu. The sequence number is monotonic per ring, so
// the oldest retained line is always r.lines[r.head].Seq.
func (r *Ring) LowestRetainedSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return 0
	}
	return r.lines[r.head].Seq
}

// HeadWrittenAt reports the host-side WrittenAt of the oldest line
// the ring currently retains (the same line LowestRetainedSeq
// references), or the zero time.Time when the ring is empty.
//
// The gap frame on issue #517 / PR-B acceptance #4 carries this
// timestamp in `gap_to_written_at` so a client can surface a
// meaningful "lines whose newest retained timestamp is X were
// evicted" message; clock source is the host (ADR-022 entropy
// hazard).
//
// Locked under r.mu.
func (r *Ring) HeadWrittenAt() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return time.Time{}
	}
	return r.lines[r.head].WrittenAt
}

// Close marks the ring as closed and tears down every active subscriber
// channel. Subsequent Write calls return ErrClosed; Snapshot returns the
// last-committed lines up to the close point. Idempotent.
//
// The pkg/fcvm/vmm.go::JailerVMM.Kill path calls Close so a park/unpark
// cycle drops the ring and frees the byte budget — invariant §6.2-4
// (parked app = zero resident RAM).
func (r *Ring) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	subs := r.subs
	r.subs = nil
	r.mu.Unlock()
	for _, ch := range subs {
		close(ch)
	}
	return nil
}

// ErrClosed is returned by Write after the ring has been Close()'d.
var ErrClosed = errClosed{}

type errClosed struct{}

func (errClosed) Error() string { return "logbuf: ring closed" }
