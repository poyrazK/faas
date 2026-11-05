package usageoutbox

import (
	"errors"
	"os"
	"testing"
)

// One failed append latched the outbox broken until a restart, withholding
// every attributed response and stopping delivery (bug hunt H103). It now
// reloads itself after recoverAfter, and lines past the last durable point,
// whose producers were told the write failed, are cut off rather than billed.
func TestBrokenOutboxRecoversAndDropsUndurableLines(t *testing.T) {
	saved := recoverAfter
	recoverAfter = 0
	t.Cleanup(func() { recoverAfter = saved })

	q, err := Open(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(testEvent("durable")); err != nil {
		t.Fatal(err)
	}
	// A torn write: a complete line appended past the durable end, then the
	// fsync fails and the producer is told so.
	f, err := os.OpenFile(q.dataPath, os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"event_id":"withheld","account_id":"a","app_id":"b","request_count":1}` + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	q.mu.Lock()
	q.end += int64(len(`{"event_id":"withheld","account_id":"a","app_id":"b","request_count":1}` + "\n"))
	q.pending++
	_ = q.failLocked(errors.New("sync usage outbox: injected"), true)
	q.mu.Unlock()

	if err := q.Enqueue(testEvent("after")); err != nil {
		t.Fatalf("enqueue after recovery: %v", err)
	}
	var got []string
	for {
		item, ok, err := q.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		got = append(got, item.Event.EventID)
		if err := q.Ack(item); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "durable" || got[1] != "after" {
		t.Fatalf("delivered %v, want durable then after (the withheld event dropped)", got)
	}
	if err := q.Health(); err != nil {
		t.Fatalf("health after recovery: %v", err)
	}
}
