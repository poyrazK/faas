package fcvm

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// newPhaseLogManager builds a Manager whose logger writes to buf so a
// test can assert on the phase-breakdown line.
func newPhaseLogManager(t *testing.T, run Runner, vmm VMM, buf *bytes.Buffer) *Manager {
	t.Helper()
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewManager(run, vmm, Paths{Kernel: "/srv/fc/base/vmlinux-6.1"}, testFCVersion, log, nil)
}

// TestWakeFailure_LogsPhaseBreakdown is the regression guard for the
// diagnosis gap behind the 2026-09-03 cold-boot investigation.
//
// A prime cold boot ran 51s against a 35s budget and the journal held
// exactly two lines for the whole window — an unrelated "events: emit"
// and the final failure. The error surfaced at `ip addr add` with
// "context canceled", which says only that the deadline had already
// passed, not which phase consumed it. bringUpTimings existed but rode
// on the Instance for the success path only; an error return discarded
// it, and everything before setupNetwork was never measured at all.
//
// Every failed wake must now say where the time went.
func TestWakeFailure_LogsPhaseBreakdown(t *testing.T) {
	var buf bytes.Buffer
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newPhaseLogManager(t, run, vmm, &buf)

	// Empty plan fails fast, before any I/O — the cheapest failure
	// path, and still required to report its breakdown.
	_, err := m.Wake(context.Background(), WakeRequest{
		Instance: "phase-fail", BaseKey: "/b.ext4", LayerKey: "/l.ext4",
		VcpuCount: 2, MemSizeMiB: 128, Plan: "",
	})
	if err == nil {
		t.Fatal("Wake with empty plan: expected error")
	}

	got := buf.String()
	if !strings.Contains(got, "wake failed; phase breakdown") {
		t.Fatalf("no phase breakdown logged on failure; a failed wake must say where the time went.\ngot: %s", got)
	}
	for _, want := range []string{"instance=phase-fail", "total_ms=", "lease_acquire_ms="} {
		if !strings.Contains(got, want) {
			t.Errorf("phase breakdown missing %q\ngot: %s", want, got)
		}
	}
}

// TestWakeFast_DoesNotLogPhaseBreakdown keeps the instrumentation quiet
// on the hot path. A line per successful wake would bury the signal in
// exactly the journal an operator greps during an incident.
func TestWakeFast_DoesNotLogPhaseBreakdown(t *testing.T) {
	var buf bytes.Buffer
	run := &fakeRunner{}
	vmm := &fakeVMM{}
	m := newPhaseLogManager(t, run, vmm, &buf)

	_, err := m.Wake(context.Background(), WakeRequest{
		Instance: "phase-fast", BaseKey: "/b.ext4", LayerKey: "/l.ext4",
		VcpuCount: 2, MemSizeMiB: 128, Plan: "hobby",
	})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if strings.Contains(buf.String(), "phase breakdown") {
		t.Errorf("fast successful wake logged a phase breakdown; only failures and waking slower than SlowWakeLogThreshold should\ngot: %s", buf.String())
	}
}

// TestSlowWakeThresholdBelowColdBootBudget pins the relationship that
// makes the slow-success line useful: it must fire while wakes are
// still SUCCEEDING, so a creeping regression shows up in the journal
// before it starts tripping sched.ColdBootTimeout (35s). A threshold at
// or above the budget would only ever log wakes that already failed,
// which the failure branch covers anyway.
func TestSlowWakeThresholdBelowColdBootBudget(t *testing.T) {
	const schedColdBootTimeout = 35 * time.Second // pkg/sched.ColdBootTimeout
	if SlowWakeLogThreshold >= schedColdBootTimeout {
		t.Errorf("SlowWakeLogThreshold (%s) must be below sched.ColdBootTimeout (%s) to catch slow-but-succeeding wakes",
			SlowWakeLogThreshold, schedColdBootTimeout)
	}
}

// TestWakePhases_AttrsShape pins the flattened key/value shape. The
// phases are emitted as <name>_ms scalars rather than a nested array so
// an operator can grep a single phase (`grep env_prepare_ms`) straight
// out of the journal.
func TestWakePhases_AttrsShape(t *testing.T) {
	w := newWakePhases()
	w.mark("alpha")
	w.mark("beta")

	attrs := w.attrs()
	if len(attrs)%2 != 0 {
		t.Fatalf("attrs must be key/value pairs, got %d elements", len(attrs))
	}
	var keys []string
	for i := 0; i < len(attrs); i += 2 {
		k, ok := attrs[i].(string)
		if !ok {
			t.Fatalf("attrs[%d] = %v, want a string key", i, attrs[i])
		}
		keys = append(keys, k)
	}
	joined := strings.Join(keys, ",")
	for _, want := range []string{"total_ms", "alpha_ms", "beta_ms"} {
		if !strings.Contains(joined, want) {
			t.Errorf("attrs missing %q; got keys %v", want, keys)
		}
	}
}

// A nil *wakePhases must be inert, not a panic: the defer in Wake runs
// on every return path including the earliest failures.
func TestWakePhases_NilSafe(t *testing.T) {
	var w *wakePhases
	w.mark("noop")
	if got := w.attrs(); got != nil {
		t.Errorf("nil wakePhases attrs = %v, want nil", got)
	}
}
