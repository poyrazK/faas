// adr: 1194
// adr: 1195
// adr: 1196
// adr: 1197
package acceptance_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// The v1 flow end to end: a bounded workflow Operation runs a step, waits for
// a payment event, is resumed by a signal and finishes; its timeline tells
// that story in order.
func TestOperationV1FlowTimeline(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := signalOperationSetup(t, store, time.Hour)
		op := f.start(t)
		f.drive(t, op.ID, "event wait", waitingFor(api.OperationWaitEvent, "payment"))
		if _, err := f.signal(ctx, op.ID, "evt_1", "payment.succeeded", `{"charge":"ch_1"}`); err != nil {
			t.Fatal(err)
		}
		done := f.drive(t, op.ID, "resume", func(op state.Operation) bool { return op.State == api.OperationSucceeded })
		entries := readOperationTimeline(t, store.(state.OperationTimelineStore), f.account.ID, done.ID, 2)
		var story []string
		for i, e := range entries {
			if i > 0 && e.At.Before(entries[i-1].At) {
				t.Fatalf("timeline out of order at %d: %+v", i, e)
			}
			switch {
			case e.Event != nil && e.Event.Type != "progress":
				story = append(story, e.Event.Type)
			case e.Attempt != nil:
				story = append(story, fmt.Sprintf("%s/%d/%s", e.Attempt.Step, e.Attempt.Attempt, e.Attempt.Outcome))
			}
		}
		want := []string{"accepted", "validate/1/succeeded", "waiting", "signal_received", "resumed", "finish/1/succeeded", "succeeded"}
		if !containsOrdered(story, want) {
			t.Fatalf("timeline story %s, want %s in order", strings.Join(story, " "), strings.Join(want, " "))
		}
	})
}
