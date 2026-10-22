package socket

import (
	"container/list"
	"context"
	"sync"

	"github.com/onebox-faas/faas/pkg/durableentity"
)

// RegistryCache caches authenticated immutable shards for one private store.
// It never caches quota roots, provider errors, or unverified shard bodies.
// Attach it to Quotas using the same immutable Store; nil disables caching.
type RegistryCache struct {
	store                  durableentity.ObjectStore
	mu                     sync.Mutex
	maxBytes               int64
	maxEntries, maxFlights int
	bytes                  int64
	entries                map[string]*list.Element
	lru                    list.List
	flights                map[string]*registryFlight
	changed                chan struct{}
}
type registryCachedShard struct {
	key   string
	shard registryShard
	bytes int64
}
type registryFlight struct {
	done    chan struct{}
	shard   registryShard
	err     error
	waiters int
	retry   bool
}

// NewRegistryCache bounds retained cache accounting, entries and concurrent
// provider fetches. Accounting includes a conservative decoded-data allowance;
// per-call copies and bounded in-flight buffers are outside retained accounting.
func NewRegistryCache(store durableentity.ObjectStore, maxBytes int64, maxEntries, maxFlights int) (*RegistryCache, error) {
	if store == nil || maxBytes <= 0 || maxBytes > 64<<20 || maxEntries <= 0 || maxEntries > 4096 || maxFlights <= 0 || maxFlights > 64 {
		return nil, durableentity.ErrInvalid
	}
	return &RegistryCache{store: store, maxBytes: maxBytes, maxEntries: maxEntries, maxFlights: maxFlights, entries: map[string]*list.Element{}, flights: map[string]*registryFlight{}, changed: make(chan struct{})}, nil
}
func cloneRegistryShard(s registryShard) registryShard {
	s.Entities = append([]entityReservation(nil), s.Entities...)
	s.Retirements = append([]QuotaRetirement(nil), s.Retirements...)
	return s
}
func (c *RegistryCache) read(ctx context.Context, scope string, index int, hash string, epochs ...uint64) (registryShard, error) {
	key := registryPath(scope, index, hash, epochs...)
	for {
		if err := ctx.Err(); err != nil {
			return registryShard{}, err
		}
		c.mu.Lock()
		if element := c.entries[key]; element != nil {
			c.lru.MoveToFront(element)
			shard := cloneRegistryShard(element.Value.(registryCachedShard).shard)
			c.mu.Unlock()
			return shard, nil
		}
		if flight := c.flights[key]; flight != nil {
			flight.waiters++
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				c.mu.Lock()
				flight.waiters--
				c.mu.Unlock()
				return registryShard{}, ctx.Err()
			case <-flight.done:
				c.mu.Lock()
				flight.waiters--
				c.mu.Unlock()
				if err := ctx.Err(); err != nil {
					return registryShard{}, err
				}
				// A canceled leader cannot poison a caller whose own context is live.
				if flight.retry {
					continue
				}
				if flight.err != nil {
					return registryShard{}, flight.err
				}
				return cloneRegistryShard(flight.shard), nil
			}
		}
		if len(c.flights) >= c.maxFlights {
			changed := c.changed
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return registryShard{}, ctx.Err()
			case <-changed:
				continue
			}
		}
		flight := &registryFlight{done: make(chan struct{})}
		c.flights[key] = flight
		c.mu.Unlock()
		shard, weight, err := readRegistryShard(ctx, c.store, scope, index, hash, epochs...)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		c.mu.Lock()
		flight.shard, flight.err = shard, err
		flight.retry = ctx.Err() != nil
		if err == nil && weight <= c.maxBytes {
			for c.lru.Len() >= c.maxEntries || c.bytes+weight > c.maxBytes {
				oldest := c.lru.Back()
				entry := oldest.Value.(registryCachedShard)
				delete(c.entries, entry.key)
				c.bytes -= entry.bytes
				c.lru.Remove(oldest)
			}
			c.entries[key] = c.lru.PushFront(registryCachedShard{key: key, shard: shard, bytes: weight})
			c.bytes += weight
		}
		delete(c.flights, key)
		close(flight.done)
		close(c.changed)
		c.changed = make(chan struct{})
		c.mu.Unlock()
		if err != nil {
			return registryShard{}, err
		}
		return cloneRegistryShard(shard), nil
	}
}
