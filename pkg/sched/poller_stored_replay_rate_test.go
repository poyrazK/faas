package sched_test

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TestStoredTriggerReplayHonorsRateTrimmedBatch: replays are listed before the
// per-plan record rate trims the batch, so the source hands out only what the
// budget took instead of overspending it (and refunding a negative amount).
func TestStoredTriggerReplayHonorsRateTrimmedBatch(t *testing.T) {
	records := make([]sqlc.TriggerRecord, 10)
	for i := range records {
		records[i] = sqlc.TriggerRecord{ItemIdentifier: fmt.Sprintf("orders/0/%d", i), Payload: []byte(`{}`)}
	}
	for _, tc := range []struct {
		batch int32
		want  int
	}{{batch: 3, want: 3}, {batch: 100, want: 10}, {batch: 0, want: 10}} {
		res := sched.PollStoredTriggerReplays(records, sqlc.Trigger{Kind: "kafka", BatchSizeMax: tc.batch})
		if res.Error != nil || len(res.Records) != tc.want {
			t.Fatalf("batch %d: records=%d error=%v, want %d", tc.batch, len(res.Records), res.Error, tc.want)
		}
		if !res.Records[0].StoredReplay || res.Records[0].ItemIdentifier != "orders/0/0" {
			t.Fatalf("batch %d: first record %+v", tc.batch, res.Records[0])
		}
	}
}
