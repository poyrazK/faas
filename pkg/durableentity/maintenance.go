// adr: 678
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// MaintenanceResult reports one entity visit, not an entire bucket sweep.
type MaintenanceResult struct {
	Cleanup        CleanupResult
	Visited        int
	Skipped        int
	Busy           int
	Failed         int
	Recovered      bool
	SweepCompleted bool
	Inventory      *InventoryResult
}

type maintenanceCheckpoint struct {
	Schema     int       `json:"schema"`
	Scope      string    `json:"scope"`
	Revision   string    `json:"revision"`
	Owner      string    `json:"owner"`
	Token      string    `json:"token"`
	Epoch      uint64    `json:"epoch"`
	ExpiresAt  time.Time `json:"expires_at"`
	Cursor     string    `json:"cursor,omitempty"`
	Pending    []string  `json:"pending,omitempty"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type cleanupHint struct {
	Schema    int    `json:"schema"`
	Entity    ID     `json:"entity"`
	Cursor    string `json:"cursor,omitempty"`
	Inventory bool   `json:"inventory,omitempty"`
}

// CheckMaintenance probes listing and deletes only its own unique probe key.
// Ordinary invocation never requires DELETE permission.
func (m *Manager) CheckMaintenance(ctx context.Context) error {
	store, ok := m.store.(CleanupStore)
	if !ok {
		return ErrUnsupported
	}
	if _, err := m.maintenancePrefixes(ctx, ""); err != nil {
		return err
	}
	prefix := "gregale/durable-entities/v1/probes/maintenance/" + uuid.NewString() + "/"
	key := prefix + "probe.json"
	if _, err := m.store.Put(ctx, key, []byte(`{"probe":true}`), ""); err != nil {
		return writeFailure("create maintenance probe", err)
	}
	page, err := store.ListEntityObjects(ctx, prefix, "", api.DurableEntityCleanupPageSize)
	if err != nil {
		return err
	}
	if len(page.Keys) != 1 || page.Keys[0] != key || page.NextCursor != "" {
		return ErrUnsupported
	}
	if err := store.DeleteEntityObject(ctx, key); err != nil {
		return err
	}
	if _, _, err := m.store.Get(ctx, key, api.MaxDurableEntityManifestBytes); !errors.Is(err, ErrNotFound) {
		return errors.Join(ErrUnsupported, err)
	}
	return nil
}

func (m *Manager) maintenancePrefixes(ctx context.Context, cursor string) (EntityPrefixPage, error) {
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityMaintenanceReadTimeout)
	defer cancel()
	return m.entityPrefixes(ctx, cursor, api.DurableEntityMaintenanceScanPageSize)
}

func maintenanceScope(apps map[string]bool) (string, error) {
	var allowed []string
	for app, enabled := range apps {
		if enabled {
			if !validIdentity(app) {
				return "", ErrInvalid
			}
			allowed = append(allowed, app)
		}
	}
	if len(allowed) == 0 {
		return "", ErrInvalid
	}
	slices.Sort(allowed)
	body, _ := json.Marshal(allowed)
	return digest(body), nil
}

// MaintenanceStep claims a bucket-backed scan checkpoint and visits at most
// one entity. Each visit collects only one page, preserving fair rotation even
// when a single entity has a large journal. A crashed visit can repeat safely.
// The scan checkpoint and per-entity cursor are hints, never deletion authority.
func (m *Manager) MaintenanceStep(ctx context.Context, owner string, apps map[string]bool) (MaintenanceResult, error) {
	if !validIdentity(owner) {
		return MaintenanceResult{}, ErrInvalid
	}
	scope, err := maintenanceScope(apps)
	if err != nil {
		return MaintenanceResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.DurableEntityMaintenanceTimeout)
	defer cancel()
	checkpoint, etag, recovered, err := m.claimMaintenance(ctx, scope, owner)
	if err != nil {
		return MaintenanceResult{}, err
	}
	result := MaintenanceResult{Recovered: recovered}
	if len(checkpoint.Pending) == 0 {
		page, listErr := m.maintenancePrefixes(ctx, checkpoint.Cursor)
		if listErr != nil {
			// A persisted provider cursor can expire. Restart discovery; repeated
			// visits are harmless and no list result authorizes deletion.
			checkpoint.Cursor, checkpoint.NextCursor = "", ""
			return result, errors.Join(listErr, m.finishMaintenance(ctx, checkpoint, etag))
		}
		checkpoint.Pending, checkpoint.NextCursor = page.Prefixes, page.NextCursor
	}
	if len(checkpoint.Pending) > 0 {
		result.Visited = 1
		m.maintainEntity(ctx, checkpoint.Pending[0], owner, apps, &result)
		checkpoint.Pending = checkpoint.Pending[1:]
	}
	if len(checkpoint.Pending) == 0 {
		checkpoint.Cursor, checkpoint.NextCursor = checkpoint.NextCursor, ""
		result.SweepCompleted = checkpoint.Cursor == ""
	}
	return result, m.finishMaintenance(ctx, checkpoint, etag)
}

func (m *Manager) maintainEntity(ctx context.Context, prefix, owner string, apps map[string]bool, result *MaintenanceResult) {
	readCtx, cancel := context.WithTimeout(ctx, api.DurableEntityMaintenanceReadTimeout)
	base, err := m.manifestAtPrefix(readCtx, prefix)
	cancel()
	if err != nil {
		result.Failed++
		return
	}
	if !apps[base.ID.AppID] {
		result.Skipped++
		return
	}
	claim, err := m.Acquire(ctx, base.ID, owner)
	if err != nil {
		if errors.Is(err, ErrBusy) || errors.Is(err, ErrConflict) {
			result.Busy++
		} else {
			result.Failed++
		}
		return
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DurableEntityReleaseTimeout)
		defer cancel()
		if err := m.Release(releaseCtx, claim); err != nil {
			result.Failed++
		}
	}()
	base, _, err = m.owned(ctx, claim)
	if err != nil {
		result.Failed++
		return
	}
	hint, etag, err := m.readCleanupHint(ctx, base.ID)
	if err != nil {
		result.Failed++
		return
	}
	if base.StorageUsage == nil || hint.Inventory {
		hint.Inventory = true
		var inventory InventoryResult
		inventory, err = m.Inventory(ctx, claim)
		if err == nil {
			result.Inventory = &inventory
			if inventory.Complete {
				hint.Inventory, hint.Cursor = false, ""
			}
		}
	} else {
		result.Cleanup, err = m.Collect(ctx, claim, hint.Cursor)
		hint.Cursor = result.Cleanup.NextCursor
		if err == nil && hint.Cursor == "" {
			hint.Inventory = true
		}
	}
	if err != nil {
		result.Failed++
		// An invalid/expired cursor or interrupted page is restarted next visit.
		hint.Cursor = ""
	}
	if _, _, err := m.owned(ctx, claim); err != nil {
		result.Failed++
		return
	}
	if _, err := m.putMaintenanceObject(ctx, base.ID.prefix()+"maintenance.json", hint, etag); err != nil {
		result.Failed++
	}
}

func (m *Manager) readCleanupHint(ctx context.Context, id ID) (cleanupHint, string, error) {
	hint := cleanupHint{Schema: 1, Entity: id}
	body, etag, err := m.store.Get(ctx, id.prefix()+"maintenance.json", api.MaxDurableEntityMaintenanceBytes)
	if errors.Is(err, ErrNotFound) {
		return hint, "", nil
	}
	if err != nil {
		return hint, "", err
	}
	if etag == "" || len(body) > api.MaxDurableEntityMaintenanceBytes || json.Unmarshal(body, &hint) != nil || hint.Schema != 1 || hint.Entity != id || len(hint.Cursor) > api.MaxObjectS3ListCursorBytes {
		return cleanupHint{}, "", ErrCorrupt
	}
	return hint, etag, nil
}
