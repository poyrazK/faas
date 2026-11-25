package dbsched

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmaintenance"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	refreshingStandby   = "refreshing_standby"
	switchingOver       = "switching_over"
	restoringRedundancy = "restoring_redundancy"
)

func (s *PgStore) rollingPolicies(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.database_id::text FROM native_postgres_maintenance_windows w
		JOIN database_vms v USING (database_id) WHERE v.replicated
		UNION SELECT database_id::text FROM native_postgres_base_refreshes
			WHERE rolling_standby_node IS NOT NULL AND finished_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func rollingWindow(ctx context.Context, db sqlc.DBTX, id string) (dbmaintenance.Interval, time.Time, error) {
	var window dbmaintenance.Window
	var now time.Time
	err := db.QueryRow(ctx, `
		SELECT weekday, start_minute, duration_minutes, clock_timestamp()
		FROM native_postgres_maintenance_windows WHERE database_id = $1`, mustUUID(id)).
		Scan(&window.Weekday, &window.StartMinute, &window.DurationMinutes, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbmaintenance.Interval{}, now, nil
	}
	return window.ActiveOrNext(now), now, err
}

// The advisory lock covers VM operations whose catalog and guest changes
// span transactions, such as withdrawing a synchronous standby. A rolling
// refresh must not arm a handoff while one of those operations is in flight.
func tryStandbyOperation(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var locked bool
	err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('dbsched:standby:' || $1, 0))`, id).Scan(&locked)
	return locked, err
}

func (s *Scheduler) lockStandbyOperation(ctx context.Context, id string) (func(), error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return func() {}, nil
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	release := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }
	locked, err := tryStandbyOperation(ctx, tx, id)
	if err != nil || !locked {
		release()
		if err == nil {
			err = ErrConflict
		}
		return nil, err
	}
	return release, nil
}

// lockRollingVM serializes a destructive step with another scheduler and
// the normal runtime state transitions. The worker holds the lock until
// the step's catalog result is committed.
func (s *PgStore) lockRollingVM(ctx context.Context, tx pgx.Tx, id string) (VM, error) {
	var key string
	err := tx.QueryRow(ctx, `
		SELECT database_id::text FROM database_vms WHERE database_id = $1 FOR UPDATE SKIP LOCKED`, mustUUID(id)).Scan(&key)
	if err != nil {
		return VM{}, err
	}
	return s.get(ctx, tx, key)
}

func rollingCatalogReady(ctx context.Context, db sqlc.DBTX, id string) (bool, error) {
	var ready bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM managed_postgres_databases WHERE provider_resource_id = $1
			AND state = 'ready' AND observed_generation = desired_generation
			AND NOT scale_to_zero AND availability = 'single_zone' FOR SHARE)`, id).Scan(&ready)
	return ready, err
}

func (s *PgStore) beginRollingRefresh(ctx context.Context, sampled VM) (bool, error) {
	var admitted bool
	_, err := inTx(ctx, s.pool, func(tx pgx.Tx) (VM, error) {
		vm, err := s.lockRollingVM(ctx, tx, sampled.DatabaseID)
		if errors.Is(err, pgx.ErrNoRows) {
			return VM{}, nil
		}
		if err != nil {
			return VM{}, err
		}
		if !vm.AlwaysOn || vm.Held || vm.State != StateRunning || !switchable(vm) ||
			vm.NodeID != sampled.NodeID || vm.StandbyNode != sampled.StandbyNode || !vm.StateChangedAt.Equal(sampled.StateChangedAt) {
			return VM{}, nil
		}
		window, now, err := rollingWindow(ctx, tx, vm.DatabaseID)
		if err != nil || !window.Contains(now) {
			return VM{}, err
		}
		ready, err := rollingCatalogReady(ctx, tx, vm.DatabaseID)
		if err != nil || !ready {
			return VM{}, err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO native_postgres_base_refreshes
				(database_id, window_start, window_end, source_running_at, node_id, started_at, recover_after, rolling_standby_node, phase)
			SELECT $1, $2, $3, $4, $5, clock_timestamp(), $3, $6, $7 WHERE clock_timestamp() < $3
			ON CONFLICT DO NOTHING`, mustUUID(vm.DatabaseID), window.Start, window.End,
			vm.StateChangedAt, vm.NodeID, vm.StandbyNode, refreshingStandby)
		admitted = tag.RowsAffected() == 1
		return VM{}, err
	})
	return admitted, err
}

// rollingSwitchArmed fences ordinary parking and failover while the journal
// owns a handoff. Its original primary must stay unroutable until the
// promotion result is accounted for.
func rollingSwitchArmed(ctx context.Context, db sqlc.DBTX, id string) (bool, error) {
	var armed bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM native_postgres_base_refreshes WHERE database_id = $1
			AND finished_at IS NULL AND phase = 'switching_over')`, mustUUID(id)).Scan(&armed)
	return armed, err
}

// Protect the planned resume from a monitor that sampled the old walsender
// while the standby was being stopped. Failed attaches still use normal HA
// recovery and the usual timeout.
func rollingStandbyResume(ctx context.Context, db sqlc.DBTX, vm VM) (bool, error) {
	var pending bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM native_postgres_base_refreshes WHERE database_id = $1
			AND node_id = $2 AND rolling_standby_node = $3 AND finished_at IS NULL
			AND phase = 'refreshing_standby' AND standby_restarted AND clock_timestamp() < recover_after)`,
		mustUUID(vm.DatabaseID), vm.NodeID, vm.StandbyNode).Scan(&pending)
	return pending, err
}

func saveRollingPhase(ctx context.Context, tx pgx.Tx, refresh baseRefresh, phase, reason string) error {
	_, err := tx.Exec(ctx, `UPDATE native_postgres_base_refreshes SET phase = $3, reason = $4
		WHERE database_id = $1 AND window_start = $2`, mustUUID(refresh.DatabaseID), refresh.Window.Start, phase, reason)
	return err
}

func finishRollingRefresh(ctx context.Context, tx pgx.Tx, refresh baseRefresh, outcome, reason string) error {
	_, err := tx.Exec(ctx, `UPDATE native_postgres_base_refreshes SET finished_at = clock_timestamp(), outcome = $3, reason = $4
		WHERE database_id = $1 AND window_start = $2`, mustUUID(refresh.DatabaseID), refresh.Window.Start, outcome, reason)
	if err == nil && outcome == "succeeded" {
		_, err = tx.Exec(ctx, `UPDATE native_postgres_maintenance_windows SET stale_since = NULL WHERE database_id = $1`, mustUUID(refresh.DatabaseID))
	}
	return err
}

func (s *Scheduler) advanceRollingRefresh(ctx context.Context, store *PgStore, id string) (bool, error) {
	unlock, ok := s.tryLock(id)
	if !ok {
		return false, nil
	}
	defer unlock()
	nodes, err := store.Nodes(ctx)
	if err != nil {
		return false, err
	}
	byID := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}

	var progressed, attach bool
	_, err = inTx(ctx, store.pool, func(tx pgx.Tx) (VM, error) {
		locked, err := tryStandbyOperation(ctx, tx, id)
		if err != nil || !locked {
			return VM{}, err
		}
		vm, err := store.lockRollingVM(ctx, tx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return VM{}, nil
		}
		if err != nil {
			return VM{}, err
		}
		var r baseRefresh
		r.DatabaseID = id
		err = tx.QueryRow(ctx, `
			SELECT node_id, rolling_standby_node, source_running_at, window_start, window_end,
				recover_after, phase, standby_restarted, handoff_started, COALESCE(rolling_image, '')
			FROM native_postgres_base_refreshes WHERE database_id = $1
				AND rolling_standby_node IS NOT NULL AND finished_at IS NULL`, mustUUID(id)).
			Scan(&r.NodeID, &r.StandbyNode, &r.SourceAt, &r.Window.Start, &r.Window.End, &r.RecoverAt, &r.Phase, &r.StandbyRestarted, &r.HandoffStarted, &r.Image)
		if errors.Is(err, pgx.ErrNoRows) {
			return VM{}, nil
		}
		if err != nil {
			return VM{}, err
		}

		currentPrimary, currentStandby := byID[vm.NodeID], byID[vm.StandbyNode]
		switch r.Phase {
		case refreshingStandby:
			progressed, attach, err = s.refreshRollingStandby(ctx, store, tx, r, vm, currentPrimary, currentStandby)
		case switchingOver:
			if r.HandoffStarted {
				attach, err = s.handoffRollingRefresh(ctx, store, tx, r, vm, currentPrimary, currentStandby)
			} else {
				progressed, err = s.prepareRollingHandoff(ctx, store, tx, r, vm, currentPrimary, currentStandby)
			}
		case restoringRedundancy:
			err = s.finishRollingRedundancy(ctx, tx, r, vm, currentPrimary, currentStandby)
		}
		return vm, err
	})
	if err == nil && attach {
		s.kickStandby(ctx, id)
	}
	return progressed, err
}

func (s *Scheduler) refreshRollingStandby(ctx context.Context, store *PgStore, tx pgx.Tx, r baseRefresh, vm VM, primary, standby Node) (armed, attach bool, err error) {
	id := vm.DatabaseID
	if vm.State != StateRunning || vm.NodeID != r.NodeID || !vm.StateChangedAt.Equal(r.SourceAt) ||
		vm.StandbyNode != r.StandbyNode || !vm.AlwaysOn || vm.Held || vm.ResizePending {
		return false, false, finishRollingRefresh(ctx, tx, r, "interrupted", "database_placement_changed")
	}
	window, now, err := rollingWindow(ctx, tx, id)
	if err != nil {
		return false, false, err
	}
	if !window.Contains(now) || !window.Start.Equal(r.Window.Start) {
		return false, false, finishRollingRefresh(ctx, tx, r, "window_closed", "standby_validation_incomplete")
	}
	ready, err := rollingCatalogReady(ctx, tx, id)
	if err != nil {
		return false, false, err
	}
	if !ready {
		return false, false, finishRollingRefresh(ctx, tx, r, "interrupted", "database_operation_pending")
	}
	if primary.Lifecycle != NodeActive || standby.Lifecycle != NodeActive {
		return false, false, finishRollingRefresh(ctx, tx, r, "runtime_failed", "database_node_unavailable")
	}
	if !vm.StandbyRunning || !vm.StandbySynced {
		return false, false, saveRollingPhase(ctx, tx, r, r.Phase, "awaiting_synchronized_standby")
	}
	p, pErr := s.rollingStatus(ctx, primary, id, dbguest.OpStatus)
	if pErr != nil || !refreshReady(p) || p.Major != vm.Major {
		return false, false, saveRollingPhase(ctx, tx, r, r.Phase, "primary_health_unverified")
	}
	im, imErr := s.rollingStatus(ctx, primary, id, dbguest.OpImportStatus)
	if imErr != nil || im.Error != "" || im.MigrationRunning || im.Import != nil && im.Import.State == dbguest.ImportRunning {
		return false, false, saveRollingPhase(ctx, tx, r, r.Phase, "database_copy_unverified_or_active")
	}
	st, stErr := s.rollingStatus(ctx, standby, id, dbguest.OpStatus)
	if stErr != nil || st.State != dbguest.StateStandby || st.Error != "" || st.Major != vm.Major {
		if st.State == dbguest.StateFailed || r.StandbyRestarted && !now.Before(r.RecoverAt) {
			return false, false, finishRollingRefresh(ctx, tx, r, "runtime_failed", "refreshed_standby_unhealthy")
		}
		return false, false, saveRollingPhase(ctx, tx, r, r.Phase, "awaiting_standby_health")
	}
	if p.DesiredImage == "" || p.RunningImage == "" || st.RunningImage == "" || p.DesiredImage != st.DesiredImage {
		return false, false, saveRollingPhase(ctx, tx, r, r.Phase, "awaiting_matching_node_images")
	}
	if !s.replicationHealthy(ctx, primary, id) {
		return false, false, saveRollingPhase(ctx, tx, r, r.Phase, "awaiting_synchronized_standby")
	}
	if st.Stale || st.RunningImage != p.DesiredImage {
		if r.StandbyRestarted {
			return false, false, finishRollingRefresh(ctx, tx, r, "still_stale", "standby_image_still_outdated")
		}
		if err := s.stopRollingVM(ctx, standby, id); err != nil {
			return false, false, finishRollingRefresh(ctx, tx, r, "runtime_failed", "standby_restart_failed")
		}
		if err := store.releaseStandbyTx(ctx, tx, id, true); err != nil {
			return false, false, err
		}
		resumeWait := max(s.cfg.StandbyResumeTimeout, defaultStandbyResumeTimeout) + time.Minute
		_, err := tx.Exec(ctx, `UPDATE native_postgres_base_refreshes
			SET standby_restarted = true, reason = 'awaiting_synchronized_standby', recover_after = $3
			WHERE database_id = $1 AND window_start = $2`, mustUUID(id), r.Window.Start, now.Add(resumeWait))
		return false, true, err
	}
	if !p.Stale && p.RunningImage == p.DesiredImage {
		return false, false, finishRollingRefresh(ctx, tx, r, "succeeded", "")
	}
	// Close admission before any external promotion. Older schedulers do
	// not treat this state as an interrupted park during recovery.
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return false, false, err
	}
	if !window.Contains(now) {
		return false, false, finishRollingRefresh(ctx, tx, r, "window_closed", "standby_validation_incomplete")
	}
	if err := store.transition(ctx, tx, id, []State{StateRunning}, stateMaintenanceHandoff, TransitionOptions{}); err != nil {
		return false, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE native_postgres_base_refreshes SET phase = $3, reason = '', rolling_image = $4
		WHERE database_id = $1 AND window_start = $2`, mustUUID(id), r.Window.Start, switchingOver, p.DesiredImage)
	return err == nil, false, err
}

// Commit the intent to stop the primary before issuing any VM calls that
// change authority. Until then the original primary can safely serve again.
func (s *Scheduler) prepareRollingHandoff(ctx context.Context, store *PgStore, tx pgx.Tx, r baseRefresh, vm VM, primary, standby Node) (bool, error) {
	id := vm.DatabaseID
	if vm.NodeID != r.NodeID || vm.State != stateMaintenanceHandoff {
		return false, finishRollingRefresh(ctx, tx, r, "interrupted", "database_placement_changed")
	}
	abort := func(outcome, reason string) (bool, error) {
		if err := store.transition(ctx, tx, id, []State{stateMaintenanceHandoff}, StateRunning, TransitionOptions{}); err != nil {
			return false, err
		}
		return false, finishRollingRefresh(ctx, tx, r, outcome, reason)
	}
	if vm.StandbyNode != r.StandbyNode || !canFailover(vm) || !vm.StandbyRunning {
		return abort("interrupted", "synchronized_standby_unconfirmed")
	}
	if primary.Lifecycle != NodeActive || standby.Lifecycle != NodeActive {
		return abort("interrupted", "database_node_unavailable")
	}
	window, now, err := rollingWindow(ctx, tx, id)
	if err != nil {
		return false, err
	}
	if !window.Contains(now) || !window.Start.Equal(r.Window.Start) {
		return abort("window_closed", "handoff_not_started")
	}
	st, err := s.rollingStatus(ctx, standby, id, dbguest.OpStatus)
	if err != nil || st.Error != "" || st.Major != vm.Major || st.State != dbguest.StateStandby {
		return abort("interrupted", "standby_health_unverified_during_handoff")
	}
	if st.RunningImage != r.Image || st.Stale {
		return abort("still_stale", "standby_image_changed_during_handoff")
	}
	tag, err := tx.Exec(ctx, `UPDATE native_postgres_base_refreshes SET handoff_started = true
		WHERE database_id = $1 AND window_start = $2 AND clock_timestamp() < window_end`, mustUUID(id), r.Window.Start)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return abort("window_closed", "handoff_not_started")
	}
	return true, nil
}

func (s *Scheduler) handoffRollingRefresh(ctx context.Context, store *PgStore, tx pgx.Tx, r baseRefresh, vm VM, primary, standby Node) (bool, error) {
	id := vm.DatabaseID
	if vm.NodeID != r.NodeID || vm.State != stateMaintenanceHandoff {
		return false, finishRollingRefresh(ctx, tx, r, "interrupted", "database_placement_changed")
	}
	if vm.StandbyNode != r.StandbyNode || !canFailover(vm) {
		return false, saveRollingPhase(ctx, tx, r, r.Phase, "synchronized_standby_unconfirmed")
	}
	if primary.ID == "" || standby.Lifecycle != NodeActive {
		return false, saveRollingPhase(ctx, tx, r, r.Phase, "database_node_unavailable")
	}
	st, stErr := s.rollingStatus(ctx, standby, id, dbguest.OpStatus)
	missing := errors.Is(stErr, ErrVMNotRunning)
	// StateReady is the observable result of a previously issued promotion
	// whose catalog transaction could not be committed.
	if !missing && (stErr != nil || st.Error != "" || st.Major != vm.Major ||
		(st.State != dbguest.StateStandby && st.State != dbguest.StateReady)) {
		return false, saveRollingPhase(ctx, tx, r, r.Phase, "standby_health_unverified_during_handoff")
	}
	if err := s.stopRollingVM(ctx, primary, id); err != nil {
		return false, saveRollingPhase(ctx, tx, r, r.Phase, "primary_stop_unconfirmed")
	}
	if missing {
		// A stopped synchronous volume can take over through the existing
		// parked promotion path once the old primary has been fenced.
		if err := store.releaseStandbyTx(ctx, tx, id, true); err != nil {
			return false, err
		}
	} else if st.State == dbguest.StateStandby {
		op, cancel := context.WithTimeout(ctx, maintenanceStopTimeout)
		err := s.promoteRunningStandby(op, standby, id)
		cancel()
		if err != nil {
			return false, saveRollingPhase(ctx, tx, r, r.Phase, "standby_promotion_unconfirmed")
		}
	}
	after, err := store.failoverTx(ctx, tx, id)
	if err != nil {
		return false, err
	}
	s.attachTried.Delete(id)
	s.freed.signal(primary.ID)
	_, err = tx.Exec(ctx, `UPDATE native_postgres_base_refreshes SET phase = $3,
		reason = 'awaiting_replacement_standby', recover_after = clock_timestamp() + interval '30 minutes'
		WHERE database_id = $1 AND window_start = $2`, mustUUID(id), r.Window.Start, restoringRedundancy)
	return after.State == StateRunning, err
}

func (s *Scheduler) finishRollingRedundancy(ctx context.Context, tx pgx.Tx, r baseRefresh, vm VM, primary, standby Node) error {
	if vm.NodeID != r.StandbyNode || !vm.Replicated || vm.State == StateFailed || vm.Held {
		return finishRollingRefresh(ctx, tx, r, "interrupted", "database_placement_changed")
	}
	if vm.State == StateRunning && vm.StandbyRunning && vm.StandbySynced && standby.Lifecycle == NodeActive {
		p, pErr := s.rollingStatus(ctx, primary, vm.DatabaseID, dbguest.OpStatus)
		st, stErr := s.rollingStatus(ctx, standby, vm.DatabaseID, dbguest.OpStatus)
		if pErr == nil && refreshReady(p) && p.Major == vm.Major && stErr == nil && st.State == dbguest.StateStandby &&
			st.Error == "" && st.Major == vm.Major && s.replicationHealthy(ctx, primary, vm.DatabaseID) {
			if p.Stale || st.Stale || p.RunningImage != r.Image || st.RunningImage != r.Image {
				return finishRollingRefresh(ctx, tx, r, "still_stale", "replacement_image_still_outdated")
			}
			return finishRollingRefresh(ctx, tx, r, "succeeded", "")
		}
	}
	if !s.now().Before(r.RecoverAt) {
		return finishRollingRefresh(ctx, tx, r, "runtime_failed", "replacement_standby_unverified")
	}
	return nil
}

func (s *Scheduler) rollingStatus(ctx context.Context, node Node, id string, op dbguest.Op) (dbguest.Response, error) {
	if node.ID == "" {
		return dbguest.Response{}, ErrNodeUnavailable
	}
	probe, cancel := context.WithTimeout(ctx, storageProbeTimeout)
	defer cancel()
	return s.vmm.Control(probe, node, id, dbguest.Request{Op: op})
}

func (s *Scheduler) replicationHealthy(ctx context.Context, primary Node, id string) bool {
	probe, cancel := context.WithTimeout(ctx, standbyProbeTimeout)
	defer cancel()
	status, err := s.vmm.Replication(probe, primary, id, "", "")
	return err == nil && status.Mode == dbguest.ReplicationSync && status.Streaming && status.Synchronous
}

func (s *Scheduler) stopRollingVM(ctx context.Context, node Node, id string) error {
	stop, cancel := context.WithTimeout(ctx, maintenanceStopTimeout)
	defer cancel()
	return s.vmm.Stop(stop, node, id)
}

// A verified standby can be fenced, prepared and promoted in one sweep.
// Each step commits before the next step can change guest state.
func (s *Scheduler) runRollingRefresh(ctx context.Context, store *PgStore, id string) {
	for range 3 {
		progressed, err := s.advanceRollingRefresh(ctx, store, id)
		if err != nil {
			s.log.Warn("dbsched: could not advance rolling maintenance", "database_id", id, "err", err)
			return
		}
		if !progressed {
			return
		}
	}
}

func guardRollingSwitch(ctx context.Context, tx pgx.Tx, id string) error {
	key, err := pgUUID(id)
	if err != nil {
		return err
	}
	var locked bool
	err = tx.QueryRow(ctx, `SELECT true FROM database_vms WHERE database_id = $1 FOR UPDATE`, key).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	armed, err := rollingSwitchArmed(ctx, tx, id)
	if err != nil {
		return err
	}
	if armed {
		return ErrConflict
	}
	return nil
}

func (s *Scheduler) resumeRollingRefreshes(ctx context.Context, store *PgStore) {
	active, err := store.activeBaseRefreshes(ctx)
	if err != nil {
		s.log.Warn("dbsched: could not read rolling maintenance", "err", err)
		return
	}
	for id, refresh := range active {
		if refresh.Phase != "" {
			s.runRollingRefresh(ctx, store, id)
		}
	}
}

func (s *Scheduler) admitRollingRefreshes(ctx context.Context, store *PgStore, candidates []VM) {
	admitted := 0
	for _, vm := range candidates {
		if admitted >= s.cfg.StaleRestarts {
			break
		}
		if s.refusal(ctx, vm) != nil {
			continue
		}
		started, err := store.beginRollingRefresh(ctx, vm)
		if err != nil {
			s.log.Warn("dbsched: could not admit rolling maintenance", "database_id", vm.DatabaseID, "err", err)
			continue
		}
		if started {
			admitted++
			s.runRollingRefresh(ctx, store, vm.DatabaseID)
		}
	}
}
