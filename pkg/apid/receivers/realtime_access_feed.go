package receivers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// realtimeAccessFeedSize bounds the changes apid keeps for realtime nodes
	// to catch up on; a node further behind drops its whole cache instead.
	realtimeAccessFeedSize = 4096
	// realtimeAccessFeedPage bounds one answer.
	realtimeAccessFeedPage = 512
)

// RealtimeAccessFeed relays committed access changes and ticket revocations
// to realtime nodes (ADR-1113, amendment of 2026-11-02). Nodes cache access
// decisions and poll this feed once a second instead of reading every
// connection's access from Postgres every second. It lives in memory: its
// epoch changes whenever a change may have been missed, which tells every
// node to drop its cache.
type RealtimeAccessFeed struct {
	mu        sync.Mutex
	listening bool
	epoch     string
	seq       int64
	changes   []realtimeAccessFeedEntry
}

type realtimeAccessFeedEntry struct {
	seq    int64
	change state.ManagedRealtimeAccessChange
}

// StartRealtimeAccessFeed starts relaying store's access changes until ctx
// ends. It returns nil when store cannot listen for them; nodes then keep
// reading access per connection.
func StartRealtimeAccessFeed(ctx context.Context, store any, log *slog.Logger) *RealtimeAccessFeed {
	listener, ok := store.(state.ManagedRealtimeAccessChangeListener)
	if !ok {
		return nil
	}
	feed := &RealtimeAccessFeed{}
	events := listener.WatchManagedRealtimeAccessChanges(ctx)
	go func() {
		for event := range events {
			feed.apply(event)
			if event.Err != nil && ctx.Err() == nil && log != nil {
				log.WarnContext(ctx, "realtime access change listener unavailable; realtime nodes read access directly", "error", event.Err)
			}
		}
		feed.apply(state.ManagedRealtimeAccessChangeEvent{InvalidateAll: true})
	}()
	return feed
}

func (f *RealtimeAccessFeed) apply(event state.ManagedRealtimeAccessChangeEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listening = event.Listening
	if event.InvalidateAll {
		f.epoch = newRealtimeAccessFeedEpoch()
		f.changes = f.changes[:0]
		return
	}
	if !event.Listening || event.Change.EndpointID == "" {
		return
	}
	f.seq++
	if len(f.changes) >= realtimeAccessFeedSize {
		copy(f.changes, f.changes[1:])
		f.changes = f.changes[:len(f.changes)-1]
	}
	f.changes = append(f.changes, realtimeAccessFeedEntry{seq: f.seq, change: event.Change})
}

// since answers a node that has seen the feed up to (epoch, after). ok is
// false while apid is not listening, so the node keeps reading directly.
func (f *RealtimeAccessFeed) since(epoch string, after int64) (state.ManagedRealtimeAccessChanges, bool) {
	if f == nil {
		return state.ManagedRealtimeAccessChanges{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.listening {
		return state.ManagedRealtimeAccessChanges{}, false
	}
	page := state.ManagedRealtimeAccessChanges{Epoch: f.epoch, Seq: f.seq}
	oldest := f.seq + 1
	if len(f.changes) > 0 {
		oldest = f.changes[0].seq
	}
	// A new epoch, a position from the future, or one the feed no longer
	// holds: the node may have missed a change and must start over.
	if epoch != f.epoch || after > f.seq || after < oldest-1 {
		page.Reset = true
		return page, true
	}
	for _, entry := range f.changes {
		if entry.seq <= after {
			continue
		}
		if len(page.Changes) == realtimeAccessFeedPage {
			page.More = true
			break
		}
		page.Changes = append(page.Changes, entry.change)
		page.Seq = entry.seq
	}
	if len(page.Changes) == 0 {
		page.Seq = f.seq
	}
	return page, true
}

func newRealtimeAccessFeedEpoch() string {
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}
