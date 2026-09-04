package activity

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedNow returns a time source that always reports t. Tests
// that need to advance the clock construct their own closure
// that dereferences a *time.Time; see
// TestLastAt_StampsOnEveryBegin for the pattern. (fixedNow
// captures t by value, so rebinding the local variable in
// the test does not change what the closure returns.)
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// TestBeginEnd_Basic — Begin increments, End decrements, and
// Inflight reports the live count + seen=true.
func TestBeginEnd_Basic(t *testing.T) {
	t.Parallel()
	c := New(fixedNow(time.Unix(1_000, 0)))

	c.Begin("inst-a")
	c.Begin("inst-a")
	c.Begin("inst-a")

	got, ok := c.Inflight("inst-a")
	if !ok {
		t.Fatalf("Inflight after 3 Begins returned ok=false, want true")
	}
	if got != 3 {
		t.Errorf("Inflight after 3 Begins = %d, want 3", got)
	}
	if got, ok := c.Total("inst-a"); !ok || got != 3 {
		t.Errorf("Total after 3 Begins = (%d, %v), want (3, true)", got, ok)
	}

	c.End("inst-a")
	got, _ = c.Inflight("inst-a")
	if got != 2 {
		t.Errorf("Inflight after 2 Begins + 1 End = %d, want 2", got)
	}

	c.End("inst-a")
	c.End("inst-a")
	got, _ = c.Inflight("inst-a")
	if got != 0 {
		t.Errorf("Inflight after fully matched Begin/End = %d, want 0", got)
	}
	if got, ok := c.Total("inst-a"); !ok || got != 3 {
		t.Errorf("Total after End calls = (%d, %v), want (3, true)", got, ok)
	}
}

// TestBeginWithoutEnd_StaysElevated — a Begin that never gets
// matched by End leaves the counter elevated. Forget is the
// recovery seam (called from vmmdgrpc.Server.Destroy).
func TestBeginWithoutEnd_StaysElevated(t *testing.T) {
	t.Parallel()
	c := New(fixedNow(time.Unix(0, 0)))

	for i := 0; i < 5; i++ {
		c.Begin("leaked")
	}
	got, ok := c.Inflight("leaked")
	if !ok {
		t.Fatalf("Inflight returned ok=false, want true")
	}
	if got != 5 {
		t.Errorf("Inflight after 5 un-matched Begins = %d, want 5", got)
	}
	// Forget drops the entry entirely — the next Begin starts
	// fresh at 1.
	c.Forget("leaked")
	got, ok = c.Inflight("leaked")
	if ok || got != 0 {
		t.Errorf("Inflight after Forget = (%d, %v), want (0, false)", got, ok)
	}
}

// TestEndWithoutBegin_NoPanic — End on an instance the cache
// has never observed is a no-op. ForwardHTTP's defer pair can
// reach End on the rare path where the early validation
// returned before Begin ran; this is the tripwire.
func TestEndWithoutBegin_NoPanic(t *testing.T) {
	t.Parallel()
	c := New(nil)

	// Should not panic, should not allocate state.
	c.End("never-seen")

	got, ok := c.Inflight("never-seen")
	if ok || got != 0 {
		t.Errorf("Inflight after stray End = (%d, %v), want (0, false)", got, ok)
	}
}

// TestEmptyInstanceID_NoOp — Begin/End with "" are no-ops.
// ForwardHTTP already returns InvalidArgument for an empty
// instance, but the cache is defensive for any future caller.
func TestEmptyInstanceID_NoOp(t *testing.T) {
	t.Parallel()
	c := New(nil)

	c.Begin("")
	c.End("")
	c.Begin("")
	c.End("")

	got, ok := c.Inflight("")
	if ok || got != 0 {
		t.Errorf("Inflight(\"\") after Begin/End = (%d, %v), want (0, false)", got, ok)
	}
	if got := c.Size(); got != 0 {
		t.Errorf("Size after Begin(\"\")/End(\"\") = %d, want 0", got)
	}
}

// TestEnd_ClampedAtZero — five stray Ends after one Begin
// (somehow) leaves the counter at 0, not at -4. This is the
// pin that protects the Prometheus gauge from going
// negative on a late-arriving End after Destroy.
func TestEnd_ClampedAtZero(t *testing.T) {
	t.Parallel()
	c := New(nil)

	c.Begin("inst-x")
	for i := 0; i < 5; i++ {
		c.End("inst-x")
	}
	got, ok := c.Inflight("inst-x")
	if !ok {
		t.Fatalf("Inflight returned ok=false, want true")
	}
	if got != 0 {
		t.Errorf("Inflight after 1 Begin + 5 Ends = %d, want 0 (clamped)", got)
	}
}

// TestLastAt_StampsOnEveryBegin — lastAt advances on every
// Begin, not just on the first. Schedd reads LastAt via the
// wire's last_request_at timestamp; a stale reading would
// make the dashboard "last seen" pill stuck.
func TestLastAt_StampsOnEveryBegin(t *testing.T) {
	t.Parallel()
	// Use a pointer so the cache's now closure reads through
	// to the latest binding each time Begin stamps lastAt.
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	c := New(clock)

	c.Begin("inst-stamp")
	first, ok := c.LastAt("inst-stamp")
	if !ok {
		t.Fatalf("LastAt after first Begin returned ok=false, want true")
	}
	if !first.Equal(now) {
		t.Errorf("LastAt after first Begin = %v, want %v", first, now)
	}

	// Advance the clock by 10 seconds; a second Begin should
	// update lastAt to the new moment.
	now = now.Add(10 * time.Second)
	c.Begin("inst-stamp")
	second, ok := c.LastAt("inst-stamp")
	if !ok {
		t.Fatalf("LastAt after second Begin returned ok=false, want true")
	}
	if !second.Equal(now) {
		t.Errorf("LastAt after second Begin = %v, want %v (advanced)", second, now)
	}

	// End does NOT roll back the timestamp — lastAt is the
	// "last seen" moment, which is independent of "still
	// inflight".
	c.End("inst-stamp")
	third, ok := c.LastAt("inst-stamp")
	if !ok {
		t.Fatalf("LastAt after End returned ok=false, want true")
	}
	if !third.Equal(now) {
		t.Errorf("LastAt after End = %v, want %v (preserved through End)", third, now)
	}
}

// TestForget_RemovesInstance — Forget removes the entry from
// the map entirely. Size() drops; a follow-up Begin allocates
// fresh state at inflight=1.
func TestForget_RemovesInstance(t *testing.T) {
	t.Parallel()
	c := New(fixedNow(time.Unix(0, 0)))

	c.Begin("i1")
	c.Begin("i2")
	if c.Size() != 2 {
		t.Fatalf("Size after 2 Begin = %d, want 2", c.Size())
	}

	c.Forget("i1")
	if c.Size() != 1 {
		t.Errorf("Size after Forget(i1) = %d, want 1", c.Size())
	}
	if _, ok := c.Inflight("i1"); ok {
		t.Errorf("Inflight(i1) after Forget = ok=true, want false")
	}
	if _, ok := c.Inflight("i2"); !ok {
		t.Errorf("Inflight(i2) after Forget(i1) = ok=false, want true")
	}
}

// TestReset_WipesAll — Reset drops every entry. The tracker
// is reusable for the next test in the suite; this also pins
// the "drop everything" admin seam.
func TestReset_WipesAll(t *testing.T) {
	t.Parallel()
	c := New(nil)

	for i := 0; i < 10; i++ {
		c.Begin("inst-" + string(rune('a'+i)))
	}
	if c.Size() != 10 {
		t.Fatalf("Size after 10 Begin = %d, want 10", c.Size())
	}

	c.Reset()
	if c.Size() != 0 {
		t.Errorf("Size after Reset = %d, want 0", c.Size())
	}
	for i := 0; i < 10; i++ {
		if _, ok := c.Inflight("inst-" + string(rune('a'+i))); ok {
			t.Errorf("Inflight after Reset still reports ok=true for a forgotten instance")
		}
	}
}

// TestConcurrent_BeginEndConverges — 8 goroutines × 100 ops
// each, on a single shared instance. Every Begin is matched
// by a corresponding End on the SAME instance id, so after
// the goroutines return the inflight counter must converge
// to 0. Runs under `go test -race` to catch any map
// read/write races.
func TestConcurrent_BeginEndConverges(t *testing.T) {
	t.Parallel()
	c := New(nil)

	const goroutines = 8
	const opsPerG = 100
	const id = "inst-shared"

	var beginCount int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerG; i++ {
				if i%2 == 0 {
					c.Begin(id)
					atomic.AddInt64(&beginCount, 1)
				} else {
					c.End(id)
				}
			}
		}()
	}
	wg.Wait()

	// opsPerG is even (100), so each goroutine issues 50
	// Begins and 50 Ends. Across all 8 goroutines, 400
	// Begins and 400 Ends — every Begin is matched, so
	// inflight must converge to 0.
	if got := atomic.LoadInt64(&beginCount); got != int64(goroutines*opsPerG/2) {
		t.Fatalf("beginCount = %d, want %d", got, goroutines*opsPerG/2)
	}
	got, ok := c.Inflight(id)
	if !ok {
		t.Fatalf("Inflight(%q) after balanced ops = ok=false, want true", id)
	}
	if got != 0 {
		t.Errorf("Inflight(%q) after balanced ops = %d, want 0", id, got)
	}
}

// TestNew_DefaultNow — passing nil to New yields a tracker
// that uses real time.Now. Begin advances lastAt to roughly
// the wall-clock moment. The tolerance is generous so this
// is not a flake trap under -race.
func TestNew_DefaultNow(t *testing.T) {
	t.Parallel()
	c := New(nil)

	before := time.Now()
	c.Begin("default-now")
	last, ok := c.LastAt("default-now")
	if !ok {
		t.Fatalf("LastAt after Begin returned ok=false, want true")
	}
	after := time.Now()

	if last.Before(before.Add(-time.Second)) || last.After(after.Add(time.Second)) {
		t.Errorf("LastAt = %v, want within [%v, %v] (default time.Now)", last, before, after)
	}
}

// TestInflight_NeverSeen — a query for an instance the cache
// has never observed returns (0, false). The wire-side Stats
// handler relies on this to leave row.InflightRequests at the
// zero default and row.LastRequestAt nil.
func TestInflight_NeverSeen(t *testing.T) {
	t.Parallel()
	c := New(nil)

	c.Begin("inst-known")

	got, ok := c.Inflight("inst-unknown")
	if ok || got != 0 {
		t.Errorf("Inflight(unknown) = (%d, %v), want (0, false)", got, ok)
	}
	if _, ok := c.LastAt("inst-unknown"); ok {
		t.Errorf("LastAt(unknown) = ok=true, want false")
	}
}
