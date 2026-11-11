package state_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 32 H32-20: concurrent `queue send` calls to one app answered 503
// "Briefly at capacity — enqueue application message" for most of a burst
// of six. Capped admissions serialise on the app row; each must still be
// admitted while the app is under its caps.
func TestPgConcurrentCappedQueueSendsAreAdmitted(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	const senders = 8
	errs := make([]error, senders)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = store.EnqueueInvocation(ctx, state.Invocation{
				AppID: appID, AccountID: accountID, Source: state.InvocationQueue, QueueName: "default",
				Payload: json.RawMessage(`{"job_id":"h32-20"}`), DueAt: time.Now().UTC(),
				AdmissionMaxPending: 1000, AdmissionMaxBacklogBytes: 1 << 30,
			})
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("sender %d: %v", i, err)
		}
	}
}
