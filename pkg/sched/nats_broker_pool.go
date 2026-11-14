// nats_broker_pool.go — the NATS connections kind=nats triggers share
// (ADR-20261101104512).
//
// One connection per server URL (credentials included): triggers naming the
// same URL share it. The pool used to dial while holding its one lock, so a
// server that did not answer made every NATS poller build on the node wait
// for it; it never closed a connection, so a deleted trigger's server, or a
// URL whose credentials had been rotated, kept a connection reconnecting for
// the life of schedd; and it kept a connection nats.go had closed for good
// (after repeated authorization errors), so its triggers failed until
// schedd restarted.

package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// natsBrokerIdleTimeout is how long a connection no trigger uses stays
// open, so a trigger rebuilt for a config change reuses it.
const natsBrokerIdleTimeout = 2 * time.Minute

type natsBrokerPool struct {
	mu       sync.Mutex
	dial     func(rawURL string) (*natsBroker, error)
	isClosed func(*natsBroker) bool
	idle     time.Duration
	brokers  map[string]*natsPooledBroker
	dials    singleflight.Group
}

// natsPooledBroker is one connection and the pollers holding it.
type natsPooledBroker struct {
	broker *natsBroker
	refs   int
	timer  *time.Timer
}

var natsBrokers = newNATSBrokerPool(NewNATSBroker, natsBrokerIdleTimeout)

func newNATSBrokerPool(dial func(rawURL string) (*natsBroker, error), idle time.Duration) *natsBrokerPool {
	return &natsBrokerPool{dial: dial, isClosed: natsBrokerClosed, idle: idle, brokers: map[string]*natsPooledBroker{}}
}

// natsBrokerClosed reports whether nats.go has closed the connection for
// good; it no longer reconnects.
func natsBrokerClosed(b *natsBroker) bool {
	return b != nil && b.conn != nil && b.conn.IsClosed()
}

// natsBrokerKey keys the pool without holding a URL's credentials as a key.
func natsBrokerKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

// acquire returns rawURL's connection, dialing it if the pool has none open,
// and a release the caller runs once it stops using the connection. The
// dial runs outside the pool's lock, once per URL however many triggers
// wait for it.
func (p *natsBrokerPool) acquire(rawURL string) (*natsBroker, func(), error) {
	key := natsBrokerKey(rawURL)
	for range 2 {
		if broker, release, ok := p.take(key); ok {
			return broker, release, nil
		}
		if _, err, _ := p.dials.Do(key, func() (any, error) {
			if p.open(key) {
				// Another caller's dial finished first.
				return nil, nil
			}
			broker, err := p.dial(rawURL)
			if err != nil {
				return nil, err
			}
			p.mu.Lock()
			entry := &natsPooledBroker{broker: broker}
			p.brokers[key] = entry
			p.idleLocked(key, entry)
			p.mu.Unlock()
			return nil, nil
		}); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, fmt.Errorf("nats_broker: connection closed as it opened")
}

// take references an open connection for key. A connection nats.go has
// closed leaves the pool, so the next acquire dials a new one; the pollers
// still holding it are rebuilt by the trigger tick (natsPoller.Closed).
func (p *natsBrokerPool) take(key string) (*natsBroker, func(), bool) {
	p.mu.Lock()
	entry, ok := p.brokers[key]
	if ok && p.isClosed(entry.broker) {
		delete(p.brokers, key)
		if entry.timer != nil {
			entry.timer.Stop()
		}
		unused := entry.refs == 0
		p.mu.Unlock()
		if unused {
			_ = entry.broker.Close()
		}
		return nil, nil, false
	}
	if !ok {
		p.mu.Unlock()
		return nil, nil, false
	}
	entry.refs++
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	p.mu.Unlock()
	var once sync.Once
	return entry.broker, func() { once.Do(func() { p.release(key, entry) }) }, true
}

// open reports whether the pool holds a live connection for key.
func (p *natsBrokerPool) open(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, ok := p.brokers[key]
	return ok && !p.isClosed(entry.broker)
}

func (p *natsBrokerPool) release(key string, entry *natsPooledBroker) {
	p.mu.Lock()
	entry.refs--
	if entry.refs > 0 {
		p.mu.Unlock()
		return
	}
	if p.brokers[key] == entry {
		p.idleLocked(key, entry)
		p.mu.Unlock()
		return
	}
	// A connection the pool already replaced closes with its last user.
	p.mu.Unlock()
	_ = entry.broker.Close()
}

// pooledNATSPoller is a NATS trigger's poller holding its pooled connection,
// which it returns to the pool when the poller closes.
type pooledNATSPoller struct {
	triggerSource
	broker  *natsBroker
	release func()
}

// Close closes the poller and returns its connection to the pool.
func (p *pooledNATSPoller) Close() error {
	err := p.triggerSource.Close()
	p.release()
	return err
}

// Closed reports that nats.go closed the poller's connection for good: the
// trigger tick builds the trigger a new poller on a new connection.
func (p *pooledNATSPoller) Closed() bool {
	return natsBrokerClosed(p.broker) || triggerSourceClosed(p.triggerSource)
}

func (p *pooledNATSPoller) Defer(ctx context.Context, t sqlc.Trigger, ids []string) error {
	if source, ok := p.triggerSource.(triggerDeferrer); ok {
		return source.Defer(ctx, t, ids)
	}
	return p.Nack(ctx, t, ids, triggerReasonBrokerError)
}

func (p *pooledNATSPoller) NackUntil(ctx context.Context, t sqlc.Trigger, ids []string, until []time.Time) error {
	if source, ok := p.triggerSource.(triggerRetryScheduler); ok {
		return source.NackUntil(ctx, t, ids, until)
	}
	return p.Nack(ctx, t, ids, triggerReasonBrokerError)
}

// BrokerStats forwards the consumer's pending counts.
func (p *pooledNATSPoller) BrokerStats(ctx context.Context, t sqlc.Trigger) BrokerStats {
	if stats, ok := p.triggerSource.(triggerStatsSource); ok {
		return stats.BrokerStats(ctx, t)
	}
	return BrokerStats{}
}

// idleLocked closes entry once it has gone unused for the idle timeout.
// Caller holds p.mu.
func (p *natsBrokerPool) idleLocked(key string, entry *natsPooledBroker) {
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.timer = time.AfterFunc(p.idle, func() {
		p.mu.Lock()
		if p.brokers[key] != entry || entry.refs > 0 {
			p.mu.Unlock()
			return
		}
		delete(p.brokers, key)
		p.mu.Unlock()
		_ = entry.broker.Close()
	})
}
