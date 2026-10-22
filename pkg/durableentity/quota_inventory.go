package durableentity

import "context"

// QuotaInventoryEntity exposes only identity and committed usage metadata.
// Missing usage needs the existing fenced per-entity inventory workflow.
type QuotaInventoryEntity struct {
	Entity            ID     `json:"entity"`
	RetainedBytes     *int64 `json:"retained_bytes"`
	StorageLimitBytes int64  `json:"storage_limit_bytes"`
}
type QuotaInventoryPage struct {
	Entities   []QuotaInventoryEntity `json:"entities"`
	NextCursor string                 `json:"next_cursor"`
}

// ScanQuotaInventory is a bounded, read-only manifest scan. Delimiter pages
// include the whole bucket because identities are hashed; only the requested
// account/app/immutable environment/tenant is returned. Lists are observations,
// not snapshots. Every prefix must be valid before advancing the cursor.
func (m *Manager) ScanQuotaInventory(ctx context.Context, cursor string, scope ID) (QuotaInventoryPage, error) {
	if !scope.valid() || scope.EnvironmentID == "" {
		return QuotaInventoryPage{}, ErrInvalid
	}
	page, err := m.entityPrefixes(ctx, cursor, 16)
	if err != nil {
		return QuotaInventoryPage{}, err
	}
	out := QuotaInventoryPage{Entities: []QuotaInventoryEntity{}, NextCursor: page.NextCursor}
	seen := map[string]bool{}
	for _, prefix := range page.Prefixes {
		if seen[prefix] {
			return QuotaInventoryPage{}, ErrCorrupt
		}
		seen[prefix] = true
		base, err := m.manifestAtPrefix(ctx, prefix)
		if err != nil {
			return QuotaInventoryPage{}, err
		}
		id := base.ID
		// Retirement owns its residual recovery charge; migration must not
		// recreate a full active reservation for a permanent tombstone.
		if base.Retirement != nil {
			continue
		}
		if id.AccountID == scope.AccountID && id.AppID == scope.AppID && id.EnvironmentID == "" {
			return QuotaInventoryPage{}, ErrInventoryPending
		}
		if id.AccountID != scope.AccountID || id.AppID != scope.AppID || id.EnvironmentID != scope.EnvironmentID || id.TenantID != scope.TenantID {
			continue
		}
		entry := QuotaInventoryEntity{Entity: id, StorageLimitBytes: base.StorageLimitBytes}
		if base.StorageUsage != nil {
			n := base.StorageUsage.TotalBytes()
			entry.RetainedBytes = &n
		}
		out.Entities = append(out.Entities, entry)
	}
	return out, nil
}
