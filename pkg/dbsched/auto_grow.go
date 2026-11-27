package dbsched

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrStorageResizePending = errors.New("database storage resize pending")

// AutoGrowStorage grows an authorized stopped volume. The managed
// catalogue owns the resize intent and retries.
func (s *Scheduler) AutoGrowStorage(ctx context.Context, id, reservation string, target int64) (VM, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return VM{}, ErrInvalid
	}
	unlock := s.lock(id)
	defer unlock()
	vm, err := store.Get(ctx, id)
	if err != nil {
		return vm, err
	}
	if target < minVolumeBytes || target > maxVolumeBytes || target < vm.VolumeBytes {
		return vm, ErrInvalid
	}
	if vm.AlwaysOn {
		return s.scheduledAutoGrow(ctx, store, vm, reservation, target)
	}
	if vm.VolumeBytes == target && !vm.ResizePending {
		return s.withTarget(ctx, vm), nil
	}
	if vm.State != StateParked || vm.Replicated || vm.Held {
		return vm, ErrConflict
	}
	return s.growParkedVolume(ctx, store, vm, reservation, target)
}

func (s *Scheduler) growParkedVolume(ctx context.Context, store *PgStore, vm VM, reservation string, target int64) (VM, error) {
	id := vm.DatabaseID
	shape := vm.shape()
	shape.VolumeBytes = target
	if err := store.resize(ctx, id, shape, s.now(), reservation); err != nil {
		return VM{}, err
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return VM{}, err
	}
	if err := s.vmm.GrowVolume(ctx, node, id, target); err != nil {
		return VM{}, err
	}
	if err := store.FinishResize(ctx, id); err != nil {
		return VM{}, err
	}
	s.freed.signal(vm.NodeID)
	return s.Get(ctx, id)
}

// The wake gives the catalogue worker a short head start when this park
// has qualifying pressure. A blocked or slow planner cannot hold the wake
// beyond this interval. An admitted physical resize must finish first.
func waitForAutoGrow(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var wait bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM database_vms v
		JOIN managed_postgres_databases d ON d.provider_resource_id = v.database_id::text
		JOIN native_postgres_auto_grow p ON p.database_id = d.id
		JOIN native_postgres_storage_samples s ON s.database_id = v.database_id AND s.role = 'primary'
		WHERE v.database_id = $1 AND p.max_bytes > v.volume_bytes
			AND NOT v.always_on AND NOT v.replicated AND NOT v.held
			AND v.idle_parked_at = v.state_changed_at
			AND v.idle_parked_at >= now() - interval '5 seconds'
			AND s.node_id = v.node_id AND s.allocated_bytes = v.volume_bytes
			AND s.state_changed_at = v.idle_parked_from
			AND s.sampled_at >= now() - interval '5 minutes'
			AND s.low AND s.low_since IS NOT NULL AND s.sampled_at - greatest(s.low_since, p.updated_at) >= interval '1 minute'
			AND (p.checked_at IS NULL OR p.checked_at < v.idle_parked_at OR p.reason = 'resize_queued')
	)`, id).Scan(&wait)
	return wait, err
}

func verifyAutoGrowStorage(ctx context.Context, tx sqlc.DBTX, vm VM, reservation string, target int64) error {
	request, err := pgUUID(reservation)
	if err != nil {
		return ErrInvalid
	}
	if vm.AlwaysOn {
		return verifyScheduledGrowStorage(ctx, tx, vm, request, target)
	}
	// A physical resize already admitted must be completed even if the
	// owner subsequently disables the policy. Both paths pin the journal.
	var ceiling int64
	err = tx.QueryRow(ctx, `SELECT p.max_bytes
		FROM native_postgres_auto_grow p
		JOIN managed_postgres_resizes r ON r.database_id = p.database_id
		JOIN database_vms v ON v.database_id::text = r.provider_resource_id
		WHERE r.id = $1 AND r.state = 'pending' AND r.automatic
			AND r.target_storage_limit_bytes = $2 AND v.database_id = $3
			AND v.state = 'parked' AND NOT v.always_on AND NOT v.replicated AND NOT v.held
			AND ((v.resize_pending AND v.volume_bytes = $2)
				OR (NOT v.resize_pending AND p.max_bytes >= $2
					AND v.idle_parked_at = v.state_changed_at AND v.idle_parked_at = r.auto_grow_idle_at
					AND v.volume_bytes = (r.source_spec->>'StorageLimitBytes')::bigint))
		FOR SHARE OF p`, request, target, vm.DatabaseID).Scan(&ceiling)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}
