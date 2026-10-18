// adr: 1527
package events

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPlatformCommittedRecoveryDoesNotAppendAgain(t *testing.T) {
	store := failingStore{state.NewMemStore()}
	ops := newStubOps()
	bc := &stubBroadcaster{}
	p := NewPlatform("schedd", store, silentLog(), ops, bc)
	p.PublishCommittedRecovery(context.Background(), state.NodeLifecycleCompletionResult{
		NodeLifecycleCompletion: state.NodeLifecycleCompletion{NodeID: "11111111-1111-1111-1111-111111111111", Generation: 2, Lifecycle: state.NodeLifecycleDraining},
		ResultGeneration:        3, ResultLifecycle: state.NodeLifecycleMaintenance, CompletedAt: time.Now(),
	})
	// AppendEvent on this store always fails. Fan-out can succeed only by
	// observing the already-committed audit rather than trying another append.
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.recoveryCalls) != 1 || ops.recoveryCalls[0] != "drained:ok" {
		t.Fatalf("committed metric: %v", ops.recoveryCalls)
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.calls != 1 || bc.lastTopic != TopicRecovery {
		t.Fatalf("committed fan-out: calls=%d topic=%s", bc.calls, bc.lastTopic)
	}
}
