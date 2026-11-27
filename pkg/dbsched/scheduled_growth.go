package dbsched

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// scheduledGrowthDue lets the catalog check the customer's allowance
// before a base-only refresh consumes this window.
func scheduledGrowthDue(ctx context.Context, tx pgx.Tx, vm VM, windowStart time.Time) (bool, error) {
	var due bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM native_postgres_auto_grow p
		JOIN managed_postgres_databases d ON d.id = p.database_id
		JOIN native_postgres_storage_samples s ON s.database_id = $1 AND s.role = 'primary'
		WHERE d.provider_resource_id = $2 AND d.environment_clone_operation_id IS NULL
			AND d.preview_source_database_id IS NULL AND d.data_resource_id IS NOT NULL
			AND p.max_bytes > $3 AND (p.checked_at IS NULL OR p.checked_at < $4)
			AND d.storage_limit_bytes = $3 AND s.allocated_bytes = $3
			AND s.node_id = $5 AND s.state_changed_at = $6
			AND s.low AND s.low_since IS NOT NULL AND s.sampled_at >= clock_timestamp() - interval '5 minutes'
			AND s.sampled_at - greatest(s.low_since, p.updated_at) >= interval '1 minute')`,
		mustUUID(vm.DatabaseID), vm.DatabaseID, vm.VolumeBytes, windowStart, vm.NodeID, vm.StateChangedAt).Scan(&due)
	return due, err
}

// The node and VM locks serialize the capacity reservation and state
// transition with placement, wake and other maintenance.
func (s *PgStore) beginScheduledGrow(ctx context.Context, vm VM, request pgtype.UUID, target int64, stale bool, recoveryDelay time.Duration) (baseRefresh, error) {
	var refresh baseRefresh
	_, err := inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		row, err := s.q.LockDatabaseNode(ctx, tx, vm.NodeID)
		if err != nil {
			return VM{}, err
		}
		var eligible bool
		err = tx.QueryRow(ctx, `SELECT state = 'running' AND always_on AND NOT held AND NOT replicated
			AND NOT resize_pending AND standby_node IS NULL AND node_id = $2 AND state_changed_at = $3 AND volume_bytes = $4
			FROM database_vms WHERE database_id = $1 FOR UPDATE`, mustUUID(vm.DatabaseID), vm.NodeID,
			vm.StateChangedAt, vm.VolumeBytes).Scan(&eligible)
		if err != nil {
			return VM{}, err
		}
		if !eligible {
			return VM{}, ErrConflict
		}
		refresh.DatabaseID, refresh.NodeID, refresh.SourceAt = vm.DatabaseID, vm.NodeID, vm.StateChangedAt
		err = tx.QueryRow(ctx, `
			SELECT g.window_start, g.window_end
			FROM native_postgres_scheduled_growth g
			JOIN managed_postgres_resizes r ON r.id = g.resize_id
			JOIN native_postgres_auto_grow p ON p.database_id = r.database_id
			JOIN native_postgres_maintenance_windows w ON w.database_id = $4
			JOIN native_postgres_storage_samples s ON s.database_id = w.database_id AND s.role = 'primary'
			WHERE r.id = $1 AND r.state = 'pending' AND r.automatic AND r.provider_resource_id = $2
				AND r.target_storage_limit_bytes = $3 AND p.max_bytes >= $3 AND p.updated_at = g.policy_at
				AND (r.source_spec->>'StorageLimitBytes')::bigint = $7
				AND g.source_running_at = $5 AND s.state_changed_at = $5 AND s.node_id = $6 AND s.allocated_bytes = $7
				AND s.low AND s.low_since IS NOT NULL AND s.sampled_at >= clock_timestamp() - interval '5 minutes'
				AND s.sampled_at - greatest(s.low_since, p.updated_at) >= interval '1 minute'
				AND native_postgres_open_window(w.weekday, w.start_minute, w.duration_minutes, clock_timestamp()) = g.window_start
				AND g.window_end = g.window_start + make_interval(mins => w.duration_minutes)
			FOR SHARE OF r, p`, request, vm.DatabaseID, target, mustUUID(vm.DatabaseID), vm.StateChangedAt,
			vm.NodeID, vm.VolumeBytes).Scan(&refresh.Window.Start, &refresh.Window.End)
		if err != nil {
			return VM{}, err
		}
		node := Node{ID: row.ID, Lifecycle: row.Lifecycle, AdmissionCeilingMB: int(row.AdmissionCeilingMb), VolumeCapacityBytes: row.VolumeCapacityBytes}
		usage, err := s.usage(ctx, tx)
		if err != nil {
			return VM{}, err
		}
		shape := vm.shape()
		shape.VolumeBytes = target
		if err := resizeFits(node, usage[node.ID], vm, shape); err != nil {
			return VM{}, err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO native_postgres_base_refreshes (database_id, window_start, window_end,
				source_running_at, node_id, started_at, recover_after, resize_id, target_volume_bytes, refresh_base)
			SELECT $1, $2, $3, $4, $5, transaction_timestamp(), clock_timestamp() + make_interval(secs => $6), $7, $8, $9
			WHERE clock_timestamp() >= $2 AND clock_timestamp() < $3
			ON CONFLICT DO NOTHING RETURNING started_at, recover_after`, mustUUID(vm.DatabaseID),
			refresh.Window.Start, refresh.Window.End, vm.StateChangedAt, vm.NodeID, recoveryDelay.Seconds(),
			request, target, stale).Scan(&refresh.StartedAt, &refresh.RecoverAt)
		if err != nil {
			return VM{}, err
		}
		return VM{}, s.transition(ctx, tx, vm.DatabaseID, []State{StateRunning}, StateParking, TransitionOptions{})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrConflict
	}
	return refresh, err
}

func (s *PgStore) scheduledGrowAttempt(ctx context.Context, id string, request pgtype.UUID, target int64) (baseRefresh, bool, error) {
	refresh := baseRefresh{DatabaseID: id}
	var pending bool
	err := s.pool.QueryRow(ctx, `SELECT source_running_at, started_at, recover_after, node_id, window_start, window_end, finished_at IS NULL
		FROM native_postgres_base_refreshes WHERE database_id = $1 AND resize_id = $2 AND target_volume_bytes = $3`, mustUUID(id), request, target).
		Scan(&refresh.SourceAt, &refresh.StartedAt, &refresh.RecoverAt, &refresh.NodeID, &refresh.Window.Start, &refresh.Window.End, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return refresh, false, nil
	}
	return refresh, pending, err
}

func verifyScheduledGrowStorage(ctx context.Context, tx sqlc.DBTX, vm VM, request pgtype.UUID, target int64) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM native_postgres_base_refreshes a JOIN managed_postgres_resizes r ON r.id = a.resize_id
		WHERE a.database_id = $1 AND a.resize_id = $2 AND a.finished_at IS NULL
			AND a.node_id = $3 AND a.target_volume_bytes = $4 AND r.state = 'pending'
			AND r.automatic AND r.target_storage_limit_bytes = $4 AND r.provider_resource_id = $5
			AND ($6 = $4 OR $6 = (r.source_spec->>'StorageLimitBytes')::bigint))`,
		mustUUID(vm.DatabaseID), request, vm.NodeID, target, vm.DatabaseID, vm.VolumeBytes).Scan(&exists)
	if err == nil && !exists {
		return ErrConflict
	}
	return err
}

func (s *Scheduler) scheduledAutoGrow(ctx context.Context, store *PgStore, vm VM, reservation string, target int64) (VM, error) {
	request, err := pgUUID(reservation)
	if err != nil {
		return vm, ErrInvalid
	}
	refresh, pending, err := store.scheduledGrowAttempt(ctx, vm.DatabaseID, request, target)
	if err != nil {
		return vm, err
	}
	if refresh.StartedAt.IsZero() {
		if vm.State != StateRunning || vm.Held || vm.Replicated || vm.ResizePending || s.refusal(ctx, vm) != nil {
			return vm, ErrConflict
		}
		refresh, err = s.stopForScheduledGrow(ctx, store, vm, request, target)
		if err != nil {
			return vm, err
		}
		pending = true
	} else if pending && vm.State == StateParking {
		if s.now().Before(refresh.RecoverAt) {
			return vm, ErrStorageResizePending
		}
		if _, err := s.observeBaseRefresh(ctx, store, refresh, vm); err != nil {
			return vm, err
		}
	}
	vm, err = store.Get(ctx, vm.DatabaseID)
	if err != nil {
		return vm, err
	}
	if vm.State == StateRunning && vm.VolumeBytes != target {
		return vm, ErrConflict
	}
	if vm.VolumeBytes != target || vm.ResizePending {
		if !pending {
			return vm, ErrConflict
		}
		if vm.State != StateParked {
			return vm, ErrStorageResizePending
		}
		grown, err := s.growParkedVolume(ctx, store, vm, reservation, target)
		if err != nil {
			// The stop is already admitted. Keep its reservation until an
			// uncertain or interrupted physical grow can be completed.
			s.log.Warn("dbsched: scheduled storage growth will retry", "database_id", vm.DatabaseID, "err", err)
			return vm, ErrStorageResizePending
		}
		vm = grown
	}
	if vm.State == StateParked && s.refusal(ctx, vm) != nil {
		if pending {
			err = store.updateBaseRefresh(ctx, refresh, vm, "", "interrupted")
		}
		return s.withTarget(ctx, vm), err
	}
	if vm.State != StateRunning {
		vm, err = s.wakeLocked(ctx, vm.DatabaseID)
		if err != nil {
			return vm, ErrStorageResizePending
		}
	}
	resp, ok := s.sampleStatus(ctx, vm.NodeID, vm.DatabaseID)
	if !ok || !refreshReady(resp) || resp.Storage == nil {
		return vm, ErrStorageResizePending
	}
	s.recordStorageSamples(ctx, store, appendVolumeSample(nil, vm, "primary", vm.NodeID, resp.Storage, s.now()))
	if pending {
		outcome := "succeeded"
		if resp.Stale {
			outcome = "still_stale"
		}
		if err := store.updateBaseRefresh(ctx, refresh, vm, StateRunning, outcome); err != nil {
			return vm, err
		}
	}
	return s.withTarget(ctx, vm), nil
}

func (s *Scheduler) stopForScheduledGrow(ctx context.Context, store *PgStore, vm VM, request pgtype.UUID, target int64) (baseRefresh, error) {
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return baseRefresh{}, err
	}
	probe, cancel := context.WithTimeout(ctx, storageProbeTimeout)
	importing, err := s.guestImport(probe, vm)
	cancel()
	if err != nil {
		return baseRefresh{}, err
	}
	if importing.State == dbguest.ImportRunning {
		return baseRefresh{}, ErrConflict
	}
	resp, ok := s.sampleStatus(ctx, vm.NodeID, vm.DatabaseID)
	if !ok || !refreshReady(resp) || resp.Storage == nil || !resp.Storage.Low {
		return baseRefresh{}, ErrConflict
	}
	refresh, err := store.beginScheduledGrow(ctx, vm, request, target, resp.Stale, maintenanceStopTimeout+s.cfg.ReadyTimeout+time.Minute)
	if err != nil {
		return refresh, err
	}
	if !refresh.Window.Contains(s.now()) {
		vm.State, vm.StateChangedAt = StateParking, refresh.StartedAt
		if err := store.updateBaseRefresh(ctx, refresh, vm, StateRunning, "window_closed"); err != nil {
			s.log.Warn("dbsched: scheduled grow cancellation will retry", "database_id", vm.DatabaseID, "err", err)
			return refresh, ErrStorageResizePending
		}
		return refresh, ErrConflict
	}
	stop, cancel := context.WithTimeout(ctx, maintenanceStopTimeout)
	err = s.vmm.Stop(stop, node, vm.DatabaseID)
	cancel()
	if err == nil {
		s.freed.signal(vm.NodeID)
		err = store.Transition(ctx, vm.DatabaseID, []State{StateParking}, StateParked, TransitionOptions{})
	}
	if err != nil {
		// A stop can have succeeded even if its response or state update
		// was lost. Recover from the observed VM before releasing capacity.
		s.log.Warn("dbsched: scheduled grow stop will reconcile", "database_id", vm.DatabaseID, "err", err)
		return refresh, ErrStorageResizePending
	}
	return refresh, nil
}
