// adr: 070 — the stats poller reads each deployment's sidecar RAM once.

package instancestats

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// sidecarCountingStore counts DeploymentSidecarRAMs reads and can fail the
// next read for one deployment.
type sidecarCountingStore struct {
	*state.MemStore
	mu       sync.Mutex
	reads    map[string]int
	failNext map[string]bool
}

func (s *sidecarCountingStore) DeploymentSidecarRAMs(ctx context.Context, deploymentID string) ([]int, error) {
	s.mu.Lock()
	s.reads[deploymentID]++
	fail := s.failNext[deploymentID]
	delete(s.failNext, deploymentID)
	s.mu.Unlock()
	if fail {
		return nil, errors.New("connection refused")
	}
	return s.MemStore.DeploymentSidecarRAMs(ctx, deploymentID)
}

func (s *sidecarCountingStore) readsOf(deploymentID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[deploymentID]
}

// TestPoller_SidecarRAMReadOncePerDeployment pins the idle-load fix: the
// 200 ms poller used to re-read every deployment's sidecars on each tick,
// schedd's most frequent idle query on production-us. A deployment is now
// read once while it has instances, a failed read is retried, and a
// deployment that leaves the instance list is evicted.
func TestPoller_SidecarRAMReadOncePerDeployment(t *testing.T) {
	mem := state.NewMemStore()
	store := &sidecarCountingStore{MemStore: mem, reads: map[string]int{}, failNext: map[string]bool{"deploy-b": true}}
	_, live := seedTwoNodes(t, mem)
	ctx := context.Background()
	for range 2 {
		if _, err := mem.CreateInstance(ctx, "app1", "deploy-a", string(state.StateRunning), 256, live.ID, ""); err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
	}
	b1, err := mem.CreateInstance(ctx, "app2", "deploy-b", string(state.StateRunning), 256, live.ID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	p := NewPoller(store, &statsFakeDialer{}, nil, NewReader(), nil, nilLogger())

	for i := 0; i < 5; i++ {
		if err := p.Tick(ctx); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	if got := store.readsOf("deploy-a"); got != 1 {
		t.Fatalf("deploy-a reads over 5 ticks = %d, want 1", got)
	}
	// The first deploy-b read failed; the second tick retried it and cached it.
	if got := store.readsOf("deploy-b"); got != 2 {
		t.Fatalf("deploy-b reads over 5 ticks = %d, want 2 (one failure, one retry)", got)
	}

	if err := mem.DeleteInstance(ctx, b1.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if _, cached := p.sidecarMBs["deploy-b"]; cached {
		t.Fatal("deploy-b still cached after its last instance left")
	}
	if _, cached := p.sidecarMBs["deploy-a"]; !cached {
		t.Fatal("deploy-a evicted while it still has instances")
	}
}
