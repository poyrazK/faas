package dbsched

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

type autovacuumPolicy struct {
	profile  dbguest.AutovacuumProfile
	revision int64
}

func (p *PgStore) autovacuumPolicy(ctx context.Context, id string) (autovacuumPolicy, error) {
	var policy autovacuumPolicy
	err := p.pool.QueryRow(ctx, `
		SELECT profile, revision FROM native_postgres_autovacuum_settings WHERE database_id=$1
	`, mustUUID(id)).Scan(&policy.profile, &policy.revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return autovacuumPolicy{profile: dbguest.AutovacuumDefault}, nil
	}
	return policy, err
}

func (p *PgStore) setAutovacuumPolicy(ctx context.Context, id string, profile dbguest.AutovacuumProfile) error {
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		var state State
		err := tx.QueryRow(ctx, `SELECT state FROM database_vms WHERE database_id=$1 FOR SHARE`, mustUUID(id)).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state == StateDeleting || state == StateFailed {
			return ErrConflict
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO native_postgres_autovacuum_settings (database_id, profile, revision)
			VALUES ($1, $2, 1)
			ON CONFLICT (database_id) DO UPDATE SET
				profile=EXCLUDED.profile,
				revision=native_postgres_autovacuum_settings.revision+1,
				next_attempt_at=NULL
			WHERE native_postgres_autovacuum_settings.profile <> EXCLUDED.profile
		`, mustUUID(id), profile)
		return err
	})
}

func (s *Scheduler) Autovacuum(ctx context.Context, id string, profile dbguest.AutovacuumProfile) (dbguest.AutovacuumStatus, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return dbguest.AutovacuumStatus{}, ErrConflict
	}
	if _, err := s.store.Get(ctx, id); err != nil {
		return dbguest.AutovacuumStatus{}, err
	}
	if profile != "" {
		if _, ok := profile.Settings(); !ok {
			return dbguest.AutovacuumStatus{}, ErrInvalid
		}
		if err := store.setAutovacuumPolicy(ctx, id, profile); err != nil {
			return dbguest.AutovacuumStatus{}, err
		}
		if err := s.reconcileAutovacuum(ctx, store, id); err != nil && ctx.Err() == nil {
			s.log.Warn("dbsched: reconcile autovacuum profile", "database_id", id, "err", err)
		}
	}

	policy, err := store.autovacuumPolicy(ctx, id)
	if err != nil {
		return dbguest.AutovacuumStatus{}, err
	}
	return s.autovacuumStatus(ctx, id, policy.profile)
}

func (s *Scheduler) autovacuumStatus(ctx context.Context, id string, profile dbguest.AutovacuumProfile) (dbguest.AutovacuumStatus, error) {
	out := dbguest.AutovacuumStatus{Profile: profile, State: dbguest.DiagnosticsUnavailable}
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return out, err
	}
	if vm.State == StateParked {
		out.State = dbguest.DiagnosticsParked
		return out, nil
	}
	if !autovacuumRunning(vm) {
		return out, nil
	}
	primary := s.probeAutovacuumNode(ctx, vm.NodeID, id, dbguest.StateReady, autovacuumPolicy{})
	var replica *dbguest.AutovacuumSettings
	if vm.StandbyRunning {
		replica = s.probeAutovacuumNode(ctx, vm.StandbyNode, id, dbguest.StateStandby, autovacuumPolicy{})
	}
	after, err := s.store.Get(ctx, id)
	if err != nil {
		return out, err
	}
	if after.State == StateParked {
		out.State = dbguest.DiagnosticsParked
		return out, nil
	}
	if !autovacuumRunning(after) || after.NodeID != vm.NodeID || !after.StateChangedAt.Equal(vm.StateChangedAt) ||
		after.StandbyRunning != vm.StandbyRunning || after.StandbyNode != vm.StandbyNode || primary == nil {
		return out, nil
	}
	out.Effective, out.ReplicaEffective = primary, replica
	out.State = dbguest.AutovacuumPending
	want, _ := profile.Settings()
	if *primary == want && (!vm.StandbyRunning || replica != nil && *replica == want) {
		out.State = dbguest.AutovacuumApplied
	}
	return out, nil
}

func autovacuumRunning(vm VM) bool {
	return vm.State == StateRunning && !vm.Held && vm.MoveNode == "" && !vm.PromotePending
}

func (s *Scheduler) probeAutovacuumNode(ctx context.Context, nodeID, id string, state dbguest.State, policy autovacuumPolicy) *dbguest.AutovacuumSettings {
	node, err := s.node(ctx, nodeID)
	if err != nil {
		return nil
	}
	return s.controlAutovacuum(ctx, node, id, state, policy)
}

func (s *Scheduler) controlAutovacuum(ctx context.Context, node Node, id string, state dbguest.State, policy autovacuumPolicy) *dbguest.AutovacuumSettings {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := s.vmm.Control(ctx, node, id, dbguest.Request{
		Op: dbguest.OpAutovacuum, AutovacuumProfile: policy.profile, AutovacuumRevision: policy.revision,
	})
	if err != nil || resp.Error != "" || resp.State != state {
		return nil
	}
	return resp.Autovacuum
}

func (s *Scheduler) autovacuumBeforeServing(ctx context.Context, node Node, id string, state dbguest.State) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return
	}
	policy, err := store.autovacuumPolicy(ctx, id)
	if err != nil {
		s.log.Warn("dbsched: read autovacuum profile before serving", "database_id", id, "err", err)
		return
	}
	if policy.revision > 0 {
		// The worker retries an unavailable guest without delaying service
		// on mixed-version clusters. The revision also fences delayed writes.
		s.controlAutovacuum(ctx, node, id, state, policy)
	}
}

func (s *Scheduler) reconcileAutovacuum(ctx context.Context, store *PgStore, id string) error {
	vm, err := s.store.Get(ctx, id)
	if err != nil || !autovacuumRunning(vm) {
		return err
	}
	policy, err := store.autovacuumPolicy(ctx, id)
	if err != nil || policy.revision == 0 {
		return err
	}
	want, _ := policy.profile.Settings()
	converged := true
	if vm.StandbyRunning {
		replica := s.probeAutovacuumNode(ctx, vm.StandbyNode, id, dbguest.StateStandby, policy)
		converged = replica != nil && *replica == want
	}
	primary := s.probeAutovacuumNode(ctx, vm.NodeID, id, dbguest.StateReady, policy)
	next := 20 * time.Second
	if converged && primary != nil && *primary == want {
		next = 5 * time.Minute
	}
	_, err = store.pool.Exec(ctx, `
		UPDATE native_postgres_autovacuum_settings SET next_attempt_at=$3
		WHERE database_id=$1 AND revision=$2
	`, mustUUID(id), policy.revision, s.now().Add(next))
	return err
}

func (s *Scheduler) runAutovacuum(ctx context.Context, store *PgStore) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.reconcileAutovacuumBatch(ctx, store); err != nil && ctx.Err() == nil {
				s.log.Warn("dbsched: reconcile autovacuum profiles", "err", err)
			}
		}
	}
}

func (s *Scheduler) reconcileAutovacuumBatch(ctx context.Context, store *PgStore) error {
	now := s.now()
	rows, err := store.pool.Query(ctx, `
		WITH due AS (
			SELECT a.database_id FROM native_postgres_autovacuum_settings a
			JOIN database_vms v USING (database_id)
			WHERE (a.next_attempt_at IS NULL OR a.next_attempt_at <= $1)
			  AND v.state='running' AND NOT v.held AND v.move_node IS NULL AND NOT v.promote_pending
			ORDER BY a.next_attempt_at NULLS FIRST, a.database_id
			LIMIT 8 FOR UPDATE OF a SKIP LOCKED
		)
		UPDATE native_postgres_autovacuum_settings a SET next_attempt_at=$2
		FROM due WHERE a.database_id=due.database_id RETURNING a.database_id::text
	`, now, now.Add(time.Minute))
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		err := s.reconcileAutovacuum(ctx, store, id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
