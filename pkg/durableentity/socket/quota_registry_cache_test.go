package socket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

type registryCountingStore struct {
	durableentity.ObjectStore
	mu      sync.Mutex
	reads   map[string]int
	rootErr error
	hook    func(context.Context, string) error
}

func (s *registryCountingStore) Get(ctx context.Context, key string, limit int64) ([]byte, string, error) {
	s.mu.Lock()
	s.reads[key]++
	err := s.rootErr
	hook := s.hook
	s.mu.Unlock()
	if err != nil && strings.HasPrefix(key, "tenant-quotas/v1/") {
		return nil, "", err
	}
	if hook != nil {
		if err := hook(ctx, key); err != nil {
			return nil, "", err
		}
	}
	return s.ObjectStore.Get(ctx, key, limit)
}
func (s *registryCountingStore) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for key, count := range s.reads {
		if strings.HasPrefix(key, prefix) {
			n += count
		}
	}
	return n
}
func registryCacheFixture(t *testing.T) (*Quotas, durableentity.ID, *quotaBucket, *registryCountingStore, quotaRecord) {
	t.Helper()
	q, id, b := enableRegistryFixture(t)
	scope, _ := scopeFor(id)
	r, _, err := q.read(t.Context(), scopeKey(scope))
	if err != nil {
		t.Fatal(err)
	}
	store := &registryCountingStore{ObjectStore: b, reads: map[string]int{}}
	cache, err := NewRegistryCache(store, 8<<20, 256, 16)
	if err != nil {
		t.Fatal(err)
	}
	q.Store = store
	q.RegistryCache = cache
	return q, id, b, store, r
}
func awaitRegistryFlight(t *testing.T, c *RegistryCache, key string, waiters int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		c.mu.Lock()
		flight := c.flights[key]
		ready := flight != nil && flight.waiters >= waiters
		c.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("flight did not reach waiting callers")
		}
	}
}
func cacheInvariant(t *testing.T, c *RegistryCache) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bytes > c.maxBytes || len(c.entries) > c.maxEntries || len(c.flights) > c.maxFlights || len(c.entries) != c.lru.Len() {
		t.Fatal("cache exceeded configured bounds", c.bytes, len(c.entries), len(c.flights))
	}
}
func TestRegistryCacheKeepsRootFreshAndCallerMutationsPrivate(t *testing.T) {
	q, id, _, store, initial := registryCacheFixture(t)
	key := scopeKey(initial.Scope)
	r, _, err := q.read(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if store.count("tenant-registry/") != 16 {
		t.Fatal("cold hydration", store.count("tenant-registry/"))
	}
	r.Entities[0].Bytes = 1
	if r, _, err = q.read(t.Context(), key); err != nil || r.Entities[0].Bytes != 1024 {
		t.Fatal("cached reservation mutated", r, err)
	}
	if store.count("tenant-registry/") != 16 || store.count("tenant-quotas/") != 2 {
		t.Fatal("cache hid quota root or refetched shards", store.reads)
	}
	fresh := id
	fresh.Key = "cached-new-document"
	if err = q.AdmitInvocation(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
	if _, _, err = q.read(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if store.count("tenant-registry/") != 17 {
		t.Fatal("changed hash did not fetch exactly one shard", store.count("tenant-registry/"))
	}
	store.mu.Lock()
	store.rootErr = errors.New("quota root unavailable")
	store.mu.Unlock()
	if err = q.AdmitInvocation(t.Context(), id); err == nil {
		t.Fatal("warm cache bypassed failed root read")
	}
	cacheInvariant(t, q.RegistryCache)
}
func TestRegistryCacheWarmAdmissionRechecksConcurrentRetirement(t *testing.T) {
	q, id, b, _, r := registryCacheFixture(t)
	key := scopeKey(r.Scope)
	if _, _, err := q.read(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	other := *q
	other.Store = b
	other.RegistryCache = nil
	reader := retirementReader{status: &durableentity.RetirementStatus{Request: durableentity.RetirementRequest{OperationID: uuid.NewString()}, Actor: "operator", At: q.now(), Revision: 1, Phase: "complete", RetainedBytes: 128}}
	q.Store = &registryInterleavingStore{ObjectStore: b, beforeRoot: func() {
		if _, err := other.TrackRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
			t.Fatal(err)
		}
		if _, err := other.ReclaimRetirement(t.Context(), id, reader.status.Request.OperationID, reader); err != nil {
			t.Fatal(err)
		}
	}}
	if err := q.AdmitInvocation(t.Context(), id); !errors.Is(err, durableentity.ErrRetired) {
		t.Fatal("warm stale admission overwrote retirement", err)
	}
	state, _, err := q.read(t.Context(), key)
	if err != nil || len(state.Entities) != 0 || len(state.Retirements) != 1 {
		t.Fatal(state, err)
	}
	state.Retirements[0].Phase = "held"
	state.Retirements[0].RetainedBytes = 0
	usage, err := q.Inspect(t.Context(), id)
	if err != nil || usage.RecoveryBytes != 128 || usage.ReservedBytes != 128 {
		t.Fatal("caller changed cached retirement", usage, err)
	}
}
func TestRegistryCacheEvictsAndDoesNotRetainOversizeShards(t *testing.T) {
	for _, test := range []struct {
		name    string
		bytes   int64
		entries int
		retains bool
	}{{"entries", 8 << 20, 1, true}, {"bytes", 2000, 16, true}, {"oversize", 1, 16, false}} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, store, r := registryCacheFixture(t)
			c, err := NewRegistryCache(store, test.bytes, test.entries, 2)
			if err != nil {
				t.Fatal(err)
			}
			scope := scopeKey(r.Scope)
			for _, index := range []int{0, 1, 0} {
				if _, err = c.read(t.Context(), scope, index, r.Registry.Shards[index]); err != nil {
					t.Fatal(err)
				}
				cacheInvariant(t, c)
			}
			if store.count("tenant-registry/") != 3 {
				t.Fatal("evicted/oversize shard was not fetched again", store.count("tenant-registry/"))
			}
			c.mu.Lock()
			entries := len(c.entries)
			c.mu.Unlock()
			if test.retains && entries != 1 || !test.retains && entries != 0 {
				t.Fatal("unexpected retained entries", entries)
			}
		})
	}
}
func TestRegistryCacheCoalescesReadsAndCanceledWaiterDoesNotCancelLeader(t *testing.T) {
	q, _, _, store, r := registryCacheFixture(t)
	c := q.RegistryCache
	scope := scopeKey(r.Scope)
	hash := r.Registry.Shards[0]
	path := registryPath(scope, 0, hash)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.hook = func(ctx context.Context, key string) error {
		if key == path {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	leader := make(chan error, 1)
	go func() { _, err := c.read(t.Context(), scope, 0, hash); leader <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("leader did not fetch")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiter := make(chan error, 1)
	go func() { _, err := c.read(ctx, scope, 0, hash); waiter <- err }()
	awaitRegistryFlight(t, c, path, 1)
	cancel()
	if err := <-waiter; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, err := c.read(t.Context(), scope, 0, hash); results <- err }()
	}
	awaitRegistryFlight(t, c, path, 8)
	close(release)
	if err := <-leader; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if store.count(path) != 1 {
		t.Fatal("coalescing failed", store.count(path))
	}
	cacheInvariant(t, c)
}
func TestRegistryCacheCanceledLeaderDoesNotPoisonLiveWaiter(t *testing.T) {
	q, _, _, store, r := registryCacheFixture(t)
	c := q.RegistryCache
	scope := scopeKey(r.Scope)
	hash := r.Registry.Shards[0]
	path := registryPath(scope, 0, hash)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan struct{})
	var once sync.Once
	store.hook = func(call context.Context, key string) error {
		if key == path && call == ctx {
			once.Do(func() { close(entered) })
			<-call.Done()
			return call.Err()
		}
		return nil
	}
	leader := make(chan error, 1)
	go func() { _, err := c.read(ctx, scope, 0, hash); leader <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("leader did not fetch")
	}
	waiter := make(chan error, 1)
	go func() { _, err := c.read(t.Context(), scope, 0, hash); waiter <- err }()
	awaitRegistryFlight(t, c, path, 1)
	cancel()
	if err := <-leader; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case err := <-waiter:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("live waiter did not retry")
	}
	if store.count(path) != 2 {
		t.Fatal("leader cancellation was cached", store.count(path))
	}
	cacheInvariant(t, c)
}
func TestRegistryCacheBoundsFetchesAndCapacityWaitCanCancel(t *testing.T) {
	_, _, _, store, r := registryCacheFixture(t)
	c, err := NewRegistryCache(store, 8<<20, 256, 1)
	if err != nil {
		t.Fatal(err)
	}
	scope := scopeKey(r.Scope)
	path := registryPath(scope, 0, r.Registry.Shards[0])
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.hook = func(ctx context.Context, key string) error {
		if key == path {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	first := make(chan error, 1)
	go func() { _, err := c.read(t.Context(), scope, 0, r.Registry.Shards[0]); first <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first fetch missing")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = c.read(ctx, scope, 1, r.Registry.Shards[1]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waiting, stop := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer stop()
	if _, err = c.read(waiting, scope, 1, r.Registry.Shards[1]); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if store.count("tenant-registry/") != 1 {
		t.Fatal("fetch cap bypassed", store.count("tenant-registry/"))
	}
	cacheInvariant(t, c)
	live := make(chan error, 1)
	go func() { _, err := c.read(t.Context(), scope, 1, r.Registry.Shards[1]); live <- err }()
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-live:
		if err != nil {
			t.Fatal("released capacity not usable", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capacity waiter did not wake")
	}
}
func TestRegistryCacheRejectsCorruptionWithoutNegativeCaching(t *testing.T) {
	q, _, b, store, r := registryCacheFixture(t)
	scope := scopeKey(r.Scope)
	hash := r.Registry.Shards[0]
	path := registryPath(scope, 0, hash)
	b.mu.Lock()
	original := b.objects[path]
	broken := original
	broken.body = []byte("{}")
	b.objects[path] = broken
	b.mu.Unlock()
	if _, err := q.RegistryCache.read(t.Context(), scope, 0, hash); !errors.Is(err, durableentity.ErrCorrupt) {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.objects[path] = original
	b.mu.Unlock()
	if _, err := q.RegistryCache.read(t.Context(), scope, 0, hash); err != nil {
		t.Fatal("corruption cached", err)
	}
	if store.count(path) != 2 {
		t.Fatal(store.count(path))
	}
	// A self-consistent hash does not authenticate an invalid reservation.
	invalid := registryShard{Scope: scope, Index: 0, Entities: []entityReservation{{Key: fmt.Sprintf("%064x", 1), Bytes: -1}}}
	body, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	invalidHash := registryDigest(body)
	invalidPath := registryPath(scope, 0, invalidHash)
	if _, err = b.Put(t.Context(), invalidPath, body, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = q.RegistryCache.read(t.Context(), scope, 0, invalidHash); !errors.Is(err, durableentity.ErrCorrupt) {
		t.Fatal("semantically invalid shard cached", err)
	}
	q.RegistryCache.mu.Lock()
	entry := q.RegistryCache.entries[invalidPath]
	q.RegistryCache.mu.Unlock()
	if entry != nil {
		t.Fatal("invalid shard retained")
	}
	if _, err := q.RegistryCache.read(t.Context(), strings.Repeat("a", 64), 0, hash); !errors.Is(err, durableentity.ErrCorrupt) {
		t.Fatal("cached shard crossed scope", err)
	}
	cacheInvariant(t, q.RegistryCache)
}
