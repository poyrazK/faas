package realtime

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Live occupancy frames (ADR-1324). A resumable subscriber that subscribes
// with "occupancy": true receives {"type":"occupancy","channel":…,
// "subscribers":N} after subscribing and whenever the fleet-wide count
// changes, at most once per api.RealtimeOccupancyFrameInterval. Local changes
// show at the next interval; the other nodes' counts are refreshed through
// apid every api.RealtimeOccupancyRemoteRefresh.

// ManagedRealtimeOccupancyClient reads the other nodes' subscriber counts
// through apid. Nil keeps live occupancy local, which is exact on a single
// realtime node.
type ManagedRealtimeOccupancyClient interface {
	ReadRemoteOccupancy(ctx context.Context, endpointID string, channels []string) (map[string]int, error)
}

const (
	// occupancyRemoteTimeout bounds one apid read.
	occupancyRemoteTimeout = 2 * time.Second
	// occupancyFirstFrameWait holds a new watcher's first frame until the
	// other nodes' counts arrive, so the count does not jump from local to
	// fleet-wide; past it the frame goes out with what is known.
	occupancyFirstFrameWait = 2 * time.Second
	// occupancyRemoteWorkers bounds concurrent apid reads in one refresh.
	occupancyRemoteWorkers = 4
)

// occupancyWatcher receives live occupancy counts: a resumable WebSocket
// connection queues a frame, an SSE stream keeps the latest count for its
// writer.
type occupancyWatcher interface {
	sendOccupancy(ctx context.Context, channel string, count int) error
}

func (c *connection) sendOccupancy(ctx context.Context, channel string, count int) error {
	return c.m.queueResume(ctx, c, resumeServerFrame{Type: "occupancy", Channel: channel, Subscribers: &count})
}

// sseOccupancyWatcher hands an SSE stream the latest count; a count the
// stream has not written yet is replaced, never queued behind.
type sseOccupancyWatcher struct {
	updates chan int
}

func newSSEOccupancyWatcher() *sseOccupancyWatcher {
	return &sseOccupancyWatcher{updates: make(chan int, 1)}
}

// sendOccupancy is only called by the single flushing goroutine, so the
// drain-then-send loop always finishes.
func (w *sseOccupancyWatcher) sendOccupancy(_ context.Context, _ string, count int) error {
	for {
		select {
		case w.updates <- count:
			return nil
		default:
			select {
			case <-w.updates:
			default:
			}
		}
	}
}

// occupancyLive tracks which watchers follow which channels and what each was
// last sent. Its mutex may be taken while m.mu or c.mu is held, and is never
// held while queueing frames or calling apid.
type occupancyLive struct {
	mu       sync.Mutex
	watchers map[channelKey]map[occupancyWatcher]int // last count sent; -1 before the first frame
	since    map[channelKey]time.Time
	remote   map[channelKey]int
	remoteAt map[channelKey]time.Time
	dirty    map[channelKey]struct{}
}

func newOccupancyLive() occupancyLive {
	return occupancyLive{
		watchers: make(map[channelKey]map[occupancyWatcher]int),
		since:    make(map[channelKey]time.Time),
		remote:   make(map[channelKey]int),
		remoteAt: make(map[channelKey]time.Time),
		dirty:    make(map[channelKey]struct{}),
	}
}

// occupancyChanged marks a watched channel for the next frame.
func (m *Manager) occupancyChanged(key channelKey) {
	l := &m.occupancyLive
	l.mu.Lock()
	if _, watched := l.watchers[key]; watched {
		l.dirty[key] = struct{}{}
	}
	l.mu.Unlock()
}

// watchOccupancy subscribes a watcher to live occupancy for an endpoint
// channel. It reports false when the node already watches its maximum of
// channels.
func (m *Manager) watchOccupancy(endpointID, channel string, w occupancyWatcher) bool {
	key := channelKey{endpointID: endpointID, channel: channel}
	l := &m.occupancyLive
	l.mu.Lock()
	defer l.mu.Unlock()
	watchers := l.watchers[key]
	if watchers == nil {
		if len(l.watchers) >= api.RealtimeOccupancyWatchedMax {
			return false
		}
		watchers = make(map[occupancyWatcher]int)
		l.watchers[key] = watchers
		l.since[key] = time.Now()
	}
	watchers[w] = -1
	l.dirty[key] = struct{}{}
	return true
}

func (m *Manager) unwatchOccupancy(endpointID, channel string, w occupancyWatcher) {
	l := &m.occupancyLive
	l.mu.Lock()
	defer l.mu.Unlock()
	l.unwatchLocked(channelKey{endpointID: endpointID, channel: channel}, w)
}

// unwatchOccupancyConnection drops every watch of a removed connection.
func (m *Manager) unwatchOccupancyConnection(c *connection) {
	l := &m.occupancyLive
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, watchers := range l.watchers {
		if _, ok := watchers[c]; ok {
			l.unwatchLocked(key, c)
		}
	}
}

func (l *occupancyLive) unwatchLocked(key channelKey, w occupancyWatcher) {
	watchers := l.watchers[key]
	delete(watchers, w)
	if len(watchers) == 0 {
		delete(l.watchers, key)
		delete(l.since, key)
		delete(l.remote, key)
		delete(l.remoteAt, key)
		delete(l.dirty, key)
	}
}

func (m *Manager) startOccupancyFrames() {
	if m.cfg.occupancyManual || !m.cfg.OccupancyPreview {
		return
	}
	go m.runOccupancyTicker(m.ctx, api.RealtimeOccupancyFrameInterval, m.flushOccupancy) //nolint:contextcheck // the Manager owns the loop's lifecycle; Close cancels it.
	if m.cfg.OccupancyClient != nil {
		go m.runOccupancyTicker(m.ctx, api.RealtimeOccupancyFrameInterval, func(ctx context.Context) { //nolint:contextcheck // as above.
			m.refreshRemoteOccupancy(ctx, time.Now())
		})
	}
}

func (m *Manager) runOccupancyTicker(ctx context.Context, interval time.Duration, run func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run(ctx)
		}
	}
}

type occupancyDelivery struct {
	key      channelKey
	count    int
	watchers []occupancyWatcher
}

// flushOccupancy sends each changed channel's count to the watchers that have
// not seen it. A frame that cannot be queued is retried on the next flush;
// occupancy never closes a connection.
func (m *Manager) flushOccupancy(ctx context.Context) {
	l := &m.occupancyLive
	now := time.Now()
	l.mu.Lock()
	var deliveries []occupancyDelivery
	for key := range l.dirty {
		watchers := l.watchers[key]
		if m.cfg.OccupancyClient != nil && l.remoteAt[key].IsZero() && now.Sub(l.since[key]) < occupancyFirstFrameWait {
			continue
		}
		delete(l.dirty, key)
		total := m.occupancy.count(key) + l.remote[key]
		var pending []occupancyWatcher
		for w, last := range watchers {
			if last != total {
				pending = append(pending, w)
			}
		}
		if len(pending) > 0 {
			deliveries = append(deliveries, occupancyDelivery{key: key, count: total, watchers: pending})
		}
	}
	l.mu.Unlock()
	for _, delivery := range deliveries {
		for _, w := range delivery.watchers {
			count := delivery.count
			err := w.sendOccupancy(ctx, delivery.key.channel, count)
			l.mu.Lock()
			if watchers := l.watchers[delivery.key]; watchers != nil {
				if _, ok := watchers[w]; ok {
					if err == nil {
						watchers[w] = count
					} else {
						l.dirty[delivery.key] = struct{}{}
					}
				}
			}
			l.mu.Unlock()
		}
	}
}

// refreshRemoteOccupancy reads the other nodes' counts for watched channels
// that have none yet or whose counts are older than the refresh interval. A
// failed read keeps the last counts and waits a full interval before retrying.
func (m *Manager) refreshRemoteOccupancy(ctx context.Context, now time.Time) {
	client := m.cfg.OccupancyClient
	if client == nil {
		return
	}
	l := &m.occupancyLive
	due := make(map[string][]string)
	l.mu.Lock()
	for key := range l.watchers {
		if at := l.remoteAt[key]; at.IsZero() || now.Sub(at) >= api.RealtimeOccupancyRemoteRefresh {
			due[key.endpointID] = append(due[key.endpointID], key.channel)
		}
	}
	l.mu.Unlock()
	type batch struct {
		endpointID string
		channels   []string
	}
	jobs := make(chan batch)
	var workers sync.WaitGroup
	for range occupancyRemoteWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				readCtx, cancel := context.WithTimeout(ctx, occupancyRemoteTimeout)
				counts, err := client.ReadRemoteOccupancy(readCtx, job.endpointID, job.channels)
				cancel()
				if err != nil {
					m.reportFleetFailure(err)
				}
				l.mu.Lock()
				for _, channel := range job.channels {
					key := channelKey{endpointID: job.endpointID, channel: channel}
					if _, watched := l.watchers[key]; !watched {
						continue
					}
					l.remoteAt[key] = now
					if err == nil && l.remote[key] != counts[channel] {
						l.remote[key] = counts[channel]
						l.dirty[key] = struct{}{}
					}
				}
				l.mu.Unlock()
			}
		}()
	}
	for endpointID, channels := range due {
		for start := 0; start < len(channels); start += api.RealtimeOccupancyChannelsMax {
			jobs <- batch{endpointID: endpointID, channels: channels[start:min(start+api.RealtimeOccupancyChannelsMax, len(channels))]}
		}
	}
	close(jobs)
	workers.Wait()
}
