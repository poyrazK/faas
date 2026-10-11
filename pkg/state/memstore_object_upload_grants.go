package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectUploadGrantStore = (*MemStore)(nil)

func (m *MemStore) CreateObjectUploadGrant(ctx context.Context, g ObjectUploadGrant, ttl int) (ObjectUploadGrant, error) {
	if !validObjectUploadGrant(g, ttl) {
		return ObjectUploadGrant{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return ObjectUploadGrant{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	live, exists := m.objectBuckets[g.Bucket.ID]
	if !exists || !sameObjectMutationBucket(g.Bucket, live) || !m.cloneBucketAccessibleLocked(live) {
		return ObjectUploadGrant{}, ErrConflict
	}
	if _, fenced := m.objectWriteFences[live.ID]; fenced {
		return ObjectUploadGrant{}, ErrObjectBucketWriteFenced
	}
	if g.Kind == ObjectUploadGrantMultipartPart && !validObjectUploadGrantPart(g, m.objectMultipartUploads[g.UploadID], time.Now().UTC()) {
		return ObjectUploadGrant{}, ErrConflict
	}
	for _, existing := range m.objectUploadGrants {
		if existing.ID == g.ID || existing.TokenHash == g.TokenHash {
			return ObjectUploadGrant{}, ErrConflict
		}
	}
	if m.objectUploadGrants == nil {
		m.objectUploadGrants = map[string]ObjectUploadGrant{}
	}
	g.Bucket = live
	g.CreatedAt = time.Now().UTC()
	g.ExpiresAt = g.CreatedAt.Add(time.Duration(ttl) * time.Second)
	if g.Kind == ObjectUploadGrantMultipartPart {
		if cap := m.objectMultipartUploads[g.UploadID].ExpiresAt; cap.Before(g.ExpiresAt) {
			g.ExpiresAt = cap
		}
	}
	if !g.ExpiresAt.After(g.CreatedAt) {
		return ObjectUploadGrant{}, ErrConflict
	}
	m.objectUploadGrants[g.ID] = cloneObjectUploadGrant(g)
	return cloneObjectUploadGrant(g), nil
}

func (m *MemStore) ResolveObjectUploadGrant(ctx context.Context, hash string) (ObjectUploadGrant, error) {
	if !validObjectUploadTokenHash(hash) {
		return ObjectUploadGrant{}, ErrNotFound
	}
	if err := ctx.Err(); err != nil {
		return ObjectUploadGrant{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for _, g := range m.objectUploadGrants {
		if g.TokenHash != hash || !g.ExpiresAt.After(now) {
			continue
		}
		b, exists := m.objectBuckets[g.Bucket.ID]
		if !exists || !sameObjectMutationBucket(g.Bucket, b) || !m.cloneBucketAccessibleLocked(b) {
			return ObjectUploadGrant{}, ErrNotFound
		}
		if g.Kind == ObjectUploadGrantMultipartPart && !validObjectUploadGrantPart(g, m.objectMultipartUploads[g.UploadID], now) {
			return ObjectUploadGrant{}, ErrNotFound
		}
		g.Bucket = b
		return cloneObjectUploadGrant(g), nil
	}
	return ObjectUploadGrant{}, ErrNotFound
}

func (m *MemStore) PruneExpiredObjectUploadGrants(ctx context.Context, limit int32) (int64, error) {
	if limit < 1 || limit > api.ObjectUploadGrantPruneBatch {
		return 0, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var count int64
	for id, g := range m.objectUploadGrants {
		if !g.ExpiresAt.After(time.Now().UTC()) && count < int64(limit) {
			delete(m.objectUploadGrants, id)
			count++
		}
	}
	return count, nil
}
