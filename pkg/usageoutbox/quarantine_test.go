package usageoutbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An event apid will never accept blocked every later event from the node
// and, once the outbox filled, withheld billable responses. A quarantined
// event is kept in quarantine.jsonl and the cursor moves on (bug hunt
// 2026-10-28 H38).
func TestQuarantineMovesPastARejectedEvent(t *testing.T) {
	dir := t.TempDir()
	q, err := Open(dir, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"evt-rejected", "evt-next"} {
		if err := q.Enqueue(testEvent(id)); err != nil {
			t.Fatal(err)
		}
	}
	item, ok, err := q.Next()
	if err != nil || !ok || item.Event.EventID != "evt-rejected" {
		t.Fatalf("Next = %+v, %v, %v", item.Event.EventID, ok, err)
	}
	if err := q.Quarantine(item, "rpc error: code = InvalidArgument"); err != nil {
		t.Fatal(err)
	}
	next, ok, err := q.Next()
	if err != nil || !ok || next.Event.EventID != "evt-next" {
		t.Fatalf("after quarantine Next = %+v, %v, %v; want evt-next", next.Event.EventID, ok, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "quarantine.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"event_id":"evt-rejected"`) || !strings.Contains(string(data), "InvalidArgument") {
		t.Fatalf("quarantine file = %s, want the rejected event and its reason", data)
	}
}
