package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
)

const projectRollbackRecoveryWindow = 30 * time.Minute

const projectRollbackServingGateway = `n.active AND n.role IN ('compute-only', 'compute-node')
	AND nullif(btrim(n.gateway_target_url), '') IS NOT NULL`

// Keep waiting for gateways that are temporarily unavailable. Retired gateways
// leave the barrier, and newly serving gateways have to acknowledge the route.
const projectRollbackPendingGateways = `ARRAY(
	SELECT n.name FROM compute_nodes n
	WHERE n.lifecycle <> 'retired'
	  AND (n.name = ANY(r.expected_gateways) OR (` + projectRollbackServingGateway + `))
	ORDER BY n.name
)`

// A later serving period of the same deployment is a different cutover.
// Service-mode cutovers use the handoff's checked rollback ID through drain.
const projectRollbackRouteCurrent = `
	EXISTS (
		SELECT 1 FROM deployments d
		WHERE d.id = r.target_id AND d.app_id = r.app_id AND d.deleted_at IS NULL
		  AND d.status = 'live' AND d.traffic_percent = 100
		  AND (CASE WHEN r.service THEN
		         d.service_rollout_handoff #>> '{bindings_check,request_id}' = r.rollback_id::text
		         AND d.service_rollout_handoff ->> 'action' = 'promote'
		       ELSE d.rollout_completed_at = r.routing_committed_at END)
		  AND NOT EXISTS (
		      SELECT 1 FROM deployments other WHERE other.app_id = r.app_id
		        AND other.scope IN ('default', 'production', '') AND other.id <> d.id
		        AND other.deleted_at IS NULL AND other.status = 'live' AND other.traffic_percent > 0)
	)`

type ProjectRollbackHTTPRecoveryStore interface {
	ProjectRollbackHTTPRecovery(context.Context, string) (map[string]api.ProjectRollbackTraffic, error)
	ReplayProjectRollbackRoutes(context.Context) error
}

// ProjectRollbackHealthStore reads a route only while it still belongs to the
// same serving period. Passive recovery evidence can outlive that period.
type ProjectRollbackHealthStore interface {
	ProjectRollbackHealthRoute(context.Context, string, string) (api.ProjectRollbackTraffic, error)
}

func (s *PgStore) ProjectRollbackHealthRoute(ctx context.Context, operation, attempt string) (api.ProjectRollbackTraffic, error) {
	var route api.ProjectRollbackTraffic
	var expired bool
	err := s.pool.QueryRow(ctx, `
		SELECT r.generation, r.routing_committed_at, r.gateways_applied_at, r.expires_at <= clock_timestamp()
		FROM project_rollback_http_recovery r WHERE r.operation_id = $1 AND r.rollback_id = $2
		  AND r.generation IS NOT NULL AND `+projectRollbackRouteCurrent, operation, attempt).
		Scan(&route.RouteGeneration, &route.RoutingCommittedAt, &route.GatewaysAppliedAt, &expired)
	if err != nil {
		return route, mapErr(err)
	}
	// A resume may happen after passive observations stopped. Requeue the
	// same generation if its gateway barrier still needs acknowledgements.
	if expired && route.GatewaysAppliedAt == nil {
		_, err = s.pool.Exec(ctx, `
			UPDATE project_rollback_http_recovery r
			SET next_notify_at = clock_timestamp(),
			    expires_at = clock_timestamp() + $3::double precision * interval '1 second'
			WHERE r.operation_id = $1 AND r.rollback_id = $2 AND r.gateways_applied_at IS NULL
			  AND r.expires_at <= clock_timestamp() AND `+projectRollbackRouteCurrent, operation, attempt, projectRollbackRecoveryWindow.Seconds())
	}
	return route, err
}

func startProjectRollbackHTTPRecovery(ctx context.Context, tx pgx.Tx, id string) error {
	var route db.DeploymentRouteChangedPayload
	err := tx.QueryRow(ctx, `
		UPDATE project_rollback_http_recovery r
		SET generation = nextval('deployment_route_generation_seq'),
		    routing_committed_at = CASE WHEN r.service THEN clock_timestamp() ELSE d.rollout_completed_at END,
		    expected_gateways = ARRAY(SELECT n.name FROM compute_nodes n
		        WHERE `+projectRollbackServingGateway+` ORDER BY n.name),
		    next_notify_at = clock_timestamp() + interval '10 seconds',
		    expires_at = clock_timestamp() + $2::double precision * interval '1 second'
		FROM deployments d WHERE r.rollback_id = $1 AND r.generation IS NULL AND d.id = r.target_id
		RETURNING r.app_id::text, r.target_id::text, r.generation`, id, projectRollbackRecoveryWindow.Seconds()).
		Scan(&route.AppID, &route.DeploymentID, &route.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	body, err := json.Marshal(route)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify($1, $2)`, db.NotifyProjectRollbackRouteChanged, string(body))
	return err
}

// Replay the same generation after missed notifications or a gateway restart.
// Both scheduling and publishing commit together.
func (s *PgStore) ReplayProjectRollbackRoutes(ctx context.Context) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// An ACK can commit while this batch is being selected. Reconcile from a
	// fresh statement snapshot once the selected receipts are locked.
	rows, err := tx.Query(ctx, `
		SELECT generation FROM project_rollback_http_recovery
		WHERE next_notify_at <= clock_timestamp() AND generation IS NOT NULL
		ORDER BY next_notify_at, generation LIMIT $1 FOR UPDATE SKIP LOCKED`, api.ServiceBindingCheckBatchSize)
	if err != nil {
		return err
	}
	generations, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(generations) == 0 {
		return err
	}

	_, err = tx.Exec(ctx, `
		WITH due AS (
			SELECT r.generation, r.expected_gateways, r.gateways_applied_at,
			       r.expires_at > clock_timestamp() AND `+projectRollbackRouteCurrent+` AS watching
			FROM project_rollback_http_recovery r
			WHERE r.generation = ANY($1::bigint[])
		), pending AS (
			SELECT r.generation, `+projectRollbackPendingGateways+` AS gateways
			FROM due r WHERE r.watching AND r.gateways_applied_at IS NULL
		), scheduled AS (
			UPDATE project_rollback_http_recovery r
			SET next_notify_at = CASE WHEN due.watching THEN clock_timestamp() + interval '10 seconds' END,
			    expected_gateways = coalesce(pending.gateways, r.expected_gateways),
			    acknowledged_gateways = CASE WHEN pending.gateways IS NULL THEN r.acknowledged_gateways
			        ELSE ARRAY(SELECT g FROM unnest(r.acknowledged_gateways) AS g WHERE g = ANY(pending.gateways)) END,
			    gateways_applied_at = CASE WHEN cardinality(pending.gateways) > 0
			          AND pending.gateways <@ r.acknowledged_gateways
			        THEN clock_timestamp() ELSE r.gateways_applied_at END
			FROM due LEFT JOIN pending USING (generation) WHERE r.generation = due.generation
			RETURNING r.app_id, r.target_id, r.generation, r.next_notify_at
		)
		SELECT pg_notify($2, jsonb_build_object(
		    'app_id', app_id, 'deployment_id', target_id, 'generation', generation)::text)
		FROM scheduled WHERE next_notify_at IS NOT NULL`, generations, db.NotifyProjectRollbackRouteChanged)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AcknowledgeProjectRollbackHTTPRoute is called after the gateway has installed
// the exact target in its routing caches. Old notification-only ACKs cannot
// advance this receipt.
func (s *PgStore) AcknowledgeProjectRollbackHTTPRoute(ctx context.Context, route db.DeploymentRouteChangedPayload, node string) (time.Time, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Take the receipt lock before reading the roster. A statement that waits
	// here can otherwise see a newer receipt alongside an older roster snapshot.
	var rollbackID string
	err = tx.QueryRow(ctx, `
		SELECT rollback_id::text FROM project_rollback_http_recovery
		WHERE generation = $1 AND app_id = $2 AND target_id = $3
		  AND first_success_at IS NULL AND expires_at > clock_timestamp()
		FOR UPDATE`, route.Generation, route.AppID, route.DeploymentID).Scan(&rollbackID)
	if err != nil {
		return time.Time{}, mapErr(err)
	}

	var expires time.Time
	err = tx.QueryRow(ctx, `
		WITH matched AS (
			SELECT r.rollback_id, r.acknowledged_gateways,
			       CASE WHEN r.gateways_applied_at IS NULL THEN `+projectRollbackPendingGateways+`
			            ELSE r.expected_gateways END AS expected
			FROM project_rollback_http_recovery r
			WHERE r.rollback_id = $1
			  AND EXISTS (SELECT 1 FROM compute_nodes WHERE name = $2 AND lifecycle <> 'retired')
			  AND NOT EXISTS (SELECT 1 FROM compute_node_db_logins WHERE login = session_user AND node_name <> $2)
			  AND r.expires_at > clock_timestamp() AND `+projectRollbackRouteCurrent+`
		), accepted AS (
			SELECT rollback_id, expected, ARRAY(
				SELECT g FROM unnest(expected) AS g WHERE g = $2 OR g = ANY(acknowledged_gateways)
			) AS acknowledged
			FROM matched WHERE $2 = ANY(expected)
		)
		UPDATE project_rollback_http_recovery r
		SET expected_gateways = accepted.expected, acknowledged_gateways = accepted.acknowledged,
		    gateways_applied_at = CASE WHEN accepted.expected <@ accepted.acknowledged
		        THEN coalesce(r.gateways_applied_at, clock_timestamp()) ELSE r.gateways_applied_at END
		FROM accepted WHERE r.rollback_id = accepted.rollback_id
		RETURNING r.expires_at`, rollbackID, node).Scan(&expires)
	if err != nil {
		return time.Time{}, mapErr(err)
	}
	return expires, mapErr(tx.Commit(ctx))
}

func (s *PgStore) RecordProjectRollbackHTTPResponse(ctx context.Context, route db.DeploymentRouteChangedPayload, node string, startedAt, succeededAt time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE project_rollback_http_recovery r
		SET first_success_at = coalesce(r.first_success_at, least($6, clock_timestamp())), next_notify_at = NULL
		WHERE r.generation = $1 AND r.app_id = $2 AND r.target_id = $3
		  AND $4 = ANY(r.acknowledged_gateways)
		  AND NOT EXISTS (SELECT 1 FROM compute_node_db_logins WHERE login = session_user AND node_name <> $4)
		  AND r.gateways_applied_at <= $5 AND $5 <= $6
		  AND $6 <= clock_timestamp() + interval '5 seconds' AND $6 <= r.expires_at
		  AND r.expires_at > clock_timestamp()
		  AND `+projectRollbackRouteCurrent, route.Generation, route.AppID, route.DeploymentID, node, startedAt, succeededAt)
	return tag.RowsAffected() != 0, err
}

func (s *PgStore) ProjectRollbackHTTPRecovery(ctx context.Context, id string) (map[string]api.ProjectRollbackTraffic, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.rollback_id::text, r.generation, r.routing_committed_at,
		       r.gateways_applied_at, r.first_success_at,
		       CASE
		         WHEN r.first_success_at IS NOT NULL THEN 'verified'
		         WHEN r.routing_committed_at IS NULL THEN 'awaiting_cutover'
		         WHEN r.expires_at <= clock_timestamp() THEN 'expired'
		         WHEN NOT `+projectRollbackRouteCurrent+` THEN 'superseded'
		         WHEN r.gateways_applied_at IS NULL THEN 'awaiting_gateways'
		         ELSE 'awaiting_traffic'
		       END
		FROM project_rollback_http_recovery r WHERE r.operation_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]api.ProjectRollbackTraffic)
	for rows.Next() {
		var child string
		var t api.ProjectRollbackTraffic
		if err := rows.Scan(&child, &t.RouteGeneration, &t.RoutingCommittedAt, &t.GatewaysAppliedAt, &t.FirstSuccessAt, &t.Status); err != nil {
			return nil, err
		}
		out[child] = t
	}
	return out, rows.Err()
}

var _ ProjectRollbackHTTPRecoveryStore = (*PgStore)(nil)

var _ ProjectRollbackHealthStore = (*PgStore)(nil)
