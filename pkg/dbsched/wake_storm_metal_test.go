//go:build metal && linux

// adr: 1027 — a wake storm on real database VMs: every parked database on a
// node is woken at once, unbounded and then within two boot slots; and a
// wake over the node's RAM ceiling parks an idle database to make room.
package dbsched

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMetalWakeStorm(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the scheduler metal test")
	}
	s, store, _ := metalScheduler(t, workdir, base)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	nodes, err := store.Nodes(ctx)
	if err != nil || len(nodes) != 1 {
		t.Fatalf("nodes = %+v, %v", nodes, err)
	}
	node := nodes[0]
	setNode := func(ceilingMB, slots int) {
		n := node
		n.AdmissionCeilingMB, n.BootSlots = ceilingMB, slots
		seedNodes(t, store, n)
	}
	const count = 6
	var ids []string
	for i := 0; i < count; i++ {
		id := uuid.NewString()
		t.Cleanup(func() { _ = s.Delete(context.Background(), id) })
		if _, err := s.Provision(ctx, ProvisionRequest{DatabaseID: id, Major: 17, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30}); err != nil {
			t.Fatalf("provision %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	// storm wakes every database at once and parks them again, returning
	// the wall time, each wake's latency and the most VMs seen booting.
	storm := func(slots int) (time.Duration, []time.Duration, int) {
		setNode(1<<20, slots)
		var mu sync.Mutex
		var latencies []time.Duration
		maxWaking := 0
		stop := make(chan struct{})
		sampled := make(chan struct{})
		go func() {
			defer close(sampled)
			for {
				waking, _ := store.List(ctx, StateWaking)
				mu.Lock()
				maxWaking = max(maxWaking, len(waking))
				mu.Unlock()
				select {
				case <-stop:
					return
				case <-time.After(5 * time.Millisecond):
				}
			}
		}()
		started := time.Now()
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				began := time.Now()
				if _, err := s.Wake(ctx, id); err != nil {
					t.Errorf("wake %s in a storm of %d with %d slots: %v", id, count, slots, err)
					return
				}
				mu.Lock()
				latencies = append(latencies, time.Since(began))
				mu.Unlock()
			}(id)
		}
		wg.Wait()
		total := time.Since(started)
		close(stop)
		<-sampled
		for _, id := range ids {
			if err := s.Park(ctx, id); err != nil {
				t.Fatalf("park %s: %v", id, err)
			}
		}
		slices.Sort(latencies)
		return total, latencies, maxWaking
	}
	report := func(label string, total time.Duration, latencies []time.Duration, maxWaking int) {
		if len(latencies) != count {
			t.Fatalf("%s: %d of %d wakes succeeded", label, len(latencies), count)
		}
		t.Logf("%s: all %d awake in %v; wake p50 %v, max %v; at most %d booting at once", label, count,
			total.Round(time.Millisecond), latencies[count/2].Round(time.Millisecond), latencies[count-1].Round(time.Millisecond), maxWaking)
	}
	total, latencies, maxWaking := storm(count)
	report("unbounded", total, latencies, maxWaking)
	total, latencies, maxWaking = storm(2)
	report("2 boot slots", total, latencies, maxWaking)
	if maxWaking > 2 {
		t.Fatalf("%d databases booting at once on a node with 2 boot slots", maxWaking)
	}

	// RAM pressure: the ceiling fits two 384 MiB VMs. With two running, a
	// third wake parks the one nobody has touched for ten minutes.
	setNode(800, 4)
	idle, busy, waking := ids[0], ids[1], ids[2]
	for _, id := range []string{idle, busy} {
		if _, err := s.Wake(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Touch(ctx, idle, time.Now().Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	if _, err := s.Wake(ctx, waking); err != nil {
		t.Fatalf("wake over the RAM ceiling: %v", err)
	}
	evictWake := time.Since(began)
	for id, want := range map[string]State{idle: StateParked, busy: StateRunning, waking: StateRunning} {
		if vm, err := s.Get(ctx, id); err != nil || vm.State != want {
			t.Fatalf("%s = %+v, %v; want %s", id, vm, err, want)
		}
	}
	t.Logf("wake over the RAM ceiling, parking an idle database first: %v", evictWake.Round(time.Millisecond))
}
