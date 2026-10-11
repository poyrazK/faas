package socket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/durableentity"
)

const registryShards = 16
const registryShardBytes = 256 << 10

// Registry roots are published in the quota CAS. Unpublished immutable shards
// confer no authority; old schema readers reject the root before admission.
type quotaRegistry struct {
	OperationID    string    `json:"operation_id"`
	SourceRevision string    `json:"source_revision"`
	Actor          string    `json:"actor"`
	At             time.Time `json:"at"`
	Shards         []string  `json:"shards"`
}
type registryShard struct {
	Scope       string              `json:"scope"`
	Index       int                 `json:"index"`
	Entities    []entityReservation `json:"entities"`
	Retirements []QuotaRetirement   `json:"retirements"`
}

func entityCapacity(r quotaRecord) int {
	if r.Schema == 4 {
		return 1024
	}
	return 64
}
func retirementCapacity(r quotaRecord) int {
	if r.Schema == 4 {
		return 4096
	}
	return 256
}
func registryIndex(key string) int { return int(strings.IndexByte("0123456789abcdef", key[0])) }
func registryDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func registryPath(scope string, index int, hash string) string {
	return "tenant-registry/v1/" + scope + "/" + string("0123456789abcdef"[index]) + "/" + hash
}

func (q *Quotas) loadRegistry(ctx context.Context, key string, r *quotaRecord) error {
	if r.Schema != 4 {
		if r.Registry != nil {
			return durableentity.ErrCorrupt
		}
		return nil
	}
	root := r.Registry
	if root == nil || !sessionUUID(root.OperationID) || !validScopeKey(root.SourceRevision) || !grantIdentity(root.Actor) || root.At.IsZero() || len(root.Shards) != registryShards || len(r.Entities) != 0 || len(r.Retirements) != 0 {
		return durableentity.ErrCorrupt
	}
	for index, hash := range root.Shards {
		if !validScopeKey(hash) {
			return durableentity.ErrCorrupt
		}
		body, version, err := q.Store.Get(ctx, registryPath(key, index, hash), registryShardBytes)
		if errors.Is(err, durableentity.ErrNotFound) {
			return durableentity.ErrCorrupt
		}
		if err != nil {
			return err
		}
		var shard registryShard
		if version == "" || registryDigest(body) != hash || json.Unmarshal(body, &shard) != nil || shard.Scope != key || shard.Index != index || len(shard.Entities) > 64 || len(shard.Retirements) > 256 {
			return durableentity.ErrCorrupt
		}
		for _, e := range shard.Entities {
			if !validScopeKey(e.Key) || registryIndex(e.Key) != index {
				return durableentity.ErrCorrupt
			}
		}
		for _, e := range shard.Retirements {
			if !validScopeKey(e.EntityKey) || registryIndex(e.EntityKey) != index {
				return durableentity.ErrCorrupt
			}
		}
		r.Entities = append(r.Entities, shard.Entities...)
		r.Retirements = append(r.Retirements, shard.Retirements...)
	}
	return nil
}

// All mutations, including policy changes, must use this serializer. The single
// subsequent root CAS publishes fences, recovery charges and credits atomically.
func (q *Quotas) marshalQuota(ctx context.Context, key string, r quotaRecord) ([]byte, error) {
	if r.Schema == 4 {
		if r.Registry == nil || !validQuotaRetirements(r) {
			return nil, durableentity.ErrCorrupt
		}
		shards := make([]registryShard, registryShards)
		for i := range shards {
			shards[i].Scope = key
			shards[i].Index = i
		}
		for _, e := range r.Entities {
			if !validScopeKey(e.Key) {
				return nil, durableentity.ErrCorrupt
			}
			i := registryIndex(e.Key)
			shards[i].Entities = append(shards[i].Entities, e)
		}
		for _, e := range r.Retirements {
			if !validScopeKey(e.EntityKey) {
				return nil, durableentity.ErrCorrupt
			}
			i := registryIndex(e.EntityKey)
			shards[i].Retirements = append(shards[i].Retirements, e)
		}
		root := *r.Registry
		root.Shards = make([]string, registryShards)
		for i, s := range shards {
			if len(s.Entities) > 64 || len(s.Retirements) > 256 {
				return nil, durableentity.ErrLimit
			}
			sort.Slice(s.Entities, func(a, b int) bool { return s.Entities[a].Key < s.Entities[b].Key })
			sort.Slice(s.Retirements, func(a, b int) bool { return s.Retirements[a].EntityKey < s.Retirements[b].EntityKey })
			body, err := json.Marshal(s)
			if err != nil {
				return nil, err
			}
			if len(body) > registryShardBytes {
				return nil, durableentity.ErrLimit
			}
			hash := registryDigest(body)
			root.Shards[i] = hash
			if len(r.Registry.Shards) == registryShards && r.Registry.Shards[i] == hash {
				continue
			}
			path := registryPath(key, i, hash)
			version, err := q.Store.Put(ctx, path, body, "")
			if errors.Is(err, durableentity.ErrConflict) {
				existing, v, readErr := q.Store.Get(ctx, path, registryShardBytes)
				if readErr != nil {
					return nil, readErr
				}
				if v == "" || registryDigest(existing) != hash {
					return nil, durableentity.ErrCorrupt
				}
				continue
			}
			if err != nil || version == "" {
				return nil, errors.Join(durableentity.ErrUncertain, err)
			}
		}
		r.Registry = &root
		r.Entities = nil
		r.Retirements = nil
	}
	body, err := json.Marshal(r)
	if err == nil && len(body) > maxQuotaRecordBytes {
		err = durableentity.ErrLimit
	}
	return body, err
}

type RegistryStatus struct {
	Enabled         bool      `json:"enabled"`
	Revision        string    `json:"revision"`
	OperationID     string    `json:"operation_id,omitempty"`
	Actor           string    `json:"actor,omitempty"`
	At              time.Time `json:"at,omitempty"`
	Active          int       `json:"active"`
	Retired         int       `json:"retired"`
	ActiveCapacity  int       `json:"active_capacity"`
	RetiredCapacity int       `json:"retired_capacity"`
}

func registryStatus(r quotaRecord, version string) RegistryStatus {
	out := RegistryStatus{Enabled: r.Schema == 4, Revision: registryDigest([]byte(version)), Active: len(r.Entities), Retired: len(r.Retirements), ActiveCapacity: entityCapacity(r), RetiredCapacity: retirementCapacity(r)}
	if r.Registry != nil {
		out.OperationID = r.Registry.OperationID
		out.Actor = r.Registry.Actor
		out.At = r.Registry.At
	}
	return out
}
func (q *Quotas) RegistryStatus(ctx context.Context, id durableentity.ID) (RegistryStatus, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return RegistryStatus{}, err
	}
	r, v, err := q.read(ctx, scopeKey(scope))
	if err != nil {
		return RegistryStatus{}, err
	}
	return registryStatus(r, v), nil
}

// EnableRegistry migrates one authenticated ledger snapshot. There is no partial
// backfill authority: a stale expected revision cannot publish any shard set.
func (q *Quotas) EnableRegistry(ctx context.Context, id durableentity.ID, operation, expectedRevision, actor string) error {
	scope, err := scopeFor(id)
	if err != nil {
		return err
	}
	if !sessionUUID(operation) || !validScopeKey(expectedRevision) || !grantIdentity(actor) {
		return durableentity.ErrInvalid
	}
	r, v, err := q.read(ctx, scopeKey(scope))
	if err != nil {
		return err
	}
	if r.Registry != nil {
		if r.Registry.OperationID == operation && r.Registry.SourceRevision == expectedRevision {
			return nil
		}
		return durableentity.ErrRequestConflict
	}
	if registryDigest([]byte(v)) != expectedRevision {
		return durableentity.ErrConflict
	}
	prune(&r, q.now())
	if r.Schema < 2 || r.Limits != q.Limits || r.Migration == nil || r.Migration.Phase != "complete" || pendingRetirements(r) {
		return durableentity.ErrInventoryPending
	}
	if len(r.Connections) != 0 {
		return durableentity.ErrBusy
	}
	r.Schema = 4
	r.Registry = &quotaRegistry{OperationID: operation, SourceRevision: expectedRevision, Actor: actor, At: q.now()}
	body, err := q.marshalQuota(ctx, scopeKey(scope), r)
	if err != nil {
		return err
	}
	next, err := q.Store.Put(ctx, "tenant-quotas/v1/"+scopeKey(scope), body, v)
	if errors.Is(err, durableentity.ErrConflict) {
		return err
	}
	if err != nil || next == "" {
		return errors.Join(durableentity.ErrUncertain, err)
	}
	return nil
}

type RegistryEntry struct {
	EntityKey     string           `json:"entity_key"`
	State         string           `json:"state"`
	ReservedBytes int64            `json:"reserved_bytes"`
	Retirement    *QuotaRetirement `json:"retirement,omitempty"`
}
type RegistryPage struct {
	RegistryStatus
	Entries    []RegistryEntry `json:"entries"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// Cursor binds scope and the complete quota snapshot, so pagination never mixes
// retirement credits from different revisions. Any concurrent root write expires it.
func (q *Quotas) ListRegistry(ctx context.Context, id durableentity.ID, cursor string) (RegistryPage, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return RegistryPage{}, err
	}
	key := scopeKey(scope)
	r, v, err := q.read(ctx, key)
	if err != nil {
		return RegistryPage{}, err
	}
	out := RegistryPage{RegistryStatus: registryStatus(r, v), Entries: []RegistryEntry{}}
	last := ""
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 3 || parts[0] != key || parts[1] != out.Revision || !validScopeKey(parts[2]) {
			return RegistryPage{}, durableentity.ErrConflict
		}
		last = parts[2]
	}
	entries := map[string]RegistryEntry{}
	for _, e := range r.Entities {
		entries[e.Key] = RegistryEntry{EntityKey: e.Key, State: "active", ReservedBytes: e.Bytes}
	}
	for _, e := range r.Retirements {
		entry := entries[e.EntityKey]
		copy := e
		entry.EntityKey = e.EntityKey
		entry.State = "retired"
		entry.Retirement = &copy
		if e.Phase == "complete" {
			entry.ReservedBytes = e.RetainedBytes
		}
		entries[e.EntityKey] = entry
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		if k > last {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i == 64 {
			out.NextCursor = key + ":" + out.Revision + ":" + keys[i-1]
			break
		}
		out.Entries = append(out.Entries, entries[k])
	}
	return out, nil
}

func retirementSpace(r quotaRecord, key string) bool {
	if len(r.Retirements) >= retirementCapacity(r) {
		return false
	}
	if r.Schema != 4 {
		return true
	}
	count := 0
	for _, e := range r.Retirements {
		if registryIndex(e.EntityKey) == registryIndex(key) {
			count++
		}
	}
	return count < 256
}
