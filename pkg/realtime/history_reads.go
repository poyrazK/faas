package realtime

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// History reads are shared per node (ADR-156, amendment of 2026-11-02). A
// retained publish wakes every resume subscriber of its channel on the node,
// and each used to read the channel's history from the control plane on its
// own; SSE streams polled once a second each. Readers of the same channel
// from the same position now share one read, and its page for
// historyShareFor. A read is keyed by the channel's publish generation on
// this node, so a subscriber woken by a publish never gets a page read
// before that publish.
const (
	historyShareFor      = time.Second
	historyShareMaxBytes = 16 << 20
	historyShareMaxKeys  = 65536
)

type historyReadKey struct {
	endpointID string
	channel    string
	after      int64
	gen        uint64
}

type historyRead struct {
	done  chan struct{}
	page  state.ManagedRealtimeChannelHistory
	err   error
	at    time.Time
	bytes int
}

type historyReads struct {
	mu    sync.Mutex
	gens  map[channelKey]uint64
	reads map[historyReadKey]*historyRead
	bytes int
}

// published moves a channel to a new generation before its subscribers are
// woken, so their reads start after the publish.
func (h *historyReads) published(key channelKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gens == nil || len(h.gens) >= historyShareMaxKeys {
		// Forgetting generations could match a page read before a publish
		// to the restarted counter, so forget the pages too.
		h.gens = map[channelKey]uint64{}
		h.reads, h.bytes = nil, 0
	}
	h.gens[key]++
}

// read returns a shared page for (endpoint, channel, after), reading it with
// fn when no reader of the current generation has one.
func (h *historyReads) read(ctx context.Context, endpointID, channel string, after int64, fn func(context.Context) (state.ManagedRealtimeChannelHistory, error)) (state.ManagedRealtimeChannelHistory, error) {
	h.mu.Lock()
	key := historyReadKey{endpointID: endpointID, channel: channel, after: after, gen: h.gens[channelKey{endpointID: endpointID, channel: channel}]}
	now := time.Now()
	if r := h.reads[key]; r != nil {
		select {
		case <-r.done:
			if now.Sub(r.at) < historyShareFor {
				h.mu.Unlock()
				return r.page, r.err
			}
			h.dropLocked(key, r)
		default:
			h.mu.Unlock()
			select {
			case <-r.done:
				return r.page, r.err
			case <-ctx.Done():
				return state.ManagedRealtimeChannelHistory{}, ctx.Err()
			}
		}
	}
	if h.reads == nil {
		h.reads = map[historyReadKey]*historyRead{}
	}
	if len(h.reads) >= historyShareMaxKeys {
		h.sweepLocked(now)
	}
	r := &historyRead{done: make(chan struct{})}
	h.reads[key] = r
	h.mu.Unlock()

	// Others wait on this read: it must not end with the caller that
	// started it.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resumeHistoryReadTimeout)
	page, err := fn(readCtx)
	cancel()

	h.mu.Lock()
	r.page, r.err, r.at = page, err, time.Now()
	close(r.done)
	if err != nil {
		h.dropLocked(key, r)
	} else {
		r.bytes = historyPageBytes(page)
		h.bytes += r.bytes
		if h.bytes > historyShareMaxBytes {
			h.sweepLocked(r.at)
			if h.bytes > historyShareMaxBytes {
				h.dropLocked(key, r)
			}
		}
	}
	h.mu.Unlock()
	return page, err
}

func (h *historyReads) dropLocked(key historyReadKey, r *historyRead) {
	if h.reads[key] == r {
		delete(h.reads, key)
		h.bytes -= r.bytes
	}
}

// sweepLocked drops finished reads older than historyShareFor.
func (h *historyReads) sweepLocked(now time.Time) {
	for key, r := range h.reads {
		select {
		case <-r.done:
			if now.Sub(r.at) >= historyShareFor {
				h.dropLocked(key, r)
			}
		default:
		}
	}
}

func historyPageBytes(page state.ManagedRealtimeChannelHistory) int {
	n := 256
	for _, message := range page.Messages {
		n += len(message.Data) + 256
		for k, v := range message.Metadata {
			n += len(k) + len(v)
		}
	}
	return n
}
