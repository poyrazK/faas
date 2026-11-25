package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ManagedValkeyCache marks an app as a managed Redis-compatible cache
// (ADR-20261116192126) and holds the cache's settings. The app itself
// carries the name (slug), idle timeout and runtime state.
type ManagedValkeyCache struct {
	AppID          string
	AccountID      string
	MemoryMB       int
	EvictionPolicy string
	CreatedAt      time.Time
}

// ManagedValkeyStore is the optional store surface for managed caches.
// PgStore and MemStore implement it; callers type-assert.
type ManagedValkeyStore interface {
	CreateManagedValkeyCache(ctx context.Context, cache ManagedValkeyCache) (ManagedValkeyCache, error)
	// GetManagedValkeyCache returns ErrNotFound when appID is not a cache.
	GetManagedValkeyCache(ctx context.Context, appID string) (ManagedValkeyCache, error)
	// ListManagedValkeyCaches lists an account's caches whose app is not
	// deleted, oldest first.
	ListManagedValkeyCaches(ctx context.Context, accountID string) ([]ManagedValkeyCache, error)
	DeleteManagedValkeyCache(ctx context.Context, appID string) error
	// UpdateManagedValkeyCache records a resize or policy change.
	UpdateManagedValkeyCache(ctx context.Context, appID string, memoryMB int, evictionPolicy string) (ManagedValkeyCache, error)
}

var _ ManagedValkeyStore = (*PgStore)(nil)
var _ ManagedValkeyStore = (*MemStore)(nil)

// ManagedValkeyGrantStore records which apps may call a cache. Binding a
// cache to an app grants the call (ADR-20261124174500): a project workload
// reaches a cache outside its project only through a grant.
type ManagedValkeyGrantStore interface {
	// GrantManagedValkeyCache is idempotent.
	GrantManagedValkeyCache(ctx context.Context, cacheAppID, appID, accountID string) error
	// RevokeManagedValkeyCacheGrant reports whether a grant existed.
	RevokeManagedValkeyCacheGrant(ctx context.Context, cacheAppID, appID string) (bool, error)
	ManagedValkeyCacheGrantReader
}

// ManagedValkeyCacheGrantReader is the read half the service authorizer
// needs; the pinned service policy snapshot implements it too.
type ManagedValkeyCacheGrantReader interface {
	// ManagedValkeyCacheGranted reports whether appID holds a grant to
	// cacheAppID while cacheAppID is still a managed cache.
	ManagedValkeyCacheGranted(ctx context.Context, cacheAppID, appID string) (bool, error)
}

var _ ManagedValkeyGrantStore = (*PgStore)(nil)
var _ ManagedValkeyGrantStore = (*MemStore)(nil)
var _ ManagedValkeyCacheGrantReader = servicePolicyReader{}

// GrantManagedValkeyCache records that appID may call the cache.
func (s *PgStore) GrantManagedValkeyCache(ctx context.Context, cacheAppID, appID, accountID string) error {
	return mapErr(sqlc.New().GrantManagedValkeyCache(ctx, s.pool, sqlc.GrantManagedValkeyCacheParams{
		CacheAppID: uuidToPgtype(cacheAppID), AppID: uuidToPgtype(appID), AccountID: uuidToPgtype(accountID)}))
}

// RevokeManagedValkeyCacheGrant removes appID's grant to the cache.
func (s *PgStore) RevokeManagedValkeyCacheGrant(ctx context.Context, cacheAppID, appID string) (bool, error) {
	n, err := sqlc.New().RevokeManagedValkeyCacheGrant(ctx, s.pool, sqlc.RevokeManagedValkeyCacheGrantParams{
		CacheAppID: uuidToPgtype(cacheAppID), AppID: uuidToPgtype(appID)})
	if err != nil {
		return false, mapErr(err)
	}
	return n > 0, nil
}

// ManagedValkeyCacheGranted reads a grant outside a policy snapshot.
func (s *PgStore) ManagedValkeyCacheGranted(ctx context.Context, cacheAppID, appID string) (bool, error) {
	granted, err := sqlc.New().ReadServicePolicyCacheGrant(ctx, s.pool, sqlc.ReadServicePolicyCacheGrantParams{
		CacheAppID: uuidToPgtype(cacheAppID), AppID: uuidToPgtype(appID)})
	if err != nil {
		return false, mapErr(err)
	}
	return granted, nil
}

// ManagedValkeyCacheGranted reads a grant inside the pinned policy view.
func (s servicePolicyReader) ManagedValkeyCacheGranted(ctx context.Context, cacheAppID, appID string) (bool, error) {
	granted, err := sqlc.New().ReadServicePolicyCacheGrant(ctx, s.tx, sqlc.ReadServicePolicyCacheGrantParams{
		CacheAppID: uuidToPgtype(cacheAppID), AppID: uuidToPgtype(appID)})
	if err != nil {
		return false, fmt.Errorf("read managed cache grant: %w", err)
	}
	return granted, nil
}

func managedValkeyGrantKey(cacheAppID, appID string) string { return cacheAppID + "\x00" + appID }

// GrantManagedValkeyCache records that appID may call the cache.
func (m *MemStore) GrantManagedValkeyCache(ctx context.Context, cacheAppID, appID, accountID string) error {
	if cacheAppID == appID {
		return fmt.Errorf("grant cache %s to itself: %w", cacheAppID, ErrConflict)
	}
	m.managedValkeyCacheGrants.LoadOrStore(managedValkeyGrantKey(cacheAppID, appID), accountID)
	return nil
}

// RevokeManagedValkeyCacheGrant removes appID's grant to the cache.
func (m *MemStore) RevokeManagedValkeyCacheGrant(ctx context.Context, cacheAppID, appID string) (bool, error) {
	_, existed := m.managedValkeyCacheGrants.LoadAndDelete(managedValkeyGrantKey(cacheAppID, appID))
	return existed, nil
}

// ManagedValkeyCacheGranted reports a grant to a cache that still exists.
func (m *MemStore) ManagedValkeyCacheGranted(ctx context.Context, cacheAppID, appID string) (bool, error) {
	if _, ok := m.managedValkeyCaches.Load(cacheAppID); !ok {
		return false, nil
	}
	_, ok := m.managedValkeyCacheGrants.Load(managedValkeyGrantKey(cacheAppID, appID))
	return ok, nil
}

// CreateManagedValkeyCache inserts the marker row for an existing app.
func (s *PgStore) CreateManagedValkeyCache(ctx context.Context, cache ManagedValkeyCache) (ManagedValkeyCache, error) {
	err := s.pool.QueryRow(ctx, `
		insert into managed_valkey_caches (app_id, account_id, memory_mb, eviction_policy)
		values ($1, $2, $3, $4)
		returning created_at`,
		cache.AppID, cache.AccountID, cache.MemoryMB, cache.EvictionPolicy).Scan(&cache.CreatedAt)
	if err != nil {
		return ManagedValkeyCache{}, mapErr(err)
	}
	return cache, nil
}

// GetManagedValkeyCache reads one cache by its app id.
func (s *PgStore) GetManagedValkeyCache(ctx context.Context, appID string) (ManagedValkeyCache, error) {
	var cache ManagedValkeyCache
	err := s.pool.QueryRow(ctx, `
		select app_id::text, account_id::text, memory_mb, eviction_policy, created_at
		from managed_valkey_caches where app_id = $1`, appID).
		Scan(&cache.AppID, &cache.AccountID, &cache.MemoryMB, &cache.EvictionPolicy, &cache.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedValkeyCache{}, ErrNotFound
	}
	if err != nil {
		return ManagedValkeyCache{}, mapErr(err)
	}
	return cache, nil
}

// ListManagedValkeyCaches lists an account's live caches.
func (s *PgStore) ListManagedValkeyCaches(ctx context.Context, accountID string) ([]ManagedValkeyCache, error) {
	rows, err := s.pool.Query(ctx, `
		select c.app_id::text, c.account_id::text, c.memory_mb, c.eviction_policy, c.created_at
		from managed_valkey_caches c
		join apps a on a.id = c.app_id
		where c.account_id = $1 and a.status <> 'deleted'
		order by c.created_at, c.app_id`, accountID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []ManagedValkeyCache
	for rows.Next() {
		var cache ManagedValkeyCache
		if err := rows.Scan(&cache.AppID, &cache.AccountID, &cache.MemoryMB, &cache.EvictionPolicy, &cache.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, cache)
	}
	return out, mapErr(rows.Err())
}

// DeleteManagedValkeyCache removes the marker row; the app is deleted by
// the ordinary app lifecycle.
func (s *PgStore) DeleteManagedValkeyCache(ctx context.Context, appID string) error {
	_, err := s.pool.Exec(ctx, `delete from managed_valkey_caches where app_id = $1`, appID)
	return mapErr(err)
}

// UpdateManagedValkeyCache records a cache's new size and policy.
func (s *PgStore) UpdateManagedValkeyCache(ctx context.Context, appID string, memoryMB int, evictionPolicy string) (ManagedValkeyCache, error) {
	var cache ManagedValkeyCache
	err := s.pool.QueryRow(ctx, `
		update managed_valkey_caches set memory_mb = $2, eviction_policy = $3
		where app_id = $1
		returning app_id::text, account_id::text, memory_mb, eviction_policy, created_at`,
		appID, memoryMB, evictionPolicy).
		Scan(&cache.AppID, &cache.AccountID, &cache.MemoryMB, &cache.EvictionPolicy, &cache.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedValkeyCache{}, ErrNotFound
	}
	if err != nil {
		return ManagedValkeyCache{}, mapErr(err)
	}
	return cache, nil
}

// UpdateManagedValkeyCache implements ManagedValkeyStore in memory.
func (m *MemStore) UpdateManagedValkeyCache(ctx context.Context, appID string, memoryMB int, evictionPolicy string) (ManagedValkeyCache, error) {
	value, ok := m.managedValkeyCaches.Load(appID)
	if !ok {
		return ManagedValkeyCache{}, ErrNotFound
	}
	cache := value.(ManagedValkeyCache)
	cache.MemoryMB, cache.EvictionPolicy = memoryMB, evictionPolicy
	m.managedValkeyCaches.Store(appID, cache)
	return cache, nil
}

// CreateManagedValkeyCache implements ManagedValkeyStore in memory.
func (m *MemStore) CreateManagedValkeyCache(ctx context.Context, cache ManagedValkeyCache) (ManagedValkeyCache, error) {
	m.mu.Lock()
	_, appExists := m.apps[cache.AppID]
	m.mu.Unlock()
	if !appExists {
		return ManagedValkeyCache{}, ErrNotFound
	}
	if cache.CreatedAt.IsZero() {
		cache.CreatedAt = time.Now().UTC()
	}
	if _, loaded := m.managedValkeyCaches.LoadOrStore(cache.AppID, cache); loaded {
		return ManagedValkeyCache{}, ErrConflict
	}
	return cache, nil
}

// GetManagedValkeyCache implements ManagedValkeyStore in memory.
func (m *MemStore) GetManagedValkeyCache(ctx context.Context, appID string) (ManagedValkeyCache, error) {
	value, ok := m.managedValkeyCaches.Load(appID)
	if !ok {
		return ManagedValkeyCache{}, ErrNotFound
	}
	return value.(ManagedValkeyCache), nil
}

// ListManagedValkeyCaches implements ManagedValkeyStore in memory.
func (m *MemStore) ListManagedValkeyCaches(ctx context.Context, accountID string) ([]ManagedValkeyCache, error) {
	var out []ManagedValkeyCache
	m.managedValkeyCaches.Range(func(_, value any) bool {
		cache := value.(ManagedValkeyCache)
		if cache.AccountID == accountID {
			out = append(out, cache)
		}
		return true
	})
	m.mu.Lock()
	out = slices.DeleteFunc(out, func(cache ManagedValkeyCache) bool {
		app, ok := m.apps[cache.AppID]
		return !ok || app.Status == AppDeleted
	})
	m.mu.Unlock()
	slices.SortFunc(out, func(a, b ManagedValkeyCache) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.AppID, b.AppID)
	})
	return out, nil
}

// DeleteManagedValkeyCache implements ManagedValkeyStore in memory.
func (m *MemStore) DeleteManagedValkeyCache(ctx context.Context, appID string) error {
	m.managedValkeyCaches.Delete(appID)
	return nil
}
