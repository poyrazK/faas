// adr: 712
package durableentity

import (
	"context"
	"encoding/json"
	"math"
)

// StorageUsage describes exactly the immutable objects reachable from one
// committed snapshot: that snapshot, receipt leaves, index branches and archive.
// It excludes manifests, checkpoints, probes, orphans and provider version history.
type StorageUsage struct {
	SnapshotBytes int64  `json:"snapshot_bytes"`
	ReceiptBytes  int64  `json:"receipt_bytes"`
	IndexBytes    int64  `json:"index_bytes"`
	LegacyBytes   int64  `json:"legacy_bytes"`
	ReceiptCount  uint64 `json:"receipt_count"`
	Checksum      string `json:"checksum,omitempty"`
}

func (u StorageUsage) TotalBytes() int64 {
	return u.SnapshotBytes + u.ReceiptBytes + u.IndexBytes + u.LegacyBytes
}

func validUsage(u StorageUsage) bool {
	var total int64
	for _, size := range []int64{u.SnapshotBytes, u.ReceiptBytes, u.IndexBytes, u.LegacyBytes} {
		if size < 0 || size > math.MaxInt64-total {
			return false
		}
		total += size
	}
	return true
}

func usageChecksum(value manifest) string {
	u := *value.StorageUsage
	u.Checksum = ""
	body, _ := json.Marshal(struct {
		ID        ID
		Version   uint64
		Key, Hash string
		Usage     StorageUsage
	}{value.ID, value.Version, value.SnapshotKey, value.SnapshotHash, u})
	return digest(body)
}

func sealStorageUsage(value *manifest) {
	if value.StorageUsage != nil {
		// Keep caller-owned statistics immutable when authority metadata changes.
		usage := *value.StorageUsage
		value.StorageUsage = &usage
		value.StorageUsage.Checksum = usageChecksum(*value)
	}
}

func validStorageMetadata(value manifest) bool {
	if value.StorageLimitBytes < 0 || value.Schema < 3 && (value.StorageUsage != nil || value.StorageLimitBytes != 0) {
		return false
	}
	if value.StorageUsage == nil {
		return true
	}
	u := *value.StorageUsage
	return validUsage(u) && u.Checksum == usageChecksum(value) && (value.Version != 0 || u.TotalBytes() == 0 && u.ReceiptCount == 0)
}

func stricterStorageLimit(persisted, configured int64) int64 {
	if configured > 0 && (persisted == 0 || configured < persisted) {
		return configured
	}
	return persisted
}

// SetStorageLimit is private operator authority, for explicit increases or
// removal (zero). Lowering a cap preserves all data and old receipt replay.
// Ordinary acquisitions can only tighten a configured cap, never remove one.
func (m *Manager) SetStorageLimit(ctx context.Context, claim Claim, limit int64) error {
	if limit < 0 {
		return ErrInvalid
	}
	value, etag, err := m.owned(ctx, claim)
	if err != nil {
		return err
	}
	value.StorageLimitBytes = limit
	return m.putManifest(ctx, value, etag)
}

func projectedUsage(base manifest, delta journalDelta, snapshotBytes int64) (*StorageUsage, error) {
	if base.StorageUsage == nil {
		if base.StorageLimitBytes > 0 {
			return nil, ErrInventoryPending
		}
		return nil, nil
	}
	u := *base.StorageUsage
	u.Checksum = ""
	if u.ReceiptCount == math.MaxUint64 || delta.leafBytes > math.MaxInt64-u.ReceiptBytes || delta.legacyBytes > math.MaxInt64-u.LegacyBytes || delta.indexBytes > 0 && delta.indexBytes > math.MaxInt64-u.IndexBytes || delta.indexBytes < 0 && -delta.indexBytes > u.IndexBytes {
		return nil, ErrLimit
	}
	u.SnapshotBytes = snapshotBytes
	u.ReceiptBytes += delta.leafBytes
	u.IndexBytes += delta.indexBytes
	u.LegacyBytes += delta.legacyBytes
	u.ReceiptCount++
	if !validUsage(u) {
		return nil, ErrLimit
	}
	if base.StorageLimitBytes > 0 && u.TotalBytes() > base.StorageLimitBytes {
		return nil, &LimitError{Budget: "committed_storage_bytes", Limit: base.StorageLimitBytes, Observed: u.TotalBytes()}
	}
	return &u, nil
}
