package realtime

// adr: 314

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkCallbackOutboxClaimNextBacklog(b *testing.B) {
	for _, backlog := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("pending_%d", backlog), func(b *testing.B) {
			queue := &CallbackOutbox{
				items:       make(map[string]*callbackOutboxItem, backlog),
				inFlight:    make(map[string]struct{}),
				connections: make(map[string]*callbackConnectionQueue, backlog),
			}
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := range backlog {
				id := fmt.Sprintf("evt_%08d", i)
				item := &callbackOutboxItem{
					record: callbackOutboxRecord{Event: Event{
						ID: id, Type: EventMessage, ConnectionID: fmt.Sprintf("connection_%08d", i),
						At: start.Add(time.Duration(i) * time.Nanosecond),
					}},
					readyIndex: -1,
					retryIndex: -1,
				}
				queue.items[id] = item
				queue.addConnectionItemLocked(item)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				event, ok, err := queue.ClaimNext()
				if err != nil || !ok {
					b.Fatalf("ClaimNext = (%+v, %v, %v)", event, ok, err)
				}
				queue.Release(event.ID)
			}
		})
	}
}
