// adr: 570 (ADR-104 consult coalescing)
package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type recoveryBatchCentral struct {
	*batchingCentral
	releaseBatch chan struct{}
}

func (b *recoveryBatchCentral) ConsumeTokens(ctx context.Context, scope, subject, plan string, rps, burst float64, n int) (int, int, error) {
	select {
	case <-b.releaseBatch:
		return b.batchingCentral.ConsumeTokens(ctx, scope, subject, plan, rps, burst, n)
	case <-ctx.Done():
		return 0, 0, ctx.Err()
	}
}

// A queued batch must carry the same recovery classification as a single
// consult. Freeze the clock so waiting out the breaker cannot hide a failure.
func TestLimiter_CoalescedFailurePreservesRecovery(t *testing.T) {
	for _, backoff := range []bool{false, true} {
		t.Run(fmt.Sprintf("backoff=%v", backoff), func(t *testing.T) {
			backend := &recoveryBatchCentral{batchingCentral: &batchingCentral{tokens: 10, batchErr: fmt.Errorf("batch statement: %w", &classifiedCentralTestError{backoff: backoff})}, releaseBatch: make(chan struct{})}
			frozen := time.Unix(1_700_000_000, 0)
			limiter := NewLimiterWithCentralAndClock(backend, func() time.Time { return frozen })
			const subject = "00000000-0000-0000-0000-000000000002"
			allow := func() bool {
				return limiter.AllowWithCentralParams(t.Context(), "appid", 100, 100, "app:"+subject+":scale")
			}
			backend.hold = make(chan struct{})
			backend.leaderStart = make(chan struct{})
			started := backend.leaderStart
			leader := make(chan bool, 1)
			go func() { leader <- allow() }()
			<-started
			followers := make(chan bool, 2)
			for range 2 {
				go func() { followers <- allow() }()
			}
			key := centralBatchKey{scope: "app", subjectID: subject, plan: "scale"}
			deadline := time.Now().Add(5 * time.Second)
			for {
				limiter.coalescer.mu.Lock()
				joined := 0
				if next := limiter.coalescer.inflight[key]; next != nil {
					joined = next.n
				}
				limiter.coalescer.mu.Unlock()
				if joined == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("followers joined=%d, want 2", joined)
				}
				time.Sleep(time.Millisecond)
			}
			close(backend.hold)
			if !<-leader {
				t.Fatal("healthy single consult refused the leader")
			}
			close(backend.releaseBatch)
			for range 2 {
				if <-followers {
					t.Fatal("failed batch admitted a local token")
				}
			}

			deadline = time.Now().Add(5 * time.Second)
			for {
				limiter.coalescer.mu.Lock()
				idle := len(limiter.coalescer.inflight) == 0
				limiter.coalescer.mu.Unlock()
				if idle {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("batch did not release its counter")
				}
				time.Sleep(time.Millisecond)
			}
			backend.mu.Lock()
			backend.batchErr = nil
			backend.mu.Unlock()
			if got := allow(); got != !backoff {
				t.Fatalf("immediate recovery admitted=%v, want %v", got, !backoff)
			}
			wantSingles := int64(2)
			if backoff {
				wantSingles = 1
			}
			if got := backend.singles.Load(); got != wantSingles {
				t.Fatalf("single consults=%d, want %d", got, wantSingles)
			}
			backend.mu.Lock()
			defer backend.mu.Unlock()
			if len(backend.batchSize) != 1 || backend.batchSize[0] != 2 {
				t.Fatalf("batches=%v, want one failed batch of 2", backend.batchSize)
			}
		})
	}
}
