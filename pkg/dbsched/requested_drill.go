package dbsched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbarchive"
)

const DrillQueued DrillResult = "queued"

var requestedDrillNamespace = uuid.MustParse("9511f9b8-f2c2-4c3c-a78f-05e65c421e31")

// RequestRestoreDrill records a request before waking the source or
// allocating a copy. The key is scoped to the source database.
func (s *Scheduler) RequestRestoreDrill(ctx context.Context, sourceID, requestID string) (Drill, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return Drill{}, ErrConflict
	}
	sourceKey, sourceErr := uuid.Parse(sourceID)
	requestKey, requestErr := uuid.Parse(requestID)
	if sourceErr != nil || requestErr != nil || requestKey == uuid.Nil {
		return Drill{}, ErrInvalid
	}
	id := uuid.NewSHA1(requestedDrillNamespace, append(sourceKey[:], requestKey[:]...)).String()
	if existing, err := store.requestedDrill(ctx, sourceKey.String(), id); !errors.Is(err, ErrNotFound) {
		return existing, err
	}
	source, err := store.Get(ctx, sourceKey.String())
	if err != nil {
		return Drill{}, err
	}
	if err := s.drillSourceReady(source); err != nil {
		return Drill{}, err
	}
	_, err = store.pool.Exec(ctx, `
		INSERT INTO database_restore_drills (drill_id, source_id, node_id, request_id, target_at, result)
		VALUES ($1, $2, $3, $4, $5, 'queued')
		ON CONFLICT DO NOTHING
	`, mustUUID(id), mustUUID(source.DatabaseID), source.NodeID, mustUUID(requestKey.String()), time.Time{})
	if err != nil {
		return Drill{}, err
	}
	drill, err := store.requestedDrill(ctx, source.DatabaseID, id)
	if errors.Is(err, ErrNotFound) {
		return Drill{}, fmt.Errorf("%w: a restore drill is already queued or running", ErrConflict)
	}
	return drill, err
}

func (s *Scheduler) GetRestoreDrill(ctx context.Context, sourceID, id string) (Drill, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return Drill{}, ErrConflict
	}
	if _, err := uuid.Parse(id); err != nil {
		return Drill{}, ErrInvalid
	}
	d, err := store.requestedDrill(ctx, sourceID, id)
	if err == nil && d.Rehearsal != nil {
		return Drill{}, ErrNotFound
	}
	return d, err
}

func (s *Scheduler) drillSourceReady(source VM) error {
	if !source.Bootstrapped || source.LastBackupAt.IsZero() || !source.LastBackupAt.Before(s.now()) {
		return fmt.Errorf("%w: the database has no completed base backup", ErrConflict)
	}
	if source.Held || source.MoveNode != "" || source.PromotePending ||
		(source.State != StateRunning && source.State != StateParked) {
		return fmt.Errorf("%w: the database is not ready for a restore drill", ErrConflict)
	}
	return nil
}

const requestedDrillColumns = `drill_id::text, node_id, source_id::text, COALESCE(request_id::text, ''),
	target_at, started_at, finished_at, recovered_to, result, error_code, pending_outcome`

func readRequestedDrill(row pgx.Row) (Drill, error) {
	var d Drill
	var finished, recovered *time.Time
	var code, pending *string
	err := row.Scan(&d.ID, &d.NodeID, &d.SourceID, &d.RequestID, &d.TargetAt, &d.StartedAt,
		&finished, &recovered, &d.Result, &code, &pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	if finished != nil {
		d.FinishedAt = *finished
	}
	if recovered != nil {
		d.RecoveredTo = *recovered
	}
	if code != nil {
		d.ErrorCode = *code
	}
	if pending != nil {
		d.PendingOutcome = DrillResult(*pending)
	}
	return d, err
}

func (p *PgStore) requestedDrill(ctx context.Context, source, id string) (Drill, error) {
	d, err := readRequestedDrill(p.pool.QueryRow(ctx, `
		SELECT `+requestedDrillColumns+` FROM database_restore_drills
		WHERE drill_id=$1 AND source_id=$2
		  AND (request_id IS NOT NULL OR EXISTS (SELECT 1 FROM database_upgrade_rehearsals r WHERE r.drill_id=$1))
	`, mustUUID(id), mustUUID(source)))
	if err != nil {
		return d, err
	}
	return p.withUpgradeRehearsal(ctx, d)
}

func (p *PgStore) activeRequestedDrills(ctx context.Context) ([]Drill, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT `+requestedDrillColumns+` FROM database_restore_drills
		WHERE result IN ('queued','running') AND (request_id IS NOT NULL OR EXISTS (
			SELECT 1 FROM database_upgrade_rehearsals r WHERE r.drill_id=database_restore_drills.drill_id
		))
		ORDER BY started_at, drill_id
	`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Drill, error) { return readRequestedDrill(row) })
}

// lockDrill shares admission between scheduled and requested drills. The
// source lock also covers a database whose node changed after admission.
func (s *Scheduler) lockDrill(ctx context.Context, d Drill) (func(), bool, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return func() {}, true, nil
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	unlock := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }
	for _, key := range []string{"dbsched:drill-node:" + d.NodeID, "dbsched:drill-source:" + d.SourceID} {
		var locked bool
		err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended($1,0))`, key).Scan(&locked)
		if err != nil || !locked {
			unlock()
			return nil, false, err
		}
	}
	return unlock, true, nil
}

func (p *PgStore) otherActiveDrill(ctx context.Context, d Drill, includeQueued bool) (bool, error) {
	var active bool
	err := p.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM database_restore_drills
			WHERE (node_id=$1 OR source_id=$2) AND drill_id<>$3
			  AND (result='running' OR (result='queued' AND ($4 OR EXISTS (
			      SELECT 1 FROM database_upgrade_rehearsals r
			      WHERE r.drill_id=database_restore_drills.drill_id AND r.restore_started
			  )))))
	`, d.NodeID, mustUUID(d.SourceID), mustUUID(d.ID), includeQueued).Scan(&active)
	return active, err
}

func (s *Scheduler) runRequestedDrills(ctx context.Context, store *PgStore) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.advanceRequestedDrills(ctx, store); err != nil && ctx.Err() == nil {
				s.log.Warn("dbsched: advance requested restore drills", "err", err)
			}
		}
	}
}

func (s *Scheduler) advanceRequestedDrills(ctx context.Context, store *PgStore) error {
	drills, err := store.activeRequestedDrills(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, d := range drills {
		step, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.advanceRequestedDrill(step, store, d)
		cancel()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *Scheduler) advanceRequestedDrill(ctx context.Context, store *PgStore, d Drill) error {
	unlock, locked, err := s.lockDrill(ctx, d)
	if err != nil || !locked {
		return err
	}
	defer unlock()
	d, err = store.requestedDrill(ctx, d.SourceID, d.ID)
	if err != nil {
		return err
	}
	if d.Result != DrillQueued && d.Result != DrillRunning {
		return nil
	}
	if d.PendingOutcome != "" {
		return s.finishRequestedDrill(ctx, store, d)
	}
	if d.Rehearsal != nil {
		return s.advanceUpgradeRehearsal(ctx, store, d)
	}
	if d.Result == DrillRunning {
		result, code, recovered, done := s.drillOutcome(ctx, d)
		if !done {
			return nil
		}
		if code == errCodeRestore {
			probe, cancel := context.WithTimeout(ctx, 10*time.Second)
			check, _ := s.CheckRestoreTarget(probe, d.SourceID, d.TargetAt)
			cancel()
			if check.Outcome == dbarchive.RestoreWALGap {
				code = "missing_wal"
			}
		}
		return s.completeRequestedDrill(ctx, store, d, result, code, recovered)
	}
	if s.now().Sub(d.StartedAt) > s.cfg.DrillTimeout {
		return s.completeRequestedDrill(ctx, store, d, DrillFailed, "drill_timeout", time.Time{})
	}
	// Restore places its VM before returning. Recover that handoff after a
	// scheduler restart without writing another source marker.
	if !d.TargetAt.IsZero() {
		vm, err := s.store.Get(ctx, d.ID)
		if err == nil {
			if _, err := s.resumeExisting(ctx, vm, d.SourceID, d.TargetAt); err != nil {
				return err
			}
			return store.markRequestedDrillRunning(ctx, d.ID)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	busy, err := store.otherActiveDrill(ctx, d, false)
	if err != nil || busy {
		return err
	}
	source, err := s.store.Get(ctx, d.SourceID)
	if errors.Is(err, ErrNotFound) || err == nil && (source.NodeID != d.NodeID || s.drillSourceReady(source) != nil) {
		return s.completeRequestedDrill(ctx, store, d, DrillSkipped, "drill_source_unavailable", time.Time{})
	}
	if err != nil {
		return err
	}
	if d.TargetAt.IsZero() {
		d.TargetAt, err = s.markDrill(ctx, d)
		if err != nil {
			return s.failRequestedDrillStart(ctx, store, d, err)
		}
		_, err = store.pool.Exec(ctx, `UPDATE database_restore_drills SET target_at=$2 WHERE drill_id=$1`,
			mustUUID(d.ID), d.TargetAt)
		if err != nil {
			return err
		}
	}
	if _, err := s.Restore(ctx, drillRestoreRequest(d, source)); err != nil {
		return s.failRequestedDrillStart(ctx, store, d, err)
	}
	return store.markRequestedDrillRunning(ctx, d.ID)
}

func (p *PgStore) markRequestedDrillRunning(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE database_restore_drills SET result='running' WHERE drill_id=$1 AND result='queued'
	`, mustUUID(id))
	return err
}

func (s *Scheduler) failRequestedDrillStart(ctx context.Context, store *PgStore, d Drill, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	result, code := drillStartFailure(err)
	return s.completeRequestedDrill(ctx, store, d, result, code, time.Time{})
}

// Record the outcome before deleting the temporary copy. If cleanup or
// the final update fails, the next sweep can finish with the same outcome.
func (s *Scheduler) completeRequestedDrill(ctx context.Context, store *PgStore, d Drill, result DrillResult, code string, recovered time.Time) error {
	var at *time.Time
	if !recovered.IsZero() {
		at = &recovered
	}
	_, err := store.pool.Exec(ctx, `
		UPDATE database_restore_drills SET pending_outcome=$2, error_code=NULLIF($3,''), recovered_to=$4
		WHERE drill_id=$1 AND result IN ('queued','running')
	`, mustUUID(d.ID), result, code, at)
	if err != nil {
		return err
	}
	d.PendingOutcome = result
	return s.finishRequestedDrill(ctx, store, d)
}

func (s *Scheduler) finishRequestedDrill(ctx context.Context, store *PgStore, d Drill) error {
	if !d.TargetAt.IsZero() {
		var err error
		if d.Rehearsal != nil {
			err = s.discardUpgradeRehearsal(ctx, store, d)
		} else {
			err = s.discardDrill(ctx, d)
		}
		if err != nil {
			return err
		}
	}
	tag, err := store.pool.Exec(ctx, `
		UPDATE database_restore_drills SET result=pending_outcome, pending_outcome=NULL, finished_at=clock_timestamp()
		WHERE drill_id=$1 AND pending_outcome IS NOT NULL
	`, mustUUID(d.ID))
	if err == nil && tag.RowsAffected() == 1 && d.Rehearsal == nil {
		s.cfg.Metrics.drilled(d.PendingOutcome)
	}
	return err
}
