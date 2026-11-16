package state_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// send-batch admitted each message in its own transaction (~34 ms each on
// production, 2026-11-12). One transaction now admits the batch in order
// against the same caps: a batch that reaches the app's pending cap keeps the
// messages under it and refuses the next with the depth error.
func TestPgEnqueueInvocationBatchAdmitsInOrderUpToTheCap(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	template := state.Invocation{AppID: appID, AccountID: accountID, Source: state.InvocationQueue, QueueName: "default",
		DueAt: time.Now().UTC(), AdmissionMaxPending: 5, AdmissionMaxBacklogBytes: 1 << 30}
	payloads := make([]json.RawMessage, 8)
	for i := range payloads {
		payloads[i] = json.RawMessage(`{"n":` + string(rune('0'+i)) + `}`)
	}
	var batcher state.InvocationBatchEnqueuer = store
	rows, err := batcher.EnqueueInvocationBatch(ctx, template, payloads)
	var depth *state.InvocationDepthError
	if !errors.As(err, &depth) || depth.Limit != 5 || len(rows) != 5 {
		t.Fatalf("batch past the cap = %d rows, err %v; want 5 rows and the depth error", len(rows), err)
	}
	for i, row := range rows {
		var got struct{ N int }
		if row.ID == "" || json.Unmarshal(row.Payload, &got) != nil || got.N != i {
			t.Fatalf("row %d = id %q payload %s, want payload %s in order", i, row.ID, row.Payload, payloads[i])
		}
	}
	stats, err := store.QueueAppTelemetry(ctx, appID, 100)
	if err != nil || stats.Depth != 5 {
		t.Fatalf("queue depth after the batch = %d, err %v; want 5", stats.Depth, err)
	}
	if _, err := batcher.EnqueueInvocationBatch(ctx, state.Invocation{AppID: appID, AccountID: accountID, Source: state.InvocationQueue,
		WorkPolicyName: "keyed"}, payloads); !errors.Is(err, state.ErrInvocationBatchUnsupported) {
		t.Fatalf("keyed template err = %v, want ErrInvocationBatchUnsupported", err)
	}
}

// Concurrent batches to one app serialise on its lock and together never pass
// the pending cap.
func TestPgConcurrentInvocationBatchesStayUnderTheCap(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	template := state.Invocation{AppID: appID, AccountID: accountID, Source: state.InvocationQueue, QueueName: "default",
		DueAt: time.Now().UTC(), AdmissionMaxPending: 150, AdmissionMaxBacklogBytes: 1 << 30}
	payloads := make([]json.RawMessage, 100)
	for i := range payloads {
		payloads[i] = json.RawMessage(`{}`)
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		admitted int
	)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := store.EnqueueInvocationBatch(ctx, template, payloads)
			if err != nil && !errors.Is(err, state.ErrQuotaExceeded) {
				t.Errorf("batch: %v", err)
			}
			mu.Lock()
			admitted += len(rows)
			mu.Unlock()
		}()
	}
	wg.Wait()
	stats, err := store.QueueAppTelemetry(ctx, appID, 1000)
	if err != nil || admitted != 150 || stats.Depth != 150 {
		t.Fatalf("admitted %d, depth %d, err %v; want exactly the cap of 150", admitted, stats.Depth, err)
	}
}
