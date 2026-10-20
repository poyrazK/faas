package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestObjectWriteReceiptCursorTiesAndLiveChanges(t *testing.T) {
	m := state.NewMemStore()
	bucket := uuid.NewString()
	(*m.ObjectBuckets()) = map[string]state.ObjectBucket{bucket: {ID: bucket, AccountID: "account", AppID: "app"}}
	(*m.ObjectUploadCompletions()) = make(map[string]state.ObjectUploadCompletion)
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	ids := []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}
	for _, id := range ids {
		(*m.ObjectUploadCompletions())[id] = state.ObjectUploadCompletion{ID: id, AccountID: "account", AppID: "app", BucketID: bucket, Origin: "gateway", Key: "key", Status: "pending", WritePhase: state.ObjectUploadDispatched, CreatedAt: stamp}
	}
	page, err := m.ListObjectWriteReceipts(context.Background(), "account", "app", bucket, "pending", 1, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids[2] || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	// The cursor remains valid if its row settles before the next page.
	c := (*m.ObjectUploadCompletions())[ids[2]]
	c.Status, c.WritePhase = "completed", state.ObjectUploadSettled
	(*m.ObjectUploadCompletions())[c.ID] = c
	next, err := m.ListObjectWriteReceipts(context.Background(), "account", "app", bucket, "pending", 2, page.NextCursor)
	if err != nil || len(next.Items) != 2 || next.Items[0].ID != ids[1] || next.Items[1].ID != ids[0] || next.NextCursor != "" {
		t.Fatal(next, err)
	}
	if _, err = state.ParseObjectWriteReceiptCursor(uuid.NewString(), "pending", page.NextCursor); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	c.ErrorCode = "private upstream cause"
	if projection := state.ViewObjectWriteReceipt(c); projection.ErrorCode != "" {
		t.Fatal("private error exposed", projection)
	}
	for _, tc := range []struct {
		status string
		limit  int
		ok     bool
	}{{"", 0, true}, {"all", 100, true}, {"all", 101, false}, {"bad", 1, false}} {
		_, _, ok := api.ParseObjectWriteReceiptPage(tc.status, tc.limit, "")
		if ok != tc.ok {
			t.Fatal(tc, ok)
		}
	}
}

func TestObjectWriteReceiptPageBounds(t *testing.T) {
	rows := make([]state.ObjectUploadCompletion, api.ObjectWriteReceiptPageMax+1)
	for i := range rows {
		rows[i] = state.ObjectUploadCompletion{ID: uuid.NewString(), CreatedAt: time.Now().UTC()}
	}
	page := state.ObjectWriteReceiptPage(rows, "bucket", "all", api.ObjectWriteReceiptPageMax)
	if len(page.Items) != api.ObjectWriteReceiptPageMax || cap(page.Items) > api.ObjectWriteReceiptPageMax {
		t.Fatal("unbounded receipt page", len(page.Items), cap(page.Items))
	}
	cursor, err := state.ParseObjectWriteReceiptCursor("bucket", "all", page.NextCursor)
	if err != nil || cursor.ID != rows[api.ObjectWriteReceiptPageMax-1].ID {
		t.Fatal("maximum page lost continuation", cursor, err)
	}
	for _, limit := range []int{-1, 0, api.ObjectWriteReceiptPageMax + 1, int(^uint(0) >> 1)} {
		got := state.ObjectWriteReceiptPage(rows, "bucket", "all", limit)
		if got.Items == nil || len(got.Items) != 0 || got.NextCursor != "" {
			t.Fatal("invalid internal page limit accepted", limit, got)
		}
	}
}
