package dbsched

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmaintenance"
)

// vmmd allows a 90-second shutdown before forcing the VM to exit.
const maintenanceStopTimeout = 2 * time.Minute

type baseRefresh struct {
	DatabaseID        string
	SourceAt          time.Time
	StartedAt         time.Time
	RecoverAt         time.Time
	NodeID            string
	Window            dbmaintenance.Interval
	QueryOnly         bool
	StandbyNode       string
	Phase             string
	StandbyRestarted  bool
	HandoffStarted    bool
	PromotionStarted  bool
	HandoffReservedMB int64
	Image             string
}

type baseRefreshObservation struct {
	vm    VM
	stale bool
}

func (s *PgStore) recordBaseRefreshObservations(ctx context.Context, observations []baseRefreshObservation) error {
	if len(observations) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, 0, len(observations))
	epochs := make([]time.Time, 0, len(observations))
	stale := make([]bool, 0, len(observations))
	for _, obs := range observations {
		ids = append(ids, mustUUID(obs.vm.DatabaseID))
		epochs = append(epochs, obs.vm.StateChangedAt)
		stale = append(stale, obs.stale)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE native_postgres_maintenance_windows p
		SET stale_since = CASE WHEN obs.stale THEN clock_timestamp() END
		FROM unnest($1::uuid[], $2::timestamptz[], $3::bool[]) AS obs(id, epoch, stale)
		JOIN database_vms v ON v.database_id = obs.id
		WHERE p.database_id = obs.id AND v.state = 'running' AND v.state_changed_at = obs.epoch
		  AND (p.stale_since IS NOT NULL) <> obs.stale`, ids, epochs, stale)
	return err
}

// beginBaseRefresh serializes policy writes, legacy idle restarts and
// scheduled refreshes on the VM row. A nil result with a successful
// transition means the existing idle-only policy applies.
func (s *PgStore) beginBaseRefresh(ctx context.Context, vm VM, idleCutoff time.Time, recoveryDelay time.Duration, resp dbguest.Response) (*baseRefresh, bool, error) {
	var refresh *baseRefresh
	_, err := inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		var eligible bool
		err := tx.QueryRow(ctx, `
			SELECT state = 'running' AND always_on AND NOT held AND NOT replicated AND NOT resize_pending
				AND standby_node IS NULL AND state_changed_at = $2 AND node_id = $3
			FROM database_vms WHERE database_id = $1 FOR UPDATE`,
			mustUUID(vm.DatabaseID), vm.StateChangedAt, vm.NodeID).Scan(&eligible)
		if err != nil {
			return VM{}, err
		}
		if !eligible {
			return VM{}, ErrConflict
		}
		var active bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM native_postgres_base_refreshes WHERE database_id = $1 AND finished_at IS NULL)`,
			mustUUID(vm.DatabaseID)).Scan(&active); err != nil {
			return VM{}, err
		}
		if active {
			return VM{}, ErrConflict
		}

		var window dbmaintenance.Window
		var now time.Time
		err = tx.QueryRow(ctx, `
			SELECT weekday, start_minute, duration_minutes, clock_timestamp()
			FROM native_postgres_maintenance_windows WHERE database_id = $1`, mustUUID(vm.DatabaseID)).
			Scan(&window.Weekday, &window.StartMinute, &window.DurationMinutes, &now)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if !resp.Stale {
				return VM{}, ErrConflict
			}
			// Touch does not advance the runtime epoch.
			var idle bool
			err = tx.QueryRow(ctx, `SELECT last_active_at IS NULL OR last_active_at <= $2
				FROM database_vms WHERE database_id = $1`, mustUUID(vm.DatabaseID), idleCutoff).Scan(&idle)
			if err != nil {
				return VM{}, err
			}
			if !idle {
				return VM{}, ErrConflict
			}
		case err != nil:
			return VM{}, err
		default:
			interval := window.ActiveOrNext(now)
			if !interval.Contains(now) {
				return VM{}, ErrConflict
			}
			var ready bool
			err = tx.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM managed_postgres_databases
					WHERE provider_resource_id = $1 AND state = 'ready'
					  AND NOT scale_to_zero AND availability = 'single_zone' FOR SHARE)`, vm.DatabaseID).Scan(&ready)
			if err != nil {
				return VM{}, err
			}
			if !ready {
				return VM{}, ErrConflict
			}
			if !resp.Stale {
				var enabled bool
				err := tx.QueryRow(ctx, `SELECT enabled FROM native_postgres_query_settings
					WHERE database_id=$1 FOR SHARE`, mustUUID(vm.DatabaseID)).Scan(&enabled)
				if errors.Is(err, pgx.ErrNoRows) || err == nil && (!resp.QueryInsightsSupported || resp.QueryInsightsConfigured == enabled) {
					return VM{}, ErrConflict
				}
				if err != nil {
					return VM{}, err
				}
			}
			// Let the catalog reserve qualifying storage growth first. Its
			// stop and wake can apply both changes in this window.
			wait, err := scheduledGrowthDue(ctx, tx, vm, interval.Start)
			if err != nil {
				return VM{}, err
			}
			if wait {
				return VM{}, ErrConflict
			}
			refresh = &baseRefresh{DatabaseID: vm.DatabaseID, NodeID: vm.NodeID, SourceAt: vm.StateChangedAt, Window: interval, QueryOnly: !resp.Stale}
			err = tx.QueryRow(ctx, `
				INSERT INTO native_postgres_base_refreshes
					(database_id, window_start, window_end, source_running_at, node_id, started_at, recover_after, refresh_base)
				SELECT $1, $2, $3, $4, $5, transaction_timestamp(), $6, $7
				WHERE clock_timestamp() >= $2 AND clock_timestamp() < $3
				ON CONFLICT DO NOTHING RETURNING started_at`, mustUUID(vm.DatabaseID), interval.Start, interval.End,
				vm.StateChangedAt, vm.NodeID, now.Add(recoveryDelay), resp.Stale).Scan(&refresh.StartedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return VM{}, ErrConflict
			}
			if err != nil {
				return VM{}, err
			}
		}
		return VM{}, s.transition(ctx, tx, vm.DatabaseID, []State{StateRunning}, StateParking, TransitionOptions{})
	})
	if errors.Is(err, ErrConflict) || errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	return refresh, err == nil, err
}

// updateBaseRefresh compares the runtime snapshot used for the guest
// observation before adopting it. Only the original parking incarnation
// or a newer waking/running incarnation can settle the attempt.
func (s *PgStore) updateBaseRefresh(ctx context.Context, refresh baseRefresh, vm VM, to State, outcome string) error {
	_, err := inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		var same bool
		err := tx.QueryRow(ctx, `SELECT state = $2 AND state_changed_at = $3 AND node_id = $4
			FROM database_vms WHERE database_id = $1 FOR UPDATE`, mustUUID(vm.DatabaseID), string(vm.State),
			vm.StateChangedAt, vm.NodeID).Scan(&same)
		if err != nil || !same {
			return VM{}, err
		}
		var pending bool
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM native_postgres_base_refreshes
			WHERE database_id = $1 AND window_start = $2 AND finished_at IS NULL)`,
			mustUUID(vm.DatabaseID), refresh.Window.Start).Scan(&pending)
		if err != nil || !pending {
			return VM{}, err
		}
		if to != "" && to != vm.State {
			if err := s.transition(ctx, tx, vm.DatabaseID, []State{vm.State}, to, TransitionOptions{}); err != nil {
				return VM{}, err
			}
		}
		if outcome == "" {
			return VM{}, nil
		}
		_, err = tx.Exec(ctx, `UPDATE native_postgres_base_refreshes SET finished_at = clock_timestamp(), outcome = $3
			WHERE database_id = $1 AND window_start = $2`, mustUUID(vm.DatabaseID), refresh.Window.Start, outcome)
		if err == nil && outcome == "succeeded" {
			_, err = tx.Exec(ctx, `UPDATE native_postgres_maintenance_windows SET stale_since = NULL WHERE database_id = $1`, mustUUID(vm.DatabaseID))
		}
		return VM{}, err
	})
	return err
}

func (s *PgStore) activeBaseRefreshes(ctx context.Context) (map[string]baseRefresh, error) {
	rows, err := s.pool.Query(ctx, `SELECT database_id::text, source_running_at, started_at, recover_after, node_id, window_start, window_end, NOT refresh_base AND resize_id IS NULL,
		       COALESCE(rolling_standby_node, ''), phase, standby_restarted
		FROM native_postgres_base_refreshes WHERE finished_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := make(map[string]baseRefresh)
	for rows.Next() {
		var refresh baseRefresh
		if err := rows.Scan(&refresh.DatabaseID, &refresh.SourceAt, &refresh.StartedAt, &refresh.RecoverAt, &refresh.NodeID,
			&refresh.Window.Start, &refresh.Window.End, &refresh.QueryOnly, &refresh.StandbyNode, &refresh.Phase, &refresh.StandbyRestarted); err != nil {
			return nil, err
		}
		active[refresh.DatabaseID] = refresh
	}
	return active, rows.Err()
}

// restartStaleSingle reports separately whether a stop was attempted and
// whether the refreshed database was verified online.
func (s *Scheduler) restartStaleSingle(ctx context.Context, store *PgStore, vm VM, cutoff time.Time) (bool, bool, error) {
	unlock, ok := s.tryLock(vm.DatabaseID)
	if !ok {
		return false, false, nil
	}
	defer unlock()
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return false, false, err
	}
	importProbe, cancel := context.WithTimeout(ctx, storageProbeTimeout)
	importStatus, err := s.guestImport(importProbe, vm)
	cancel()
	if err != nil || importStatus.State == dbguest.ImportRunning {
		return false, false, err
	}
	probe, cancel := context.WithTimeout(ctx, storageProbeTimeout)
	resp, err := s.vmm.Control(probe, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpStatus})
	cancel()
	if err != nil || !refreshReady(resp) || (!resp.Stale && !resp.QueryInsightsSupported) {
		return false, false, err
	}
	if err := s.refusal(ctx, vm); err != nil {
		return false, false, nil
	}
	refresh, admitted, err := store.beginBaseRefresh(ctx, vm, cutoff, maintenanceStopTimeout+s.cfg.ReadyTimeout+time.Minute, resp)
	if err != nil || !admitted {
		return false, false, err
	}
	if refresh != nil && !refresh.Window.Contains(s.now()) {
		vm.State, vm.StateChangedAt = StateParking, refresh.StartedAt
		return false, false, store.updateBaseRefresh(ctx, *refresh, vm, StateRunning, "window_closed")
	}

	stop, cancel := context.WithTimeout(ctx, maintenanceStopTimeout)
	err = s.vmm.Stop(stop, node, vm.DatabaseID)
	cancel()
	if err != nil {
		if refresh == nil {
			_ = store.Transition(context.WithoutCancel(ctx), vm.DatabaseID, []State{StateParking}, StateFailed, TransitionOptions{LastError: errCodeStop})
		}
		return true, false, err
	}
	s.freed.signal(vm.NodeID)
	if err := store.Transition(ctx, vm.DatabaseID, []State{StateParking}, StateParked, TransitionOptions{}); err != nil {
		return true, false, err
	}
	running, err := s.wakeLocked(ctx, vm.DatabaseID)
	if err != nil || refresh == nil {
		return true, err == nil, err
	}
	completed, err := s.observeBaseRefresh(ctx, store, *refresh, running)
	return true, completed, err
}

func refreshReady(resp dbguest.Response) bool {
	return resp.State == dbguest.StateReady && resp.Error == ""
}

func (s *Scheduler) observeBaseRefresh(ctx context.Context, store *PgStore, refresh baseRefresh, vm VM) (bool, error) {
	if vm.NodeID != refresh.NodeID || vm.State == StateFailed {
		return false, store.updateBaseRefresh(ctx, refresh, vm, "", "runtime_failed")
	}
	if vm.State == StateParked {
		if vm.LastError == errCodeBoot {
			return false, store.updateBaseRefresh(ctx, refresh, vm, "", "runtime_failed")
		}
		// KeepWarm or a client wake can finish the already-admitted restart.
		return false, nil
	}
	if vm.State != StateParking && vm.State != StateWaking && vm.State != StateRunning {
		return false, store.updateBaseRefresh(ctx, refresh, vm, "", "interrupted")
	}
	node, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return false, err
	}
	probe, cancel := context.WithTimeout(ctx, storageProbeTimeout)
	resp, err := s.vmm.Control(probe, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpStatus})
	cancel()
	switch vm.State {
	case StateParking:
		if !vm.StateChangedAt.Equal(refresh.StartedAt) {
			return false, store.updateBaseRefresh(ctx, refresh, vm, "", "interrupted")
		}
		if errors.Is(err, ErrVMNotRunning) {
			s.freed.signal(vm.NodeID)
			return false, store.updateBaseRefresh(ctx, refresh, vm, StateParked, "")
		}
		if err == nil && refreshReady(resp) {
			// The former guest is still available. Do not replay a stop.
			return false, store.updateBaseRefresh(ctx, refresh, vm, StateRunning, "interrupted")
		}
	case StateWaking, StateRunning:
		if vm.State == StateWaking && errors.Is(err, ErrVMNotRunning) {
			s.freed.signal(vm.NodeID)
			return false, store.updateBaseRefresh(ctx, refresh, vm, StateParked, "")
		}
		if err == nil && refreshReady(resp) && vm.StateChangedAt.After(refresh.SourceAt) {
			s.freed.signal(vm.NodeID)
			outcome := "succeeded"
			outdated := resp.Stale
			if refresh.QueryOnly {
				enabled, err := store.queryInsightsEnabled(ctx, vm.DatabaseID)
				if err != nil {
					return false, err
				}
				outdated = !resp.QueryInsightsSupported || enabled != resp.QueryInsightsConfigured
			}
			if outdated {
				outcome = "still_stale"
			}
			return !outdated, store.updateBaseRefresh(ctx, refresh, vm, StateRunning, outcome)
		}
	}
	return false, err
}

func (s *Scheduler) settleBaseRefreshes(ctx context.Context, store *PgStore, active map[string]baseRefresh) {
	for id, refresh := range active {
		if refresh.Phase != "" {
			s.runRollingRefresh(ctx, store, id)
			continue
		}
		unlock, ok := s.tryLock(id)
		if !ok {
			continue
		}
		vm, err := store.Get(ctx, id)
		if err == nil && !s.now().Before(refresh.RecoverAt) &&
			(vm.State != StateWaking || !s.now().Before(vm.StateChangedAt.Add(s.cfg.ReadyTimeout+time.Minute))) {
			_, err = s.observeBaseRefresh(ctx, store, refresh, vm)
		}
		unlock()
		if err != nil && !errors.Is(err, ErrNotFound) {
			s.log.Warn("dbsched: could not settle base refresh", "database_id", id, "err", err)
		}
	}
}
