package socket

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity"
)

var ErrMigrationPending = errors.New("durable quota migration holds admission")

type QuotaInventoryScanner interface {
	ScanQuotaInventory(context.Context, string, durableentity.ID) (durableentity.QuotaInventoryPage, error)
}
type QuotaMigration struct {
	ID        string       `json:"id"`
	Revision  uint64       `json:"revision"`
	Phase     string       `json:"phase"`
	Cursor    string       `json:"cursor"`
	NotBefore time.Time    `json:"not_before"`
	Limits    TenantLimits `json:"limits"`
	Observed  uint64       `json:"observed"`
}

func (m QuotaMigration) valid() bool {
	return sessionUUID(m.ID) && m.Revision > 0 && (m.Phase == "backfill" || m.Phase == "verify" || m.Phase == "complete") && len(m.Cursor) <= api.MaxObjectS3ListCursorBytes && !m.NotBefore.IsZero() && m.Limits.valid() && (m.Phase != "complete" || m.Cursor == "")
}

type QuotaMigrationPlan struct {
	NextCursor           string       `json:"next_cursor"`
	Limits               TenantLimits `json:"limits"`
	Entities             int          `json:"entities"`
	MissingReservations  int          `json:"missing_reservations"`
	NewReservations      int          `json:"new_reservations"`
	CurrentReservations  int          `json:"current_reservations"`
	CurrentReservedBytes int64        `json:"current_reserved_bytes"`
	MissingUsage         int          `json:"missing_usage"`
	AboveEntityCap       int          `json:"above_entity_cap"`
	AdditionalBytes      int64        `json:"additional_bytes"`
	ProjectedBytes       int64        `json:"projected_bytes"`
	ByteShortfall        int64        `json:"byte_shortfall"`
	EntityShortfall      int          `json:"entity_shortfall"`
	PageFits             bool         `json:"page_fits"`
}

func (q *Quotas) migrationRecord(ctx context.Context, id durableentity.ID) (quotaRecord, error) {
	scope, err := scopeFor(id)
	if err != nil {
		return quotaRecord{}, err
	}
	r, _, err := q.read(ctx, scopeKey(scope))
	if errors.Is(err, durableentity.ErrNotFound) {
		r = quotaRecord{Scope: scope, Limits: q.Limits, Window: q.now()}
		err = nil
	}
	if err != nil {
		return quotaRecord{}, err
	}
	if r.Schema < 2 {
		r.Limits = stricterLimits(r.Limits, q.Limits)
	}
	prune(&r, q.now())
	return r, nil
}
func (q *Quotas) MigrationStatus(ctx context.Context, id durableentity.ID) (*QuotaMigration, error) {
	r, err := q.migrationRecord(ctx, id)
	return r.Migration, err
}
func migrationPlan(r quotaRecord, page durableentity.QuotaInventoryPage) (QuotaMigrationPlan, error) {
	plan := QuotaMigrationPlan{NextCursor: page.NextCursor, Limits: r.Limits, Entities: len(page.Entities)}
	if len(page.Entities) > 16 || len(page.NextCursor) > api.MaxObjectS3ListCursorBytes {
		return plan, durableentity.ErrCorrupt
	}
	seen := map[string]bool{}
	for _, entity := range page.Entities {
		scope, err := scopeFor(entity.Entity)
		key := entityKey(entity.Entity)
		if err != nil || scope != r.Scope || seen[key] {
			return plan, durableentity.ErrCorrupt
		}
		seen[key] = true
		if entity.RetainedBytes == nil {
			plan.MissingUsage++
		} else if *entity.RetainedBytes < 0 {
			return plan, durableentity.ErrCorrupt
		} else if *entity.RetainedBytes > r.Limits.EntityBytes {
			plan.AboveEntityCap++
		}
		reserved := int64(0)
		for _, e := range r.Entities {
			if e.Key == key {
				reserved = e.Bytes
				break
			}
		}
		if reserved < r.Limits.EntityBytes {
			plan.MissingReservations++
			plan.AdditionalBytes += r.Limits.EntityBytes - reserved
		}
	}
	plan.ProjectedBytes = retainedBytes(r) + plan.AdditionalBytes
	plan.CurrentReservedBytes = retainedBytes(r)
	plan.CurrentReservations = len(r.Entities)
	plan.ByteShortfall = max(0, plan.ProjectedBytes-r.Limits.RetainedBytes)
	// Missing reservations can include an existing smaller reservation.
	newEntities := 0
	for key := range seen {
		found := false
		for _, e := range r.Entities {
			if e.Key == key {
				found = true
				break
			}
		}
		if !found {
			newEntities++
		}
	}
	plan.EntityShortfall = max(0, len(r.Entities)+newEntities-entityCapacity(r))
	if r.Schema >= 4 {
		counts := make([]int, registryShards)
		known := map[string]bool{}
		for _, e := range r.Entities {
			counts[registryIndex(e.Key)]++
			known[e.Key] = true
		}
		for key := range seen {
			if !known[key] {
				counts[registryIndex(key)]++
			}
		}
		for _, count := range counts {
			plan.EntityShortfall = max(plan.EntityShortfall, max(0, count-64))
		}
	}
	plan.NewReservations = newEntities
	plan.PageFits = plan.MissingUsage == 0 && plan.AboveEntityCap == 0 && plan.ByteShortfall == 0 && plan.EntityShortfall == 0
	return plan, nil
}
func (q *Quotas) PlanMigration(ctx context.Context, id durableentity.ID, cursor string, scanner QuotaInventoryScanner) (QuotaMigrationPlan, error) {
	if scanner == nil || len(cursor) > api.MaxObjectS3ListCursorBytes {
		return QuotaMigrationPlan{}, durableentity.ErrInvalid
	}
	r, err := q.migrationRecord(ctx, id)
	if err != nil {
		return QuotaMigrationPlan{}, err
	}
	page, err := scanner.ScanQuotaInventory(ctx, cursor, id)
	if err != nil {
		return QuotaMigrationPlan{}, err
	}
	return migrationPlan(r, page)
}

// StartMigration requires an explicit compare-and-swap replacement token. A new
// UUID fences all previous workers; existing reservations never roll back.
func (q *Quotas) StartMigration(ctx context.Context, id durableentity.ID, migrationID, expectedID string, expectedRevision uint64) (*QuotaMigration, error) {
	scope, err := scopeFor(id)
	if err != nil || !sessionUUID(migrationID) || expectedID != "" && !sessionUUID(expectedID) {
		return nil, durableentity.ErrInvalid
	}
	var result *QuotaMigration
	err = q.mutateMigration(ctx, scopeKey(scope), &scope, true, func(r *quotaRecord, now time.Time) error {
		if pendingRetirements(*r) {
			return durableentity.ErrInventoryPending
		}
		if r.Migration != nil && r.Migration.ID == migrationID {
			result = r.Migration
			return nil
		}
		for _, c := range r.PolicyChanges {
			if c.Request.MigrationID == migrationID || c.Request.ExpectedMigrationID == migrationID {
				return durableentity.ErrInvalid
			}
		}
		if r.Migration == nil {
			if expectedID != "" || expectedRevision != 0 {
				return durableentity.ErrConflict
			}
		} else if r.Migration.ID != expectedID || r.Migration.Revision != expectedRevision {
			return durableentity.ErrConflict
		}
		if len(r.Connections) != 0 {
			return durableentity.ErrBusy
		}
		if q.Limits.EntityBytes != r.Limits.EntityBytes {
			return durableentity.ErrUnsupported
		}
		r.Migration = &QuotaMigration{ID: migrationID, Revision: 1, Phase: "backfill", NotBefore: now.Add(api.MaxDurableEntityLease + api.DurableEntityInvokeTimeout), Limits: r.Limits}
		result = r.Migration
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// StepMigration scans outside the CAS, then atomically backfills that page and
// advances its checkpoint only if the migration UUID and revision still match.
// A second full pass verifies coverage. Unknown writes require status inspection.
func (q *Quotas) StepMigration(ctx context.Context, id durableentity.ID, migrationID string, revision uint64, scanner QuotaInventoryScanner) (*QuotaMigration, error) {
	if scanner == nil || !sessionUUID(migrationID) || revision == 0 {
		return nil, durableentity.ErrInvalid
	}
	r, err := q.migrationRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	m := r.Migration
	if m == nil || m.ID != migrationID || m.Revision != revision {
		return nil, durableentity.ErrConflict
	}
	if m.Phase == "complete" {
		return m, nil
	}
	if q.now().Before(m.NotBefore) {
		return nil, durableentity.ErrBusy
	}
	page, err := scanner.ScanQuotaInventory(ctx, m.Cursor, id)
	if err != nil {
		return nil, err
	}
	if page.NextCursor != "" && page.NextCursor == m.Cursor {
		return nil, durableentity.ErrCorrupt
	}
	if _, err := migrationPlan(r, page); err != nil {
		return nil, err
	}
	if r.Schema >= 2 {
		if r.Limits != q.Limits {
			return nil, ErrPolicyMismatch
		}
		updater, ok := scanner.(QuotaEntityCapUpdater)
		if !ok {
			return nil, durableentity.ErrUnsupported
		}
		for _, entity := range page.Entities {
			if err := updater.RaiseQuotaEntityLimit(ctx, entity.Entity, r.Limits.EntityBytes); err != nil {
				return nil, err
			}
		}
		// Verify fresh manifest caps and usage after ownership-fenced updates.
		page, err = scanner.ScanQuotaInventory(ctx, m.Cursor, id)
		if err != nil {
			return nil, err
		}
		if page.NextCursor != "" && page.NextCursor == m.Cursor {
			return nil, durableentity.ErrCorrupt
		}
		if _, err := migrationPlan(r, page); err != nil {
			return nil, err
		}
		for _, entity := range page.Entities {
			if entity.StorageLimitBytes != r.Limits.EntityBytes {
				return nil, durableentity.ErrInventoryPending
			}
		}
	}
	var result *QuotaMigration
	err = q.mutateMigration(ctx, scopeKey(r.Scope), &r.Scope, true, func(current *quotaRecord, _ time.Time) error {
		progress := current.Migration
		if progress == nil || progress.ID != migrationID || progress.Revision != revision {
			return durableentity.ErrConflict
		}
		if current.Limits != progress.Limits || q.Limits.EntityBytes != current.Limits.EntityBytes {
			return durableentity.ErrUnsupported
		}
		if progress.Revision == math.MaxUint64 || progress.Observed > math.MaxUint64-uint64(len(page.Entities)) {
			return durableentity.ErrLimit
		}
		plan, err := migrationPlan(*current, page)
		if err != nil {
			return err
		}
		if !plan.PageFits {
			return durableentity.ErrInventoryPending
		}
		if progress.Phase == "verify" && plan.MissingReservations != 0 {
			return durableentity.ErrInventoryPending
		}
		if progress.Phase == "backfill" {
			for _, e := range page.Entities {
				key := entityKey(e.Entity)
				found := false
				for i := range current.Entities {
					if current.Entities[i].Key == key {
						current.Entities[i].Bytes = max(current.Entities[i].Bytes, current.Limits.EntityBytes)
						found = true
						break
					}
				}
				if !found {
					current.Entities = append(current.Entities, entityReservation{key, current.Limits.EntityBytes})
				}
			}
		}
		progress.Revision++
		progress.Cursor = page.NextCursor
		progress.Observed += uint64(len(page.Entities))
		if page.NextCursor == "" {
			if progress.Phase == "backfill" {
				progress.Phase = "verify"
				progress.Observed = 0
			} else {
				progress.Phase = "complete"
			}
		}
		result = progress
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
