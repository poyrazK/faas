// adr: 712
package durableentity

import (
	"context"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// EntityObjectLister is optional. Current-key sizes are observational hints.
type EntityObjectLister interface {
	ListEntityObjects(context.Context, string, string, int32) (CleanupObjects, error)
}

// CleanupStore only deletes platform-private, immutable entity objects.
// List results are candidates, never commit authority.
type CleanupStore interface {
	EntityObjectLister
	DeleteEntityObject(context.Context, string) error
}

type CleanupObjects struct {
	Keys       []string
	NextCursor string
	Sizes      map[string]int64 // Optional provider-reported current sizes; never quota authority.
}

type CleanupResult struct {
	Deleted    int    `json:"deleted"`
	Retained   int    `json:"retained"`
	Failed     int    `json:"failed"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Collect reclaims one bounded page under an explicit platform claim. Its CAS
// generation barrier prevents every older computation from publishing. Objects
// created in or after the barrier generation are never candidates. Old objects
// reachable from the barrier snapshot remain live through subsequent commits.
// Lost barrier acknowledgements cause no deletion. Restart may use an empty
// cursor; uncertain deletes are safe to retry because keys are never reused.
func (m *Manager) Collect(ctx context.Context, claim Claim, cursor string) (CleanupResult, error) {
	store, ok := m.store.(CleanupStore)
	if !ok {
		return CleanupResult{}, ErrUnsupported
	}
	if !claim.ID.valid() || len(cursor) > api.MaxObjectS3ListCursorBytes {
		return CleanupResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityCleanupTimeout)
	defer cancel()
	unlock, err := m.lock(ctx, claim.ID.prefix())
	if err != nil {
		return CleanupResult{}, err
	}
	defer unlock()
	base, etag, err := m.owned(ctx, claim)
	if err != nil {
		return CleanupResult{}, err
	}
	state, err := m.readSnapshot(ctx, base)
	if err != nil {
		return CleanupResult{}, m.restoreFailure(ctx, claim, base, err)
	}
	if base.Generation == ^uint64(0) {
		return CleanupResult{}, ErrLimit
	}
	cache := make(map[string]journalNode)
	if state.ReceiptRoot != nil {
		node, err := m.readJournal(ctx, base.ID, *state.ReceiptRoot, state.Version)
		if err != nil {
			return CleanupResult{}, m.restoreFailure(ctx, claim, base, err)
		}
		cache[state.ReceiptRoot.Key] = node
	}
	if state.LegacyReceipts != nil {
		if _, err := m.readLegacyReceipts(ctx, state); err != nil {
			return CleanupResult{}, m.restoreFailure(ctx, claim, base, err)
		}
	}
	if !m.now().Before(base.ExpiresAt) {
		return CleanupResult{}, ErrStaleOwner
	}
	base.Generation++
	if err := m.putManifest(ctx, base, etag); err != nil {
		return CleanupResult{}, err
	}
	page, err := store.ListEntityObjects(ctx, claim.ID.prefix(), cursor, api.DurableEntityCleanupPageSize)
	if err != nil {
		return CleanupResult{}, err
	}
	if !validCleanupPage(claim.ID, cursor, page) {
		return CleanupResult{}, ErrCorrupt
	}
	result := CleanupResult{NextCursor: page.NextCursor}
	for _, key := range page.Keys {
		if err := ctx.Err(); err != nil {
			return CleanupResult{}, err
		}
		unused, err := m.unusedObject(ctx, base, state, key, cache)
		if err != nil {
			result.Failed++
			continue
		}
		if !unused {
			result.Retained++
			continue
		}
		if err := store.DeleteEntityObject(ctx, key); err != nil {
			result.Failed++
		} else {
			result.Deleted++
		}
	}
	return result, nil
}

func validCleanupPage(id ID, cursor string, page CleanupObjects) bool {
	if len(page.Keys) > api.DurableEntityCleanupPageSize || len(page.NextCursor) > api.MaxObjectS3ListCursorBytes || page.NextCursor != "" && (page.NextCursor == cursor || len(page.Keys) == 0) {
		return false
	}
	for _, key := range page.Keys {
		if !strings.HasPrefix(key, id.prefix()) {
			return false
		}
	}
	return true
}

func (m *Manager) unusedObject(ctx context.Context, base manifest, state snapshot, key string, cache map[string]journalNode) (bool, error) {
	if generation, ok := snapshotGeneration(base.ID, key); ok {
		return generation < base.Generation && key != base.SnapshotKey, nil
	}
	generation, prefix, legacy, ok := receiptPath(base.ID, key)
	if !ok || generation >= base.Generation {
		return false, nil
	}
	if legacy {
		return state.LegacyReceipts == nil || state.LegacyReceipts.Key != key, nil
	}
	ref := state.ReceiptRoot
	for ref != nil && strings.HasPrefix(prefix, ref.Prefix) {
		if ref.Prefix == prefix {
			return ref.Key != key, nil
		}
		node, exists := cache[ref.Key]
		if !exists {
			var err error
			node, err = m.readJournal(ctx, base.ID, *ref, state.Version)
			if err != nil {
				return false, err
			}
			cache[ref.Key] = node
		}
		if node.Receipt != nil {
			return true, nil
		}
		child, exists := node.Children[prefix[len(ref.Prefix):len(ref.Prefix)+1]]
		if !exists {
			return true, nil
		}
		ref = &child
	}
	return true, nil
}
