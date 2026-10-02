package state

// adr: 429

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
)

var _ BaseImageScanStore = (*MemStore)(nil)

func (m *MemStore) PublishBaseImageScan(ctx context.Context, input BaseImageScanInput) (BaseImageScan, error) {
	in, hash, err := prepareBaseImageScan(input)
	if err != nil {
		return BaseImageScan{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BaseImageScan{}, err
	}
	base, ok := m.baseImageProducers[in.BaseProducerID]
	if !ok {
		return BaseImageScan{}, ErrNotFound
	}
	if m.baseImageProducerCurrent[in.Artifact.StorageKey] != base.ID {
		return BaseImageScan{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkBaseScanProducer(in, base); err != nil {
		return BaseImageScan{}, err
	}
	if old, exists := m.baseImageScans[in.ID]; exists {
		if old.InputHash != hash || m.baseImageScanCurrent[in.Artifact.StorageKey] != old.ID {
			return BaseImageScan{}, ErrConflict
		}
		if err := validateBaseImageScan(old); err != nil {
			return BaseImageScan{}, err
		}
		return cloneBaseImageScan(old), nil
	}
	now := time.Now().UTC()
	if base.PublishedAt.After(now) {
		return BaseImageScan{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkProducerScanFreshness(in.Report, now); err != nil {
		return BaseImageScan{}, err
	}
	value := BaseImageScan{ID: in.ID, InputHash: hash, Input: in, ScannedAt: now, ExpiresAt: now.Add(api.ApplicationStandardArtifactScanTTL), Result: baseScanResult(in, now)}
	if m.baseImageScans == nil {
		m.baseImageScans = map[string]BaseImageScan{}
	}
	if m.baseImageScanCurrent == nil {
		m.baseImageScanCurrent = map[string]string{}
	}
	m.baseImageScans[in.ID] = value
	m.baseImageScanCurrent[in.Artifact.StorageKey] = in.ID
	return cloneBaseImageScan(value), nil
}

func (m *MemStore) GetCurrentBaseImageScan(ctx context.Context, key string) (BaseImageScan, error) {
	if !imagechain.ValidBaseKey(key) {
		return BaseImageScan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BaseImageScan{}, err
	}
	value, ok := m.baseImageScans[m.baseImageScanCurrent[key]]
	if !ok || m.baseImageProducerCurrent[key] != value.Input.BaseProducerID {
		return BaseImageScan{}, ErrNotFound
	}
	if err := validateBaseImageScan(value); err != nil {
		return BaseImageScan{}, err
	}
	return cloneBaseImageScan(value), nil
}

func (m *MemStore) GetFreshBaseImageScan(ctx context.Context, id, hash string) (BaseImageScan, error) {
	if !validStandardResourceRead(id, id) || len(hash) != 64 {
		return BaseImageScan{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return BaseImageScan{}, err
	}
	return m.freshBaseImageScanLocked(id, hash, time.Now().UTC())
}

func (m *MemStore) freshBaseImageScanLocked(id, hash string, now time.Time) (BaseImageScan, error) {
	base, ok := m.baseImageProducers[canonicalStandardUUID(id)]
	if !ok || base.InputHash != hash || m.baseImageProducerCurrent[base.Input.Artifact.StorageKey] != base.ID {
		return BaseImageScan{}, ErrApplicationStandardRuntimeStale
	}
	value, ok := m.baseImageScans[m.baseImageScanCurrent[base.Input.Artifact.StorageKey]]
	if !ok {
		return BaseImageScan{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkBaseImageScanLease(value, base, now); err != nil {
		return BaseImageScan{}, err
	}
	return cloneBaseImageScan(value), nil
}
