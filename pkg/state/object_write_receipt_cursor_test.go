package state

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestObjectWriteReceiptCursorTiesAndLiveChanges(t *testing.T) {
	m := NewMemStore()
	bucket := uuid.NewString()
	m.objectBuckets = map[string]ObjectBucket{bucket: {ID: bucket, AccountID: "account", AppID: "app"}}
	m.objectUploadCompletions = make(map[string]ObjectUploadCompletion)
	stamp := time.Now().UTC().Truncate(time.Microsecond)
	ids := []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}
	for _, id := range ids {
		m.objectUploadCompletions[id] = ObjectUploadCompletion{ID: id, AccountID: "account", AppID: "app", BucketID: bucket, Origin: "gateway", Key: "key", Status: "pending", WritePhase: ObjectUploadDispatched, CreatedAt: stamp}
	}
	page, err := m.ListObjectWriteReceipts(context.Background(), "account", "app", bucket, "pending", 1, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != ids[2] || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	// The cursor remains valid if its row settles before the next page.
	c := m.objectUploadCompletions[ids[2]]
	c.Status, c.WritePhase = "completed", ObjectUploadSettled
	m.objectUploadCompletions[c.ID] = c
	next, err := m.ListObjectWriteReceipts(context.Background(), "account", "app", bucket, "pending", 2, page.NextCursor)
	if err != nil || len(next.Items) != 2 || next.Items[0].ID != ids[1] || next.Items[1].ID != ids[0] || next.NextCursor != "" {
		t.Fatal(next, err)
	}
	if _, err = parseObjectWriteReceiptCursor(uuid.NewString(), "pending", page.NextCursor); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	c.ErrorCode = "private upstream cause"
	if projection := ViewObjectWriteReceipt(c); projection.ErrorCode != "" {
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
