package receivers

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1113 — realtime nodes catch up on access changes from apid's feed,
// and start over whenever the feed may have missed one.
func TestRealtimeAccessFeedPages(t *testing.T) {
	var feed RealtimeAccessFeed
	if _, ok := feed.since("", 0); ok {
		t.Fatal("a feed that is not listening answered")
	}
	feed.apply(state.ManagedRealtimeAccessChangeEvent{Listening: true, InvalidateAll: true})
	start, ok := feed.since("", 0)
	if !ok || !start.Reset || start.Epoch == "" {
		t.Fatalf("a node's first poll = %+v, want a reset with the epoch", start)
	}
	for i := range 3 {
		feed.apply(state.ManagedRealtimeAccessChangeEvent{Listening: true, Change: state.ManagedRealtimeAccessChange{EndpointID: "ep", Principal: fmt.Sprint("user-", i)}})
	}
	page, _ := feed.since(start.Epoch, start.Seq)
	if page.Reset || len(page.Changes) != 3 || page.Seq != start.Seq+3 {
		t.Fatalf("catch-up page = %+v", page)
	}
	if next, _ := feed.since(page.Epoch, page.Seq); next.Reset || len(next.Changes) != 0 || next.Seq != page.Seq {
		t.Fatalf("an up-to-date poll = %+v", next)
	}

	// Paging: more than one page waits for the next poll.
	for i := range realtimeAccessFeedPage + 5 {
		feed.apply(state.ManagedRealtimeAccessChangeEvent{Listening: true, Change: state.ManagedRealtimeAccessChange{EndpointID: "ep", Principal: fmt.Sprint("bulk-", i)}})
	}
	first, _ := feed.since(page.Epoch, page.Seq)
	second, _ := feed.since(first.Epoch, first.Seq)
	if !first.More || len(first.Changes) != realtimeAccessFeedPage || second.More || len(second.Changes) != 5 {
		t.Fatalf("pages = %d (more %v), %d (more %v)", len(first.Changes), first.More, len(second.Changes), second.More)
	}

	// A node further behind than the feed holds, or one with another epoch,
	// starts over.
	for i := range realtimeAccessFeedSize + 1 {
		feed.apply(state.ManagedRealtimeAccessChangeEvent{Listening: true, Change: state.ManagedRealtimeAccessChange{EndpointID: "ep", Principal: fmt.Sprint("flood-", i)}})
	}
	if behind, _ := feed.since(second.Epoch, second.Seq); !behind.Reset {
		t.Fatalf("a node behind the feed = %+v, want a reset", behind)
	}
	feed.apply(state.ManagedRealtimeAccessChangeEvent{Listening: true, InvalidateAll: true})
	if reconnected, _ := feed.since(second.Epoch, feed.seq); !reconnected.Reset || reconnected.Epoch == second.Epoch {
		t.Fatalf("after the listener reconnected = %+v, want a new epoch and a reset", reconnected)
	}
	feed.apply(state.ManagedRealtimeAccessChangeEvent{})
	if _, ok := feed.since(second.Epoch, 0); ok {
		t.Fatal("a feed whose listener dropped still answered")
	}
}
