package state

import (
	"context"
	"time"
)

func (m *MemStore) ClaimObjectInventoryScan(ctx context.Context, bucket, token string) (ObjectInventoryScan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return ObjectInventoryScan{}, err
	}
	now := time.Now().UTC()
	u := m.objectUsage[bucket]
	if m.inventoryClaimFencedLocked(bucket) || token == "" || m.objectBuckets[bucket].State != "ready" || u.LeaseUntil.After(now) {
		return ObjectInventoryScan{}, ErrConflict
	}
	u.Token, u.LeaseUntil = token, now.Add(2*time.Minute)
	if u.AttemptAt.IsZero() {
		u.AttemptAt = now
	}
	resumable := inventoryScanResumable(u.InventoryScan, u.ObservedAt, now)
	if !resumable {
		u.InventoryScan = ObjectInventoryScan{StartedAt: now, ProgressAt: now}
	}
	// Only a claim that starts or publishes a listing makes deletions yield.
	if !resumable || u.InventoryScan.Complete {
		u.AttemptAt = now
	}
	if m.objectUsage == nil {
		m.objectUsage = map[string]ObjectBucketUsage{}
	}
	m.objectUsage[bucket] = u
	return u.InventoryScan, nil
}

func (m *MemStore) CheckpointObjectInventoryScan(ctx context.Context, bucket, token string, scan ObjectInventoryScan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	u, ok := m.objectUsage[bucket]
	if !ok || token == "" || u.Token != token || !validInventoryScanProgress(scan) || !u.InventoryScan.StartedAt.Equal(scan.StartedAt) || scan.Pages < u.InventoryScan.Pages {
		return ErrConflict
	}
	scan.ProgressAt = time.Now().UTC()
	u.InventoryScan = scan
	u.Token, u.LeaseUntil = "", time.Time{}
	m.objectUsage[bucket] = u
	return nil
}

func (m *MemStore) FinishObjectInventoryScan(ctx context.Context, bucket, token string, scan ObjectInventoryScan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	u := m.objectUsage[bucket]
	if m.inventoryPublicationRefusedLocked(bucket, token) || !scan.Complete || !validInventoryScanProgress(scan) || !u.InventoryScan.StartedAt.Equal(scan.StartedAt) || !scan.StartedAt.After(u.ObservedAt) {
		return ErrConflict
	}
	if u.ObservedAt.IsZero() {
		u.BaselineBytes, u.BaselineKeys = scan.Bytes, scan.Keys
	}
	u.ObservedBytes, u.ObservedKeys, u.ObservedAt = scan.Bytes, scan.Keys, scan.StartedAt
	u.InventoryScan = ObjectInventoryScan{}
	u.Token, u.LeaseUntil = "", time.Time{}
	m.objectUsage[bucket] = u
	return nil
}
