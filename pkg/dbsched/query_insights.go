package dbsched

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbqueryinsights"
)

func (s *Scheduler) startPrimary(ctx context.Context, node Node, vm VM) error {
	if store, ok := s.store.(*PgStore); ok && !vm.Replicated && vm.StandbyNode == "" && !vm.PromotePending {
		enabled, err := store.queryInsightsEnabled(ctx, vm.DatabaseID)
		if err != nil {
			s.log.Warn("dbsched: could not read query insights policy", "database_id", vm.DatabaseID, "err", err)
		}
		vm.QueryInsights = enabled
	}
	return s.vmm.Start(ctx, node, vm)
}

func (p *PgStore) queryInsightsEnabled(ctx context.Context, id string) (bool, error) {
	var enabled bool
	err := p.pool.QueryRow(ctx, `SELECT enabled FROM native_postgres_query_settings WHERE database_id=$1`, mustUUID(id)).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return enabled, err
}

type querySampleClaim struct {
	id, node, database string
	epoch, revision    time.Time
}

// runQueryInsights is independent of idle parking and storage maintenance.
func (s *Scheduler) runQueryInsights(ctx context.Context, store *PgStore) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.sampleQueries(ctx, store); err != nil && ctx.Err() == nil {
				s.log.Warn("dbsched: query insights sweep failed", "err", err)
			}
		}
	}
}

func (s *Scheduler) sampleQueries(ctx context.Context, store *PgStore) error {
	claims, err := store.claimQuerySamples(ctx, s.now())
	if err != nil {
		return err
	}
	var errs []error
	for _, claim := range claims {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		node, err := s.node(ctx, claim.node)
		if err != nil {
			continue
		}
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		r, err := s.vmm.Control(probe, node, claim.id, dbguest.Request{
			Op: dbguest.OpQueryInsights, DiagnosticDatabase: claim.database,
		})
		cancel()
		if err != nil || r.Error != "" || r.State != dbguest.StateReady || r.QuerySample == nil {
			continue
		}
		if err := store.recordQuerySample(ctx, claim, *r.QuerySample); err != nil {
			errs = append(errs, err)
		}
	}
	_, err = store.pool.Exec(ctx, `
		DELETE FROM native_postgres_query_intervals WHERE (database_id, ended_at) IN (
			SELECT database_id, ended_at FROM native_postgres_query_intervals
			WHERE ended_at < $1 ORDER BY ended_at LIMIT 256 FOR UPDATE SKIP LOCKED
		)`, s.now().Add(-dbqueryinsights.Retention))
	return errors.Join(append(errs, err)...)
}

func (p *PgStore) claimQuerySamples(ctx context.Context, now time.Time) ([]querySampleClaim, error) {
	rows, err := p.pool.Query(ctx, `
		WITH due AS (
			SELECT q.database_id, v.node_id, v.state_changed_at
			FROM native_postgres_query_settings q JOIN database_vms v USING (database_id)
			WHERE q.enabled AND (q.attempted_at IS NULL OR q.attempted_at <= $2)
			  AND v.state='running' AND NOT v.held AND NOT v.replicated AND v.standby_node IS NULL AND v.move_node IS NULL
			ORDER BY q.attempted_at NULLS FIRST, q.database_id
			LIMIT 16 FOR UPDATE OF q SKIP LOCKED
		)
		UPDATE native_postgres_query_settings q SET attempted_at=$1
		FROM due WHERE q.database_id=due.database_id
		RETURNING q.database_id::text, due.node_id, q.database_name, due.state_changed_at, q.updated_at
	`, now, now.Add(-dbqueryinsights.Interval))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []querySampleClaim
	for rows.Next() {
		var c querySampleClaim
		if err := rows.Scan(&c.id, &c.node, &c.database, &c.epoch, &c.revision); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *PgStore) recordQuerySample(ctx context.Context, claim querySampleClaim, sample dbqueryinsights.Sample) error {
	if !sample.Valid() {
		return nil
	}
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		id := mustUUID(claim.id)
		var same bool
		err := tx.QueryRow(ctx, `
			SELECT state='running' AND NOT held AND NOT replicated AND standby_node IS NULL AND move_node IS NULL
				AND node_id=$2 AND state_changed_at=$3
			FROM database_vms WHERE database_id=$1 FOR SHARE`, id, claim.node, claim.epoch).Scan(&same)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !same {
			return nil
		}
		if err != nil {
			return err
		}
		var encoded []byte
		err = tx.QueryRow(ctx, `
			SELECT baseline FROM native_postgres_query_settings
			WHERE database_id=$1 AND enabled AND updated_at=$2 FOR UPDATE`, id, claim.revision).Scan(&encoded)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var previous dbqueryinsights.Sample
		if json.Unmarshal(encoded, &previous) == nil && previous.Valid() {
			if !sample.At.After(previous.At) {
				return nil
			}
			if changes, ok := dbqueryinsights.Changes(previous, sample); ok {
				counters, err := json.Marshal(changes)
				if err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, `
					INSERT INTO native_postgres_query_intervals (database_id, started_at, ended_at, boot_at, counters)
					VALUES ($1,$2,$3,$4,$5::jsonb) ON CONFLICT DO NOTHING`,
					id, previous.At, sample.At, sample.BootAt, counters); err != nil {
					return err
				}
			}
		}
		encoded, err = json.Marshal(sample)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE native_postgres_query_settings SET baseline=$2::jsonb, sampled_at=$3 WHERE database_id=$1`,
			id, encoded, sample.At)
		return err
	})
}

func (p *PgStore) maintenanceQuerySettings(ctx context.Context) (map[string]bool, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT q.database_id::text, q.enabled FROM native_postgres_query_settings q
		JOIN native_postgres_maintenance_windows w USING (database_id)
		JOIN database_vms v USING (database_id)
		WHERE v.state='running' AND v.always_on AND NOT v.replicated
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		var enabled bool
		if err := rows.Scan(&id, &enabled); err != nil {
			return nil, err
		}
		out[id] = enabled
	}
	return out, rows.Err()
}
