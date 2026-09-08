package logbuf

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRing_StructuredJSONLevel(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "level info", line: `{"level":"info","msg":"started"}`, want: "info"},
		{name: "severity warning", line: `{"severity":"WARNING","message":"slow"}`, want: "warn"},
		{name: "severity critical", line: `{"severity":"CRITICAL","message":"down"}`, want: "error"},
		{name: "numeric pino error", line: `{"level":50,"msg":"failed"}`, want: "error"},
		{name: "highest of level and severity", line: `{"level":"debug","severity":"ERROR","msg":"failed"}`, want: "error"},
		{name: "plain text", line: "started", want: ""},
		{name: "malformed JSON", line: `{"level":"error"`, want: ""},
		{name: "unknown level", line: `{"level":"verbose","msg":"trace"}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(1 << 20)
			if _, err := r.Write("stdout", []byte(tt.line+"\n")); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := r.Snapshot(1)
			if len(got) != 1 {
				t.Fatalf("Snapshot returned %d lines, want 1", len(got))
			}
			if got[0].Level != tt.want {
				t.Errorf("Level = %q, want %q", got[0].Level, tt.want)
			}
			if got[0].Line != tt.line {
				t.Errorf("Line = %q, want original %q", got[0].Line, tt.line)
			}
		})
	}
}

// TestRing_LineOrdering pins the roundtrip property: bytes written in a
// specific sequence come back in the same sequence from Snapshot, with
// monotonically increasing Seq starting at 1 and consecutive lines.
func TestRing_LineOrdering(t *testing.T) {
	r := New(1 << 20)
	in := []string{"alpha", "beta", "gamma", "delta"}
	for _, s := range in {
		if _, err := r.Write("stdout", []byte(s+"\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	got := r.Snapshot(1)
	if len(got) != len(in) {
		t.Fatalf("Snapshot(1) size = %d, want %d", len(got), len(in))
	}
	for i, line := range got {
		if line.Line != in[i] {
			t.Errorf("line[%d] = %q, want %q", i, line.Line, in[i])
		}
		if line.Stream != "stdout" {
			t.Errorf("line[%d].Stream = %q, want stdout", i, line.Stream)
		}
		if i == 0 {
			if line.Seq != 1 {
				t.Errorf("line[0].Seq = %d, want 1", line.Seq)
			}
		} else {
			if line.Seq != got[i-1].Seq+1 {
				t.Errorf("line[%d].Seq = %d, not monotonically +1 over line[%d]=%d",
					i, line.Seq, i-1, got[i-1].Seq)
			}
		}
	}
}

// TestRing_SnapshotSince verifies Snapshot(sinceSeq) returns only Lines with
// Seq >= sinceSeq, no duplicates and no gaps. This is the load-bearing
// property for vmmdgrpc.Server.Logs's replay path (issue #254 acceptance #2:
// clients can resume from a known cursor).
func TestRing_SnapshotSince(t *testing.T) {
	r := New(1 << 20)
	for i := 0; i < 10; i++ {
		if _, err := r.Write("stdout", []byte("line\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// Replay from seq=5 — must return lines 5..10 (6 entries), with no
	// repeats and consecutive sequence numbers.
	got := r.Snapshot(5)
	if len(got) != 6 {
		t.Fatalf("Snapshot(5) size = %d, want 6", len(got))
	}
	for i, line := range got {
		want := int64(5 + i)
		if line.Seq != want {
			t.Errorf("got[%d].Seq = %d, want %d", i, line.Seq, want)
		}
	}
	// Replay beyond the high-water mark returns nil.
	if got := r.Snapshot(99); got != nil {
		t.Errorf("Snapshot(99) = %v, want nil", got)
	}
	// sinceSeq <= 0 is the SDK's "tail from now" cursor (the apid
	// handler's default). Return nil so vmmdgrpc.Server.Logs skips
	// replay and goes straight to live Subscribe — the load-bearing
	// contract for faas logs/CLI.
	if got := r.Snapshot(0); got != nil {
		t.Errorf("Snapshot(0) size = %d, want nil (tail-from-now)", len(got))
	}
}

// TestRing_Wraparound pins the eviction contract: when the byte budget is
// exhausted, the oldest Line is dropped on the next commit and the new Seq
// stays monotonic. totalBytes is also bounded by maxBytes at all times.
//
// Math: 17-byte payload lines on a 64-byte ring.
//   - 3 commits: 3×17 = 51 ≤ 64 → no eviction, 3 retained.
//   - 4th commit: totalBytes=51, line=17 → 68 > 64, so evict 1 → retain 3 lines.
//   - 5th commit: totalBytes=51, evict 1, retain 3 lines.
//
// We assert the buffer holds exactly 3 lines (the most recent three)
// regardless of how many commits we make beyond the budget, and the
// retained sequence numbers are consecutive from the latest committed.
func TestRing_Wraparound(t *testing.T) {
	const (
		ringBytes  = 64
		payloadLen = 17 // stored line length; does not include the '\n' terminator
	)
	r := New(ringBytes)
	payload := make([]byte, payloadLen)
	for i := range payload {
		payload[i] = 'x'
	}
	for i := 0; i < 10; i++ {
		chunk := append([]byte{}, payload...)
		chunk = append(chunk, '\n')
		if _, err := r.Write("stderr", chunk); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if r.totalBytes > ringBytes {
			t.Fatalf("after commit %d: totalBytes=%d exceeds maxBytes=%d",
				i, r.totalBytes, ringBytes)
		}
	}
	got := r.Snapshot(1)
	// 3 lines × 17 bytes = 51 ≤ 64; 4 × 17 = 68 > 64. So ring always
	// retains 3 lines once steady state is reached.
	if len(got) != 3 {
		t.Fatalf("retained=%d, want 3 (oldest lines should have been evicted)",
			len(got))
	}
	// After 10 commits, the 3 retained lines are commits 8, 9, 10.
	if got[0].Seq != 8 || got[1].Seq != 9 || got[2].Seq != 10 {
		t.Errorf("retained seqs = [%d, %d, %d], want [8, 9, 10]",
			got[0].Seq, got[1].Seq, got[2].Seq)
	}
	// totalBytes must stay at 51 (3 × 17).
	if r.totalBytes != payloadLen*3 {
		t.Errorf("totalBytes = %d, want %d", r.totalBytes, payloadLen*3)
	}
}

// TestRing_PartialLine pins the line-fragmentation contract: a Write without
// a trailing '\n' is buffered until the next Write completes it. A subsequent
// Snapshot returns exactly one Line carrying the joined bytes, never two.
func TestRing_PartialLine(t *testing.T) {
	r := New(1 << 20)
	if _, err := r.Write("stdout", []byte("hello ")); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	// Snapshot must NOT include the incomplete line yet.
	if got := r.Snapshot(0); got != nil {
		t.Errorf("Snapshot(0) after partial Write = %v, want nil (tail-from-now)", got)
	}
	if _, err := r.Write("stdout", []byte("world\n")); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	// Snapshot(1) = full replay from seq=1 (the first assigned Seq).
	// Snapshot(0) = tail from now (returns nil per wire contract).
	got := r.Snapshot(1)
	if len(got) != 1 {
		t.Fatalf("Snapshot(1) after completion = %d lines, want 1", len(got))
	}
	if got[0].Line != "hello world" {
		t.Errorf("joined line = %q, want %q", got[0].Line, "hello world")
	}
	if got[0].Seq != 1 {
		t.Errorf("Seq = %d, want 1", got[0].Seq)
	}
}

// TestRing_DeterminismMB writes 1 MiB of newline-terminated bytes from a
// single goroutine and asserts the snapshot reproduces the count correctly.
// The Line field stores the payload BEFORE the newline (the terminator is
// consumed by the splitter), so retained length is len(payload) not
// len(payload)+1. Determinism here is the test property: a flaky ring
// (lost line, duplicated line) fails on the count mismatch.
func TestRing_DeterminismMB(t *testing.T) {
	r := New(8 << 20) // 8 MiB cap, plenty of headroom for 1 MiB payload
	var buf [8]byte
	for i := 0; i < 131072; i++ {
		copy(buf[:], "L000000\n")
		if _, err := r.Write("stdout", buf[:]); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	got := r.Snapshot(1)
	if len(got) != 131072 {
		t.Fatalf("retained = %d lines, want 131072", len(got))
	}
	for i := 0; i < len(got); i++ {
		if len(got[i].Line) != 7 {
			t.Errorf("got[%d] length = %d, want 7 (raw line dropped?)",
				i, len(got[i].Line))
		}
	}
}

// TestRing_Subscribe pins the live-tail contract: Subscribe returns new
// lines as they are committed, in commit order, and cancel() detaches the
// subscription so subsequent lines are no longer published to that channel.
func TestRing_Subscribe(t *testing.T) {
	r := New(1 << 20)
	ch, cancel := r.Subscribe()
	if _, err := r.Write("stdout", []byte("a\nb\nc\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var got []string
	for i := 0; i < 3; i++ {
		select {
		case line, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed prematurely at i=%d", i)
			}
			got = append(got, line.Line)
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for line %d", i)
		}
	}
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Cancel must detach — subsequent Write does NOT publish to ch.
	cancel()
	if _, err := r.Write("stdout", []byte("post-cancel\n")); err != nil {
		t.Fatalf("Write post-cancel: %v", err)
	}
	select {
	case line, ok := <-ch:
		if ok {
			t.Errorf("received line %q on cancelled channel", line.Line)
		}
	case <-time.After(50 * time.Millisecond):
		// Expected — nothing arrives.
	}
}

func TestRing_SnapshotAndSubscribe(t *testing.T) {
	r := New(1 << 20)
	if _, err := r.Write("stdout", []byte("before\n")); err != nil {
		t.Fatalf("Write before: %v", err)
	}
	snapshot, ch, cancel := r.SnapshotAndSubscribe(1)
	defer cancel()
	if len(snapshot) != 1 || snapshot[0].Line != "before" {
		t.Fatalf("snapshot = %v, want one line before", snapshot)
	}
	if _, err := r.Write("stdout", []byte("after\n")); err != nil {
		t.Fatalf("Write after: %v", err)
	}
	select {
	case line := <-ch:
		if line.Line != "after" {
			t.Errorf("live line = %q, want after", line.Line)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live line")
	}
}

// TestRing_ConcurrentWrites pins the contract that two goroutines writing
// concurrently do not race, do not lose lines, and do not corrupt sequence
// numbers. This is what the production path looks like: the VM's stdout and
// stderr each push onto the same ring through two separate Writers.
//
// We assert the buffer is FIFO within each Write chunk (no torn lines),
// and the highest Seq equals the total committed line count, which is the
// property a slow consumer would rely on to resume correctly.
func TestRing_ConcurrentWrites(t *testing.T) {
	const perGoroutine = 1000
	r := New(8 << 20)
	var wg sync.WaitGroup
	var totalWritten int64
	for g := 0; g < 4; g++ {
		wg.Add(1)
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				chunk := []byte("g0-L00000\n")
				chunk[1] = byte('0' + g)
				// Encode i into bytes 3..6 so each line is unique.
				n := i
				for j := 6; j >= 3; j-- {
					chunk[j] = byte('0' + n%10)
					n /= 10
				}
				if _, err := r.Write("stdout", chunk); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
				atomic.AddInt64(&totalWritten, 1)
			}
		}()
	}
	wg.Wait()

	if int64(perGoroutine*4) != atomic.LoadInt64(&totalWritten) {
		t.Fatalf("totalWritten = %d, want %d", totalWritten, perGoroutine*4)
	}
	snap := r.Snapshot(1)
	if int64(len(snap)) != totalWritten {
		t.Errorf("retained = %d, want %d (lines lost)", len(snap), totalWritten)
	}
	// Seq must be contiguous and monotonic — gaps signal a corrupted Seq.
	for i, ln := range snap {
		if ln.Seq != int64(i+1) {
			t.Fatalf("snap[%d].Seq = %d, want %d (gap or duplicate)",
				i, ln.Seq, i+1)
		}
	}
}

// TestRing_CloseAfterWrite pins the lifecycle contract: after Close,
// Write returns ErrClosed, Snapshot still returns the last-committed lines,
// and active subscribers receive a closed channel.
func TestRing_CloseAfterWrite(t *testing.T) {
	r := New(1 << 20)
	if _, err := r.Write("stdout", []byte("before-close\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := r.Write("stdout", []byte("post-close\n")); err == nil {
		t.Fatalf("Write after Close = nil, want ErrClosed")
	}
	// Snapshot(1) returns the buffer pre-close (Close only blocks future Writes).
	snap := r.Snapshot(1)
	if len(snap) != 1 || snap[0].Line != "before-close" {
		t.Errorf("post-close Snapshot(1) = %v, want one line %q", snap, "before-close")
	}
	// Close is idempotent.
	if err := r.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

// TestRing_Empty pins the Snapshot-on-fresh-ring behaviour and the
// "sinceSeq <= 0 = tail from now" contract: both return nil so
// vmmdgrpc.Server.Logs skips the replay phase and goes straight to
// the live Subscribe. An empty firecracker (no guest output yet)
// hits this branch on every consumer attach.
func TestRing_Empty(t *testing.T) {
	r := New(1 << 20)
	if got := r.Snapshot(0); got != nil {
		t.Errorf("Snapshot(0) on empty = %v, want nil", got)
	}
	if got := r.Snapshot(-1); got != nil {
		t.Errorf("Snapshot(-1) on empty = %v, want nil", got)
	}
	// Write of empty bytes is a successful no-op.
	n, err := r.Write("stdout", nil)
	if n != 0 || err != nil {
		t.Errorf("Write(nil) = (%d, %v), want (0, nil)", n, err)
	}
}

// TestRing_OversizedLineRejected pins the contract that a single
// line whose payload exceeds maxBytes is dropped at commitLocked
// time rather than admitted and silently inflating totalBytes past
// the configured budget (peer review of PR #728). The pre-fix
// evict-until-size=0 loop could not compensate for a single line
// larger than maxBytes — the eviction drains the ring but the
// subsequent insert still pushes totalBytes over the cap.
func TestRing_OversizedLineRejected(t *testing.T) {
	const ringBytes = 64
	r := New(ringBytes)
	// Line payload is 2x ringBytes — strictly larger than the
	// budget. commitLocked must reject it.
	huge := make([]byte, ringBytes*2)
	for i := range huge {
		huge[i] = 'x'
	}
	huge = append(huge, '\n')
	if _, err := r.Write("stdout", huge); err != nil {
		t.Fatalf("Write oversized: %v", err)
	}
	// Ring must be empty — the line was rejected, not stored.
	if got := r.Snapshot(1); len(got) != 0 {
		t.Errorf("Snapshot(1) after oversized Write = %d lines, want 0 (rejected)", len(got))
	}
	if r.totalBytes != 0 {
		t.Errorf("totalBytes after oversized Write = %d, want 0", r.totalBytes)
	}

	// Sanity check: a normal line under the cap still works.
	if _, err := r.Write("stdout", []byte("ok\n")); err != nil {
		t.Fatalf("Write normal: %v", err)
	}
	if got := r.Snapshot(1); len(got) != 1 || got[0].Line != "ok" {
		t.Errorf("Snapshot(1) after normal Write = %v, want one line \"ok\"", got)
	}
}

// TestRing_SnapshotTailFromNow pins the "sinceSeq <= 0 = tail from
// now" contract (issue #254 Move 4 wire shape: the apid handler
// passes since_seq=0 as the default "open from now" and the SDK
// calls StreamAppLogs without specifying a cursor). The ring MUST
// return nil for sinceSeq <= 0 so the vmmd-side Logs RPC skips the
// replay phase and goes straight to the live Subscribe channel —
// otherwise a freshly-attached consumer would receive the full ring
// contents as "historical" frames, contradicting the doc on
// pkg/api.LogEvent.StreamAppLogs.
func TestRing_SnapshotTailFromNow(t *testing.T) {
	r := New(1 << 20)
	for i := 0; i < 5; i++ {
		if _, err := r.Write("stdout", []byte("line\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	// sinceSeq=0 and sinceSeq<0 must both return nil even with
	// retained content.
	if got := r.Snapshot(0); got != nil {
		t.Errorf("Snapshot(0) on populated ring = %v, want nil (tail-from-now)", got)
	}
	if got := r.Snapshot(-7); got != nil {
		t.Errorf("Snapshot(-7) on populated ring = %v, want nil", got)
	}
	// sinceSeq >= 1 still works as before (replay branch).
	if got := r.Snapshot(3); len(got) != 3 {
		t.Errorf("Snapshot(3) on 5-line ring = %d lines, want 3", len(got))
	}
}

// TestRing_CloseRaceWithWrite pins the contract that Close and Write
// (and Subscribe's cancel) are safe to call concurrently without
// panicking on a closed-channel send. The ring relies on
// lock-held-during-publish to serialise the publish loop with the
// subscribe-cancel path: while commitLocked holds r.mu and is
// iterating r.subs to send, no concurrent goroutine can call
// r.subs[i] = ... or close(ch) — both of which also take r.mu.
//
// The tripwire is run under -race; a regression that drops the lock
// around the publish loop surfaces as a "send on closed channel"
// panic or a race-detector violation.
//
// PR #388 review finding #1 was that this window looked unsafe; the
// test exists to lock the safety claim against future refactors.
func TestRing_CloseRaceWithWrite(t *testing.T) {
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		r := New(1 << 20)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Concurrent subscribers; each cancels independently.
			for j := 0; j < 4; j++ {
				ch, cancel := r.Subscribe()
				go func(ch <-chan Line) {
					for range ch {
					}
				}(ch)
				cancel()
			}
		}()
		go func() {
			defer wg.Done()
			defer func() { _ = r.Close() }()
			// Hammer Write with partial-line bytes that complete
			// on the next call. commitLocked runs many times.
			for j := 0; j < 64; j++ {
				if _, err := r.Write("stdout", []byte("a\nb\nc")); err != nil {
					return
				}
				if _, err := r.Write("stdout", []byte("\n")); err != nil {
					return
				}
			}
		}()
		wg.Wait()
	}
}

// TestRing_SlowSubscriberCallbackFires (issue #309 / tier-2 DX):
// when a subscriber's buffered channel is full, the Publish loop
// in commitLocked drops the line AND fires the
// onSlowSubscriber callback exactly once per dropped LINE (NOT
// once per Write). A multi-line Write whose bytes all land on a
// full subscriber channel therefore fires the callback N times,
// where N is the number of newline-terminated lines in the Write.
// This is intentional: the apid_logs_dropped_total{reason="slow_subscriber"}
// counter is meant to scale with log throughput (lines/sec), not
// with Write event count, so the dashboard panel reflects a real
// drop rate.
//
// The callback is the seam pkg/wire's IncLogDropped("slow_subscriber")
// uses — this test pins the load-bearing contract.
//
// Pins four behaviours in one go:
//  1. A nil callback (the default) means a drop is silent
//     (matches pre-#309 behaviour, no test regression).
//  2. A wired callback fires exactly once per dropped line when
//     the only subscriber's buffer is full.
//  3. A wired callback fires exactly once per dropped line even
//     when MULTIPLE subscribers' channels are full (per-line
//     semantics: one full subscriber channel for a given line
//     is enough to drop it; the counter increments once).
//  4. A multi-line Write with a full subscriber channel fires
//     the callback once per line, NOT once per Write.
func TestRing_SlowSubscriberCallbackFires(t *testing.T) {
	t.Run("NilCallbackSilentDrop", func(t *testing.T) {
		r := New(1 << 20)
		ch, cancel := r.Subscribe()
		defer cancel()
		// Fill the channel (buffered 64). After this loop the
		// subscriber's channel is full.
		buf := make([]byte, 0, 64*4)
		for i := 0; i < 64; i++ {
			buf = append(buf, "x\n"...)
		}
		if _, err := r.Write("stdout", buf); err != nil {
			t.Fatalf("Write fill: %v", err)
		}
		// Next Write lands with a full subscriber channel. No
		// callback wired — the drop is silent (no panic, no
		// counter).
		if _, err := r.Write("stdout", []byte("dropped\n")); err != nil {
			t.Fatalf("Write drop: %v", err)
		}
		// Drain to keep the test from leaking goroutines.
		for i := 0; i < 64; i++ {
			<-ch
		}
	})

	t.Run("CallbackFiresOncePerDroppedLine", func(t *testing.T) {
		r := New(1 << 20)
		ch, cancel := r.Subscribe()
		defer cancel()
		var drops int
		r.SetSlowSubscriberCallback(func() { drops++ })

		// Fill the subscriber channel so subsequent Writes
		// hit the slow-subscriber default-branch.
		buf := make([]byte, 0, 64*4)
		for i := 0; i < 64; i++ {
			buf = append(buf, "x\n"...)
		}
		if _, err := r.Write("stdout", buf); err != nil {
			t.Fatalf("Write fill: %v", err)
		}

		// Three Writes while the subscriber is full — each
		// Write carries one newline-terminated line, so the
		// callback fires exactly three times (per-line
		// semantics).
		for i := 0; i < 3; i++ {
			if _, err := r.Write("stdout", []byte("dropped\n")); err != nil {
				t.Fatalf("Write drop[%d]: %v", i, err)
			}
		}
		if drops != 3 {
			t.Errorf("callback fired %d times, want 3", drops)
		}

		// Drain to keep the test from leaking goroutines.
		for i := 0; i < 64; i++ {
			<-ch
		}
	})

	t.Run("MultipleFullSubscribersOneIncrementPerLine", func(t *testing.T) {
		r := New(1 << 20)
		ch1, cancel1 := r.Subscribe()
		defer cancel1()
		ch2, cancel2 := r.Subscribe()
		defer cancel2()

		var drops int
		r.SetSlowSubscriberCallback(func() { drops++ })

		// Fill BOTH subscribers' buffers (buffered 64 each).
		buf := make([]byte, 0, 64*4)
		for i := 0; i < 64; i++ {
			buf = append(buf, "x\n"...)
		}
		if _, err := r.Write("stdout", buf); err != nil {
			t.Fatalf("Write fill: %v", err)
		}

		// One more Write lands with BOTH subscribers full —
		// the callback MUST fire exactly once for that single
		// dropped line (per-line semantics), not twice (one
		// per full subscriber).
		if _, err := r.Write("stdout", []byte("dropped\n")); err != nil {
			t.Fatalf("Write drop: %v", err)
		}
		if drops != 1 {
			t.Errorf("callback fired %d times, want 1 (per-line semantics)", drops)
		}

		// Drain to keep the test from leaking goroutines.
		for i := 0; i < 64; i++ {
			<-ch1
			<-ch2
		}
	})

	t.Run("MultiLineWriteMultipleIncrements", func(t *testing.T) {
		// A single Write carrying N newline-terminated lines
		// against a full subscriber channel must fire the
		// callback N times — once per dropped line, NOT
		// once per Write. This is the load-bearing case the
		// peer review of PR #728 pinned: pre-fix the code
		// matched the "once per Write" doc claim only because
		// every prior test used single-line Writes; a
		// multi-line Write would have surprised operators by
		// undercounting the dashboard rate.
		r := New(1 << 20)
		ch, cancel := r.Subscribe()
		defer cancel()
		var drops int
		r.SetSlowSubscriberCallback(func() { drops++ })

		// Fill the subscriber channel.
		buf := make([]byte, 0, 64*4)
		for i := 0; i < 64; i++ {
			buf = append(buf, "x\n"...)
		}
		if _, err := r.Write("stdout", buf); err != nil {
			t.Fatalf("Write fill: %v", err)
		}

		// Single Write carrying 5 newline-terminated lines.
		// Expect exactly 5 callback fires.
		multi := []byte("a\nb\nc\nd\ne\n")
		if _, err := r.Write("stdout", multi); err != nil {
			t.Fatalf("Write multi-line: %v", err)
		}
		if drops != 5 {
			t.Errorf("callback fired %d times for a 5-line Write, want 5 (per-line semantics)", drops)
		}

		// Drain to keep the test from leaking goroutines.
		for i := 0; i < 64; i++ {
			<-ch
		}
	})
}
