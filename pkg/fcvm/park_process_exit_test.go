// adr: 471
package fcvm

import (
	"context"
	"sync"
	"testing"
)

// Prod hunt #3: JailerVMM.Snapshot kills Firecracker after a successful
// capture, before Park's cleanup registers the teardown. The process-exit
// watcher saw a live, non-stopping instance and relayed "process_exited" for
// every park; production-us spooled one such liveness report per park.
func TestParkDoesNotRelayItsOwnFirecrackerExit(t *testing.T) {
	v := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, v)
	var mu sync.Mutex
	var relayed []string
	m.WithLivenessSink(func(_ context.Context, instanceID, reason string) {
		mu.Lock()
		defer mu.Unlock()
		relayed = append(relayed, instanceID+":"+reason)
	})
	if _, err := m.Wake(t.Context(), admissionWake("park-exit")); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	v.snapshotHook = func(l Lease) { m.ProcessExited(l.Instance, 0) }
	v.mu.Unlock()
	if _, err := m.Park(t.Context(), "park-exit", SnapshotSpec{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), relayed...)
	mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("park relayed its own Firecracker exit as a liveness failure: %q", got)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("park did not release the instance")
	}

	// An exit outside a park is still a liveness failure.
	v.mu.Lock()
	v.snapshotHook = nil
	v.mu.Unlock()
	if _, err := m.Wake(t.Context(), admissionWake("crashed")); err != nil {
		t.Fatal(err)
	}
	m.ProcessExited("crashed", 137)
	mu.Lock()
	defer mu.Unlock()
	if len(relayed) != 1 || relayed[0] != "crashed:process_exited" {
		t.Fatalf("unexpected exit relays = %q, want [crashed:process_exited]", relayed)
	}
}
