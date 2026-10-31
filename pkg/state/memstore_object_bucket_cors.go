package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectBucketCORSStore = (*MemStore)(nil)
var _ ObjectBucketCORSReader = (*MemStore)(nil)

func (m *MemStore) corsBucketLocked(account, app, bucket string, ready bool) error {
	b, ok := m.objectBuckets[bucket]
	if !ok || b.AccountID != account || b.AppID != app || b.State == "deleted" || !m.cloneBucketAccessibleLocked(b) {
		return ErrNotFound
	}
	if ready && b.State != "ready" {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) GetObjectBucketCORS(ctx context.Context, account, app, bucket string) (api.ObjectBucketCORS, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.ObjectBucketCORS{}, err
	}
	if err := m.corsBucketLocked(account, app, bucket, false); err != nil {
		return api.ObjectBucketCORS{}, err
	}
	c, ok := m.objectBucketCORS[bucket]
	if !ok {
		return api.ObjectBucketCORS{BucketID: bucket, Rules: []api.ObjectBucketCORSRule{}}, nil
	}
	c.Rules = cloneObjectBucketCORSRules(c.Rules)
	return c, nil
}

func (m *MemStore) PutObjectBucketCORS(ctx context.Context, account, app, bucket string, rules []api.ObjectBucketCORSRule) (api.ObjectBucketCORS, error) {
	if !api.ValidObjectBucketCORSRules(rules) {
		return api.ObjectBucketCORS{}, ErrConflict
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.ObjectBucketCORS{}, err
	}
	if err := m.corsBucketLocked(account, app, bucket, true); err != nil {
		return api.ObjectBucketCORS{}, err
	}
	if m.objectBucketCORS == nil {
		m.objectBucketCORS = map[string]api.ObjectBucketCORS{}
	}
	c := api.ObjectBucketCORS{BucketID: bucket, Rules: cloneObjectBucketCORSRules(rules), Revision: m.objectBucketCORS[bucket].Revision + 1, UpdatedAt: m.clock().UTC()}
	m.objectBucketCORS[bucket] = c
	c.Rules = cloneObjectBucketCORSRules(c.Rules)
	return c, nil
}

func (m *MemStore) DeleteObjectBucketCORS(ctx context.Context, account, app, bucket string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	if err := m.corsBucketLocked(account, app, bucket, false); err != nil {
		return err
	}
	delete(m.objectBucketCORS, bucket)
	return nil
}

func (m *MemStore) ObjectBucketCORSRules(ctx context.Context, bucket string) ([]api.ObjectBucketCORSRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.objectBucketCORS[bucket]
	if !ok || m.objectBuckets[bucket].State != "ready" {
		return nil, nil
	}
	return cloneObjectBucketCORSRules(c.Rules), nil
}
