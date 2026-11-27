package dbsched

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
)

type QueryControlResult struct {
	TargetID string
	Outcome  string
}

func (s *Scheduler) ListSessions(ctx context.Context, id, database string) (dbguest.Sessions, error) {
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return dbguest.Sessions{}, err
	}
	var targets []dbquerycontrol.Target
	store, persistent := s.store.(*PgStore)
	if persistent && queryControlPrimary(vm) {
		// Read the proxy view before inspecting the guest. A newer query
		// cannot acquire a target from the time after that sample.
		targets, err = store.queryTargets(ctx, vm.DatabaseID)
		if err != nil {
			return dbguest.Sessions{}, err
		}
	}
	state, response, err := s.readDiagnostics(ctx, id, dbguest.Request{Op: dbguest.OpSessions, DiagnosticDatabase: database})
	if err != nil || state != dbguest.DiagnosticsAvailable {
		return dbguest.Sessions{State: state}, err
	}
	if response.Sessions == nil {
		return dbguest.Sessions{State: dbguest.DiagnosticsUnavailable}, nil
	}
	after, err := s.store.Get(ctx, id)
	if err != nil {
		return dbguest.Sessions{}, err
	}
	if !queryControlPrimary(after) || after.NodeID != vm.NodeID || !after.StateChangedAt.Equal(vm.StateChangedAt) {
		targets = nil
	}
	attachQueryTargets(response.Sessions, vm, targets)
	return dbguest.Sessions{State: state, Sample: response.Sessions}, nil
}

func attachQueryTargets(sample *dbguest.SessionSample, vm VM, targets []dbquerycontrol.Target) {
	byPID := make(map[int32]dbquerycontrol.Target)
	for _, t := range targets {
		if t.NodeID != vm.NodeID || t.PrimaryEpochMicros != vm.StateChangedAt.UnixMicro() {
			continue
		}
		if _, exists := byPID[t.BackendPID]; exists {
			byPID[t.BackendPID] = dbquerycontrol.Target{}
			continue
		}
		byPID[t.BackendPID] = t
	}
	for i := range sample.Sessions {
		row := &sample.Sessions[i]
		row.TargetID = ""
		if row.State == "active" {
			target := byPID[row.PID]
			row.TargetID = target.ID
			if row.ApplicationName == "" {
				row.ApplicationName = target.ApplicationName
			}
		}
	}
}

func queryControlPrimary(vm VM) bool {
	return vm.State == StateRunning && !vm.Held && vm.MoveNode == "" && !vm.PromotePending
}

func (p *PgStore) queryTargets(ctx context.Context, id string) ([]dbquerycontrol.Target, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT a.target FROM native_postgres_query_snapshots s,
		LATERAL jsonb_array_elements(s.active) a(target)
		WHERE s.observed_at > clock_timestamp() - $2::interval
		  AND a.target->>'database_id'=$1
		LIMIT 1024
	`, id, dbquerycontrol.SnapshotTTL.String())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[dbquerycontrol.Target])
}

func (s *Scheduler) CancelQuery(ctx context.Context, id, targetID string) (string, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return dbquerycontrol.Unconfirmed, nil
	}
	if !dbquerycontrol.ValidID(id) || !dbquerycontrol.ValidID(targetID) {
		return "", ErrInvalid
	}
	if err := store.requestQueryCancel(ctx, id, targetID); err != nil {
		return "", err
	}
	// The command expires independently of the request and can be
	// dispatched only once. A lost proxy reply never repeats the signal.
	until, cancel := context.WithTimeout(ctx, dbquerycontrol.CommandTTL+time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var outcome *string
		var expired bool
		err := store.pool.QueryRow(until, `
			SELECT outcome, requested_at <= clock_timestamp() - $3::interval
			FROM native_postgres_query_cancels WHERE target_id=$1 AND database_id=$2
		`, mustUUID(targetID), mustUUID(id), dbquerycontrol.CommandTTL.String()).Scan(&outcome, &expired)
		if errors.Is(err, pgx.ErrNoRows) {
			return dbquerycontrol.Stale, nil
		}
		if err != nil {
			if until.Err() != nil {
				return dbquerycontrol.Unconfirmed, nil
			}
			return "", err
		}
		if outcome != nil {
			return *outcome, nil
		}
		if expired {
			return dbquerycontrol.Unconfirmed, nil
		}
		select {
		case <-until.Done():
			return dbquerycontrol.Unconfirmed, nil
		case <-ticker.C:
		}
	}
}

func (p *PgStore) requestQueryCancel(ctx context.Context, id, targetID string) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO native_postgres_query_cancels
			(target_id, owner_id, database_id, node_id, primary_since, target)
		SELECT $2::uuid, s.owner_id, v.database_id, v.node_id, v.state_changed_at, a.target
		FROM database_vms v CROSS JOIN native_postgres_query_snapshots s,
			LATERAL jsonb_array_elements(s.active) a(target)
		WHERE v.database_id=$1 AND v.state='running' AND NOT v.held
		  AND v.move_node IS NULL AND NOT v.promote_pending
		  AND s.observed_at > clock_timestamp() - $3::interval
		  AND a.target->>'id'=($2::uuid)::text
		  AND a.target->>'database_id'=v.database_id::text
		  AND a.target->>'node_id'=v.node_id
		  AND (a.target->>'primary_epoch_micros')::bigint=(extract(epoch FROM v.state_changed_at)*1000000)::bigint
		ON CONFLICT (target_id) DO NOTHING
	`, mustUUID(id), mustUUID(targetID), dbquerycontrol.SnapshotTTL.String())
	return err
}

func (s *Scheduler) ExchangeQueryControls(ctx context.Context, owner string, active []dbquerycontrol.Target, results []QueryControlResult) ([]dbquerycontrol.Command, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return nil, ErrConflict
	}
	if !dbquerycontrol.ValidID(owner) || len(active) > dbquerycontrol.MaxTargets || len(results) > dbquerycontrol.MaxCommands {
		return nil, ErrInvalid
	}
	for _, t := range active {
		if !t.Valid() {
			return nil, ErrInvalid
		}
	}
	for _, result := range results {
		if !dbquerycontrol.ValidID(result.TargetID) || !dbquerycontrol.ValidOutcome(result.Outcome) {
			return nil, ErrInvalid
		}
	}
	return store.exchangeQueryControls(ctx, owner, active, results)
}

func (p *PgStore) exchangeQueryControls(ctx context.Context, owner string, active []dbquerycontrol.Target, results []QueryControlResult) ([]dbquerycontrol.Command, error) {
	data, err := json.Marshal(append([]dbquerycontrol.Target{}, active...))
	if err != nil {
		return nil, err
	}
	var commands []dbquerycontrol.Command
	err = pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		key := mustUUID(owner)
		if _, err := tx.Exec(ctx, `
			INSERT INTO native_postgres_query_snapshots (owner_id, active, observed_at)
			VALUES ($1,$2,clock_timestamp())
			ON CONFLICT (owner_id) DO UPDATE SET active=EXCLUDED.active, observed_at=EXCLUDED.observed_at
		`, key, data); err != nil {
			return err
		}
		for _, result := range results {
			if _, err := tx.Exec(ctx, `
				UPDATE native_postgres_query_cancels SET outcome=$3
				WHERE target_id=$1 AND owner_id=$2 AND dispatched_at IS NOT NULL AND outcome IS NULL
			`, mustUUID(result.TargetID), key, result.Outcome); err != nil {
				return err
			}
		}
		// Fence commands whose primary changed after admission.
		if _, err := tx.Exec(ctx, `
			UPDATE native_postgres_query_cancels q SET outcome='stale_target'
			WHERE q.owner_id=$1 AND q.dispatched_at IS NULL AND q.outcome IS NULL
			  AND NOT EXISTS (
				SELECT 1 FROM database_vms v WHERE v.database_id=q.database_id AND v.node_id=q.node_id
				  AND v.state_changed_at=q.primary_since AND v.state='running'
				  AND NOT v.held AND v.move_node IS NULL AND NOT v.promote_pending)
		`, key); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			WITH due AS (
				SELECT q.target_id FROM native_postgres_query_cancels q JOIN database_vms v USING (database_id)
				WHERE q.owner_id=$1 AND q.dispatched_at IS NULL AND q.outcome IS NULL
				  AND q.requested_at > clock_timestamp() - $2::interval
				  AND v.node_id=q.node_id AND v.state_changed_at=q.primary_since
				  AND v.state='running' AND NOT v.held AND v.move_node IS NULL AND NOT v.promote_pending
				ORDER BY q.requested_at LIMIT $3 FOR UPDATE OF q SKIP LOCKED
			)
			UPDATE native_postgres_query_cancels q SET dispatched_at=clock_timestamp()
			FROM due WHERE q.target_id=due.target_id RETURNING q.target, q.requested_at + $2::interval
		`, key, dbquerycontrol.CommandTTL.String(), dbquerycontrol.MaxCommands)
		if err != nil {
			return err
		}
		commands, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (dbquerycontrol.Command, error) {
			var cmd dbquerycontrol.Command
			err := row.Scan(&cmd.Target, &cmd.ExpiresAt)
			return cmd, err
		})
		return err
	})
	return commands, err
}

func (p *PgStore) pruneQueryControls(ctx context.Context) error {
	// One statement: the SQL prepare gate (and the extended protocol) refuse
	// several commands in one literal.
	_, err := p.pool.Exec(ctx, `
		WITH pruned_snapshots AS (
			DELETE FROM native_postgres_query_snapshots
			WHERE observed_at < clock_timestamp() - interval '1 minute'
		)
		DELETE FROM native_postgres_query_cancels
		WHERE requested_at < clock_timestamp() - interval '5 minutes'
	`)
	return err
}
