//go:build metal

// adr: 1151 — a restored guest is quiet-paused between requests and resumed
// for each one; no request meets a paused guest, an open stream is never
// paused, and a paused guest snapshots and destroys cleanly.
package fcvm

import (
	"context"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalQuietPause restores a guest from a snapshot with quiet pause on
// and drives it the way vmmd's forward RPCs do: mark activity, resume, send
// the request, mark it done. FAAS_TEST_QUIET_CYCLES sets the number of
// pause/resume cycles (default 1000); FAAS_TEST_QUIET_LOAD sets how long the
// concurrent load phase runs (default 20s).
func TestMetalQuietPause(t *testing.T) {
	kernel, base, layer := metalImages(t)
	cycles := envInt(t, "FAAS_TEST_QUIET_CYCLES", 1000)
	load := 20 * time.Second
	if raw := os.Getenv("FAAS_TEST_QUIET_LOAD"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("FAAS_TEST_QUIET_LOAD=%q: %v", raw, err)
		}
		load = d
	}
	const after = 100 * time.Millisecond
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatalf("create snapshot root: %v", err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatalf("create snapshot storage: %v", err)
	}
	tracker := activity.NewWithDefaults()
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, fcVersion, nil, nil).
		WithQuietPause(QuietPauseConfig{Activity: tracker, Tick: 20 * time.Millisecond})
	withCgroupRootAt(t, "/sys/fs/cgroup")
	pauseCtx, stopPause := context.WithCancel(ctx)
	defer stopPause()
	go m.RunQuietPause(pauseCtx)

	const id = "quiet"
	snapDir := t.TempDir()
	snap := &Snapshot{
		FCVersion: fcVersion, StorageKey: "snap/quiet/mem", VMStateStorageKey: "snap/quiet/vmstate",
		VMStatePath: snapDir + "/vmstate",
	}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	wake := WakeRequest{Instance: id, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256,
		Snapshot: snap, QuietPauseAfter: after}
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: id, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256,
	}); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
	if _, err := m.Park(ctx, id, spec); err != nil {
		t.Fatalf("prime park: %v", err)
	}
	inst, err := m.Wake(ctx, wake)
	if err != nil {
		t.Fatalf("restore wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("wake fell back to %s; the test needs a restored guest", inst.Method)
	}
	url := "http://" + inst.Lease.HostIP.String() + ":8080/"
	// request is what vmmd's forward RPCs do around one forwarded request.
	client := &http.Client{Timeout: 2 * time.Second}
	request := func() (resume, total time.Duration, err error) {
		started := time.Now()
		tracker.Begin(id)
		defer tracker.End(id)
		if err := m.QuietResume(ctx, id); err != nil {
			return 0, 0, err
		}
		resume = time.Since(started)
		resp, err := client.Get(url)
		if err != nil {
			return resume, 0, err
		}
		_ = resp.Body.Close()
		return resume, time.Since(started), nil
	}
	// serve waits for a freshly woken guest to answer through the forward
	// path, as the first request after a wake does.
	serve := func(within time.Duration) error {
		deadline := time.Now().Add(within)
		for {
			_, _, err := request()
			if err == nil || time.Now().After(deadline) {
				return err
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := serve(5 * time.Second); err != nil {
		t.Fatalf("restored guest does not serve: %v", err)
	}

	// Phase 1: pause/resume cycles.
	var resumes, firsts []time.Duration
	for i := 0; i < cycles; i++ {
		waitQuietPaused(t, m, id, true, 2*time.Second)
		if i%250 == 0 {
			if _, err := (&http.Client{Timeout: 300 * time.Millisecond}).Get(url); err == nil {
				t.Fatalf("cycle %d: a quiet-paused guest answered HTTP", i)
			}
		}
		resume, total, err := request()
		if err != nil {
			t.Fatalf("cycle %d: request: %v", i, err)
		}
		resumes, firsts = append(resumes, resume), append(firsts, total)
	}
	t.Logf("%d cycles: resume p50=%s p99=%s; resume+response p50=%s p99=%s",
		cycles, pct(resumes, 50), pct(resumes, 99), pct(firsts, 50), pct(firsts, 99))

	// Phase 2: concurrent requests with gaps around the pause delay. Every
	// request must succeed; none may meet a paused guest.
	var failures, sent atomic.Int64
	var mu sync.Mutex
	var loaded []time.Duration
	deadline := time.Now().Add(load)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				time.Sleep(time.Duration(rand.Int64N(int64(3 * after))))
				_, total, err := request()
				sent.Add(1)
				if err != nil {
					failures.Add(1)
					t.Errorf("load request: %v", err)
					continue
				}
				mu.Lock()
				loaded = append(loaded, total)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	t.Logf("load: %d requests, %d failed, resume+response p50=%s p99=%s max=%s",
		sent.Load(), failures.Load(), pct(loaded, 50), pct(loaded, 99), pct(loaded, 100))

	// Phase 3: an open stream (activity begun, not ended) is never paused.
	tracker.Begin(id)
	if err := m.QuietResume(ctx, id); err != nil {
		t.Fatalf("resume for the stream: %v", err)
	}
	time.Sleep(10 * after)
	if paused, _ := m.QuietPauseState(id); paused {
		t.Fatal("instance paused while a stream was open")
	}
	if _, err := firstByte(url, time.Second); err != nil {
		t.Fatalf("guest stopped serving during an open stream: %v", err)
	}
	if paused, _ := m.QuietPauseState(id); paused {
		t.Fatal("instance paused while a stream was open")
	}
	tracker.End(id)
	waitQuietPaused(t, m, id, true, 2*time.Second)

	// Phase 4: a quiet-paused guest parks (the park flight resumes it first),
	// wakes again from that snapshot, and is destroyed while paused.
	if _, err := m.Park(ctx, id, spec); err != nil {
		t.Fatalf("park a quiet-paused guest: %v", err)
	}
	inst, err = m.Wake(ctx, wake)
	if err != nil {
		t.Fatalf("wake after parking a quiet-paused guest: %v", err)
	}
	url = "http://" + inst.Lease.HostIP.String() + ":8080/"
	if err := serve(5 * time.Second); err != nil {
		t.Fatalf("guest restored from a quiet-paused snapshot does not serve: %v", err)
	}
	waitQuietPaused(t, m, id, true, 2*time.Second)
	if err := m.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy a quiet-paused guest: %v", err)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}

func waitQuietPaused(t *testing.T, m *Manager, id string, want bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if paused, _ := m.QuietPauseState(id); paused == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance quiet-paused != %v after %s", want, within)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func pct(ds []time.Duration, p int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	i := (len(s) - 1) * p / 100
	return s[i]
}

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q must be a positive integer", name, raw)
	}
	return n
}
