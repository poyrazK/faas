// adr: 678
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// InventoryResult is an exact committed-root measurement and, when supported,
// a paginated observation of current keys. Current keys exclude native history.
// Sampling across pages is not a transaction or a provider billing inventory.
type InventoryResult struct {
	Version           uint64       `json:"version"`
	Usage             StorageUsage `json:"committed"`
	LogicalComplete   bool         `json:"logical_complete"`
	Complete          bool         `json:"complete"`
	CurrentBytes      int64        `json:"current_key_bytes"`
	CurrentObjects    uint64       `json:"current_key_objects"`
	CurrentBytesKnown bool         `json:"current_key_bytes_known"`
	StorageLimitBytes int64        `json:"storage_limit_bytes"`
	At                time.Time    `json:"sampled_at"`
}

type inventoryProgress struct {
	Schema            int          `json:"schema"`
	Entity            ID           `json:"entity"`
	SnapshotKey       string       `json:"snapshot_key"`
	SnapshotHash      string       `json:"snapshot_hash"`
	Version           uint64       `json:"version"`
	Pending           []journalRef `json:"pending,omitempty"`
	Usage             StorageUsage `json:"usage"`
	LogicalComplete   bool         `json:"logical_complete"`
	Complete          bool         `json:"complete"`
	Cursor            string       `json:"cursor,omitempty"`
	LastKey           string       `json:"last_key,omitempty"`
	CurrentBytes      int64        `json:"current_bytes"`
	CurrentObjects    uint64       `json:"current_objects"`
	CurrentBytesKnown bool         `json:"current_bytes_known"`
	Revision          string       `json:"revision"`
	Checksum          string       `json:"checksum"`
}

func inventoryChecksum(p inventoryProgress) string {
	p.Checksum = ""
	body, _ := json.Marshal(p)
	return digest(body)
}

// Inventory visits at most 32 journal nodes OR 32 current-key records. Its
// bounded proof stack survives restart. Quota accounting follows authenticated
// snapshot/index references, never LIST sizes or candidate object contents.
func (m *Manager) Inventory(ctx context.Context, claim Claim) (InventoryResult, error) {
	if !claim.ID.valid() {
		return InventoryResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityInventoryTimeout)
	defer cancel()
	unlock, err := m.lock(ctx, claim.ID.prefix())
	if err != nil {
		return InventoryResult{}, err
	}
	defer unlock()
	base, _, err := m.owned(ctx, claim)
	if err != nil {
		return InventoryResult{}, err
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return InventoryResult{}, m.restoreFailure(ctx, claim, base, err)
	}
	p, etag, err := m.readInventoryProgress(ctx, base)
	if err != nil {
		return InventoryResult{}, err
	}
	if p.Schema == 0 || p.Complete || p.SnapshotKey != base.SnapshotKey || p.SnapshotHash != base.SnapshotHash || p.Version != base.Version {
		p, err = m.startInventory(ctx, base, state)
		if err != nil {
			return InventoryResult{}, m.restoreFailure(ctx, claim, base, err)
		}
	}
	if !p.LogicalComplete {
		if err := m.inventoryJournalPage(ctx, base, &p); err != nil {
			return InventoryResult{}, m.restoreFailure(ctx, claim, base, err)
		}
	} else {
		if err := m.inventoryCurrentPage(ctx, base, &p); err != nil {
			// Native cursors expire independently of the committed root proof.
			p.Cursor, p.LastKey, p.CurrentBytes, p.CurrentObjects = "", "", 0, 0
			p.CurrentBytesKnown = true
			return InventoryResult{}, errors.Join(err, m.saveInventoryProgress(ctx, base, p, etag))
		}
	}
	latest, manifestETag, err := m.owned(ctx, claim)
	if err != nil {
		return InventoryResult{}, err
	}
	if !sameInventoryRoot(base, latest) {
		return InventoryResult{}, ErrConflict
	}
	if p.LogicalComplete {
		if latest.StorageUsage != nil && !sameUsage(*latest.StorageUsage, p.Usage) {
			return InventoryResult{}, ErrCorrupt
		}
		if latest.StorageUsage == nil {
			latest.StorageUsage = &p.Usage
			if err := m.putManifest(ctx, latest, manifestETag); err != nil {
				return InventoryResult{}, err
			}
		}
	}
	if err := m.saveInventoryProgress(ctx, base, p, etag); err != nil {
		return InventoryResult{}, err
	}
	return InventoryResult{Version: base.Version, Usage: p.Usage, LogicalComplete: p.LogicalComplete, Complete: p.Complete,
		CurrentBytes: p.CurrentBytes, CurrentObjects: p.CurrentObjects, CurrentBytesKnown: p.Complete && p.CurrentBytesKnown,
		StorageLimitBytes: latest.StorageLimitBytes, At: m.now().UTC()}, nil
}

func sameInventoryRoot(a, b manifest) bool {
	return a.ID == b.ID && a.Version == b.Version && a.SnapshotKey == b.SnapshotKey && a.SnapshotHash == b.SnapshotHash
}

func sameUsage(a, b StorageUsage) bool {
	a.Checksum, b.Checksum = "", ""
	return a == b
}

func (m *Manager) startInventory(ctx context.Context, base manifest, state snapshot) (inventoryProgress, error) {
	p := inventoryProgress{Schema: 1, Entity: base.ID, SnapshotKey: base.SnapshotKey, SnapshotHash: base.SnapshotHash, Version: base.Version, CurrentBytesKnown: true}
	if base.Version != 0 {
		body, _, err := m.store.Get(ctx, base.SnapshotKey, api.MaxDurableEntitySnapshotBytes)
		if err != nil {
			return p, errorsForRestore(err)
		}
		if digest(body) != base.SnapshotHash {
			return p, ErrCorrupt
		}
		p.Usage.SnapshotBytes = int64(len(body))
	}
	p.Usage.ReceiptCount = uint64(len(state.Receipts))
	if state.LegacyReceipts != nil {
		block, size, err := m.readLegacySized(ctx, state)
		if err != nil {
			return p, err
		}
		p.Usage.LegacyBytes = size
		p.Usage.ReceiptCount += uint64(len(block.Receipts))
	}
	if state.ReceiptRoot != nil {
		p.Pending = []journalRef{*state.ReceiptRoot}
	}
	return p, nil
}

func (m *Manager) inventoryJournalPage(ctx context.Context, base manifest, p *inventoryProgress) error {
	for visited := 0; len(p.Pending) > 0 && visited < api.DurableEntityInventoryPageSize; visited++ {
		ref := p.Pending[len(p.Pending)-1]
		p.Pending = p.Pending[:len(p.Pending)-1]
		node, size, err := m.readJournalSized(ctx, base.ID, ref, base.Version)
		if err != nil {
			return err
		}
		if node.Receipt != nil {
			if p.Usage.ReceiptCount == math.MaxUint64 || size > math.MaxInt64-p.Usage.ReceiptBytes {
				return ErrCorrupt
			}
			p.Usage.ReceiptCount++
			p.Usage.ReceiptBytes += size
		} else {
			if size > math.MaxInt64-p.Usage.IndexBytes {
				return ErrCorrupt
			}
			p.Usage.IndexBytes += size
			// Deterministic depth-first order; disjoint nibble partitions make
			// every reachable leaf unique without an entity-sized visited set.
			for _, nibble := range "fedcba9876543210" {
				if child, ok := node.Children[string(nibble)]; ok {
					p.Pending = append(p.Pending, child)
				}
			}
		}
		if len(p.Pending) > api.MaxDurableEntityInventoryPending || !validUsage(p.Usage) {
			return ErrCorrupt
		}
	}
	p.LogicalComplete = len(p.Pending) == 0
	return nil
}

func (m *Manager) inventoryCurrentPage(ctx context.Context, base manifest, p *inventoryProgress) error {
	lister, ok := m.store.(EntityObjectLister)
	if !ok {
		p.Complete, p.CurrentBytesKnown = true, false
		return nil
	}
	page, err := lister.ListEntityObjects(ctx, base.ID.prefix(), p.Cursor, api.DurableEntityInventoryPageSize)
	if errors.Is(err, ErrUnsupported) {
		p.Complete, p.CurrentBytesKnown = true, false
		return nil
	}
	if err != nil {
		return err
	}
	if !validCleanupPage(base.ID, p.Cursor, page) {
		return ErrCorrupt
	}
	for _, key := range page.Keys {
		if key <= p.LastKey || p.CurrentObjects == math.MaxUint64 {
			return ErrCorrupt
		}
		p.LastKey = key
		p.CurrentObjects++
		size, known := page.Sizes[key]
		if !known || size < 0 || size > math.MaxInt64-p.CurrentBytes {
			p.CurrentBytesKnown = false
		} else {
			p.CurrentBytes += size
		}
	}
	p.Cursor, p.Complete = page.NextCursor, page.NextCursor == ""
	return nil
}

func validInventoryProgress(base manifest, p inventoryProgress) bool {
	if p.Schema != 1 || p.Entity != base.ID || !validUUID(p.Revision) || p.Checksum != inventoryChecksum(p) || !validUsage(p.Usage) ||
		len(p.Pending) > api.MaxDurableEntityInventoryPending || len(p.Cursor) > api.MaxObjectS3ListCursorBytes || p.CurrentBytes < 0 ||
		p.LastKey != "" && !strings.HasPrefix(p.LastKey, base.ID.prefix()) || p.LogicalComplete && len(p.Pending) > 0 || p.Complete && !p.LogicalComplete {
		return false
	}
	if p.Version == 0 {
		if p.SnapshotKey != "" || p.SnapshotHash != "" {
			return false
		}
	} else if _, ok := snapshotGeneration(base.ID, p.SnapshotKey); !ok || !validDigest(p.SnapshotHash) {
		return false
	}
	for _, ref := range p.Pending {
		if !validJournalRef(base.ID, &ref, base.Generation) {
			return false
		}
	}
	return true
}

func (m *Manager) readInventoryProgress(ctx context.Context, base manifest) (inventoryProgress, string, error) {
	body, etag, err := m.store.Get(ctx, base.ID.prefix()+"inventory.json", api.MaxDurableEntityInventoryBytes)
	if errors.Is(err, ErrNotFound) {
		return inventoryProgress{}, "", nil
	}
	if err != nil {
		return inventoryProgress{}, "", err
	}
	var p inventoryProgress
	if etag == "" || len(body) > api.MaxDurableEntityInventoryBytes || json.Unmarshal(body, &p) != nil || !validInventoryProgress(base, p) {
		return p, "", ErrCorrupt
	}
	return p, etag, nil
}

func (m *Manager) saveInventoryProgress(ctx context.Context, base manifest, p inventoryProgress, etag string) error {
	p.Revision = uuid.NewString()
	p.Checksum = inventoryChecksum(p)
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if len(body) > api.MaxDurableEntityInventoryBytes {
		return ErrLimit
	}
	version, err := m.store.Put(ctx, base.ID.prefix()+"inventory.json", body, etag)
	if err != nil {
		return writeFailure("save storage inventory", err)
	}
	if version == "" {
		return ErrUncertain
	}
	return nil
}
