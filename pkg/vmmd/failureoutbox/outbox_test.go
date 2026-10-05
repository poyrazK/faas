// adr: 471 — retry, process restart and persistence failure coverage.
package failureoutbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openTestOutbox(t *testing.T, root string, send Sender) *Outbox {
	t.Helper()
	o, err := Open(root, send, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	o.retryBase, o.retryMax = 10*time.Millisecond, 40*time.Millisecond
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	return o
}

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !check() {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for outbox")
		case <-tick.C:
		}
	}
}

func TestOutboxRetriesReplayAndCoalesces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	first := Report{InstanceID: "old-instance", SourceNodeID: "node-host", Kind: Liveness, Reason: "timeout"}
	oom := Report{InstanceID: "old-instance", SourceNodeID: "node-host", Kind: WorkloadOOM, PeakMB: 300, PlanMB: 256}
	o := openTestOutbox(t, root, func(context.Context, Report) error { t.Error("delivery before start"); return nil })
	for _, r := range []Report{first, {InstanceID: first.InstanceID, Kind: Liveness, Reason: "process_exited"}, oom} {
		if err := o.Enqueue(r); err != nil {
			t.Fatal(err)
		}
	}
	if o.Pending() != 2 {
		t.Fatal("duplicate reports were not coalesced")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	attempts := make(map[string]int)
	reopened := openTestOutbox(t, root, func(_ context.Context, r Report) error {
		mu.Lock()
		defer mu.Unlock()
		want := first
		if r.Kind == WorkloadOOM {
			want = oom
		}
		want.Recovered = true
		if r != want {
			t.Errorf("replay changed identity or payload: %+v", r)
		}
		attempts[r.Kind]++
		if attempts[r.Kind] < 3 {
			return errors.New("schedd unavailable or negative ack")
		}
		return nil
	})
	reopened.Start(t.Context())
	eventually(t, func() bool { return reopened.Pending() == 0 })
	mu.Lock()
	defer mu.Unlock()
	if attempts[Liveness] != 3 || attempts[WorkloadOOM] != 3 {
		t.Fatalf("attempts = %v", attempts)
	}
	files, err := filepath.Glob(filepath.Join(root, "*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("acknowledged files remain: %v, %v", files, err)
	}
}

func TestOutboxProcessRestart(t *testing.T) {
	if os.Getenv("FAAS_FAILURE_OUTBOX_CHILD") == "1" {
		o, err := Open(os.Getenv("FAAS_FAILURE_OUTBOX_DIR"), func(context.Context, Report) error { return nil }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.Enqueue(Report{InstanceID: "crashed-instance", Kind: WorkloadOOM, PeakMB: 384, PlanMB: 256}); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // No Close, worker or shutdown hook: only the durable commit survives.
	}
	root := filepath.Join(t.TempDir(), "spool")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestOutboxProcessRestart$")
	cmd.Env = append(os.Environ(), "FAAS_FAILURE_OUTBOX_CHILD=1", "FAAS_FAILURE_OUTBOX_DIR="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v: %s", err, output)
	}
	var got atomic.Bool
	o := openTestOutbox(t, root, func(_ context.Context, r Report) error {
		if r != (Report{InstanceID: "crashed-instance", Kind: WorkloadOOM, PeakMB: 384, PlanMB: 256, Recovered: true}) {
			t.Errorf("crash replay = %+v", r)
		}
		got.Store(true)
		return nil
	})
	if o.Pending() != 1 {
		t.Fatal("process exit lost report")
	}
	o.Start(t.Context())
	eventually(t, func() bool { return got.Load() && o.Pending() == 0 })
}

func TestOutboxPersistenceFailureRetainsAndRetriesBeforeDelivery(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "spool")
	var delivered atomic.Bool
	r := Report{InstanceID: "instance", Kind: Liveness, Reason: "timeout"}
	o := openTestOutbox(t, root, func(_ context.Context, got Report) error {
		if got != r {
			t.Errorf("report = %+v", got)
		}
		if _, err := os.Stat(filepath.Join(root, r.key()+".json")); err != nil {
			t.Errorf("sent before persistence: %v", err)
		}
		delivered.Store(true)
		return nil
	})
	if err := os.Rename(root, root+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := o.Enqueue(r); err == nil {
		t.Fatal("persistence failure acknowledged")
	}
	if o.Pending() != 1 {
		t.Fatal("failed persistence lost in-memory retry")
	}
	if err := os.Rename(root+"-offline", root); err != nil {
		t.Fatal(err)
	}
	o.Start(t.Context())
	eventually(t, func() bool { return delivered.Load() && o.Pending() == 0 })
}

func TestOutboxBoundsWorkersAndCancelsWithPendingReports(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	var active, peak atomic.Int32
	o := openTestOutbox(t, root, func(ctx context.Context, _ Report) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		<-ctx.Done()
		return ctx.Err()
	})
	for n := range 12 {
		if err := o.Enqueue(Report{InstanceID: string(rune('a' + n)), Kind: Liveness}); err != nil {
			t.Fatal(err)
		}
	}
	o.Start(t.Context())
	eventually(t, func() bool { return active.Load() == workers })
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if peak.Load() != workers || active.Load() != 0 {
		t.Fatalf("workers peak=%d active=%d", peak.Load(), active.Load())
	}
	reopened := openTestOutbox(t, root, func(context.Context, Report) error { return nil })
	if reopened.Pending() != 12 {
		t.Fatal("shutdown lost pending reports")
	}
}

func TestOutboxRejectsCorruptSpoolAndConcurrentOwner(t *testing.T) {
	send := func(context.Context, Report) error { return nil }
	root := filepath.Join(t.TempDir(), "spool")
	o := openTestOutbox(t, root, send)
	if second, err := Open(root, send, nil); err == nil {
		_ = second.Close()
		t.Fatal("second owner acquired spool")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"{broken", `{"version":99}`, `{"version":1,"report":{"kind":"other"}}`} {
		if err := os.WriteFile(filepath.Join(root, "corrupt.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if invalid, err := Open(root, send, nil); err == nil {
			_ = invalid.Close()
			t.Fatal("corrupt spool was silently ignored")
		}
	}
	if err := os.Remove(filepath.Join(root, "corrupt.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".pending-interrupted"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened := openTestOutbox(t, root, send)
	if reopened.Pending() != 0 {
		t.Fatal("incomplete atomic write became a report")
	}
}
