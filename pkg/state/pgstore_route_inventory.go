package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db"
)

// recordDiscoveredRouteTx runs in the same transaction as the financial
// event. The receipt bit on that event makes a lost RPC acknowledgement safe
// to replay without inflating the inventory count.
func recordDiscoveredRouteTx(ctx context.Context, tx pgx.Tx, event APIConsumerUsageEvent) error {
	route := discoveredRouteFor(event)
	if route == "" {
		return nil
	}
	var accountID, appID string
	var recorded bool
	if err := tx.QueryRow(ctx, `select account_id::text, app_id::text, discovery_recorded
		from api_consumer_usage_events where event_id = $1::uuid for update`, event.EventID).Scan(&accountID, &appID, &recorded); err != nil {
		return fmt.Errorf("api discovery: lock usage event: %w", err)
	}
	if accountID != event.AccountID || appID != event.AppID {
		return fmt.Errorf("api discovery: event ID belongs to another account or app")
	}
	if recorded {
		return nil
	}
	when := discoveredAtFor(event)
	environmentID, ok, err := discoveredRouteEnvironmentTx(ctx, tx, event)
	if err != nil {
		return err
	}
	if !ok {
		// The serving stage is gone; its routes belong to no inventory.
		return markDiscoveryRecordedTx(ctx, tx, event.EventID)
	}
	if environmentID != "" {
		return recordEnvironmentRouteTx(ctx, tx, event, environmentID, route, when)
	}
	newRoute := false
	updated, err := updateDiscoveredRouteTx(ctx, tx, event.AccountID, event.AppID, route, when)
	if err != nil {
		return err
	}
	if !updated {
		// Only new route allocation serializes by app. Existing routes use one
		// indexed UPDATE; hot apps do not take an app-wide lock per request.
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1::text, 0))`, event.AppID); err != nil {
			return fmt.Errorf("api discovery: lock app inventory: %w", err)
		}
		updated, err = updateDiscoveredRouteTx(ctx, tx, event.AccountID, event.AppID, route, when)
		if err != nil {
			return err
		}
		if !updated {
			if route != discoveredRouteOverflow {
				var count int
				if err := tx.QueryRow(ctx, `select count(*) from app_api_routes
					where account_id = $1::uuid and app_id = $2::uuid and route_template <> $3`,
					event.AccountID, event.AppID, discoveredRouteOverflow).Scan(&count); err != nil {
					return fmt.Errorf("api discovery: count routes: %w", err)
				}
				if count >= DiscoveredRouteLimit {
					route = discoveredRouteOverflow
				}
			}
			result, err := tx.Exec(ctx, `insert into app_api_routes
				(account_id, app_id, route_template, first_seen, last_seen, request_count)
				select $1::uuid, $2::uuid, $3, $4, $4, 1 from apps
				where id = $2::uuid and account_id = $1::uuid
				on conflict (account_id, app_id, route_template) do nothing`,
				event.AccountID, event.AppID, route, when)
			if err != nil {
				return fmt.Errorf("api discovery: insert route: %w", err)
			}
			if result.RowsAffected() == 1 {
				newRoute = route != discoveredRouteOverflow
			} else {
				// A writer outside this lock protocol may have won the insert.
				// Preserve its aggregate update without emitting a duplicate event.
				updated, err = updateDiscoveredRouteTx(ctx, tx, event.AccountID, event.AppID, route, when)
				if err != nil {
					return err
				}
				if !updated {
					return fmt.Errorf("api discovery: app is not owned by account")
				}
			}
		}
	}
	if newRoute {
		if err := appendDiscoveredRouteEventTx(ctx, tx, event.AccountID, event.AppID, "", route, when); err != nil {
			return err
		}
	}
	return markDiscoveryRecordedTx(ctx, tx, event.EventID)
}

func markDiscoveryRecordedTx(ctx context.Context, tx pgx.Tx, eventID string) error {
	if _, err := tx.Exec(ctx, `update api_consumer_usage_events
		set discovery_recorded = true where event_id = $1::uuid`, eventID); err != nil {
		return fmt.Errorf("api discovery: mark usage receipt: %w", err)
	}
	return nil
}

// discoveredRouteEnvironmentTx returns the stage of the deployment that
// served the route (ADR-20261104120000): "" for production, project-less
// apps and older gateways that report no deployment, and ok=false when the
// deployment's stage no longer exists.
func discoveredRouteEnvironmentTx(ctx context.Context, tx pgx.Tx, event APIConsumerUsageEvent) (string, bool, error) {
	if _, err := uuid.Parse(discoveredDeploymentFor(event)); err != nil {
		return "", true, nil
	}
	var scope, environmentID string
	err := tx.QueryRow(ctx, `select d.scope, coalesce((select e.id::text from project_environments e
			where e.project_id = a.project_id and e.slug = d.scope), '')
		from deployments d join apps a on a.id = d.app_id
		where d.id = $1::uuid and d.app_id = $2::uuid and a.account_id = $3::uuid and a.project_id is not null`,
		discoveredDeploymentFor(event), event.AppID, event.AccountID).Scan(&scope, &environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("api discovery: read deployment environment: %w", err)
	}
	if IsProductionScope(scope) {
		return "", true, nil
	}
	return environmentID, environmentID != "", nil
}

// recordEnvironmentRouteTx files a stage deployment's route in the stage's
// own inventory, with the same bounds and first-seen event as production's.
func recordEnvironmentRouteTx(ctx context.Context, tx pgx.Tx, event APIConsumerUsageEvent, environmentID, route string, when time.Time) error {
	update := func(route string) (bool, error) {
		result, err := tx.Exec(ctx, `update environment_api_routes
			set first_seen = least(first_seen, $4::timestamptz),
			    last_seen = greatest(last_seen, $4::timestamptz),
			    request_count = request_count + 1
			where environment_id = $1::uuid and app_id = $2::uuid and route_template = $3`,
			environmentID, event.AppID, route, when)
		if err != nil {
			return false, fmt.Errorf("api discovery: update stage route: %w", err)
		}
		return result.RowsAffected() == 1, nil
	}
	newRoute := false
	updated, err := update(route)
	if err != nil {
		return err
	}
	if !updated {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1::text, 0))`, event.AppID+":"+environmentID); err != nil {
			return fmt.Errorf("api discovery: lock stage inventory: %w", err)
		}
		if updated, err = update(route); err != nil {
			return err
		}
	}
	if !updated {
		if route != discoveredRouteOverflow {
			var count int
			if err := tx.QueryRow(ctx, `select count(*) from environment_api_routes
				where environment_id = $1::uuid and app_id = $2::uuid and route_template <> $3`,
				environmentID, event.AppID, discoveredRouteOverflow).Scan(&count); err != nil {
				return fmt.Errorf("api discovery: count stage routes: %w", err)
			}
			if count >= DiscoveredRouteLimit {
				route = discoveredRouteOverflow
				if updated, err = update(route); err != nil {
					return err
				}
			}
		}
	}
	if !updated {
		result, err := tx.Exec(ctx, `insert into environment_api_routes
			(account_id, app_id, environment_id, route_template, first_seen, last_seen, request_count)
			select $1::uuid, $2::uuid, $3::uuid, $4, $5, $5, 1 from apps
			where id = $2::uuid and account_id = $1::uuid
			on conflict (environment_id, app_id, route_template) do nothing`,
			event.AccountID, event.AppID, environmentID, route, when)
		if err != nil {
			return fmt.Errorf("api discovery: insert stage route: %w", err)
		}
		if result.RowsAffected() == 1 {
			newRoute = route != discoveredRouteOverflow
		} else if updated, err = update(route); err != nil {
			return err
		} else if !updated {
			return fmt.Errorf("api discovery: app is not owned by account")
		}
	}
	if newRoute {
		if err := appendDiscoveredRouteEventTx(ctx, tx, event.AccountID, event.AppID, environmentID, route, when); err != nil {
			return err
		}
	}
	return markDiscoveryRecordedTx(ctx, tx, event.EventID)
}

// appendDiscoveredRouteEventTx writes the first-seen route event and both
// wakeups inside the route-allocation transaction. A retry can neither
// duplicate the event nor commit a route without its durable fanout record.
func appendDiscoveredRouteEventTx(ctx context.Context, tx pgx.Tx, accountID, appID, environmentID, route string, firstSeen time.Time) error {
	eventPayload, noticePayload, err := apiRouteDiscoveredPayloads(accountID, appID, environmentID, route, firstSeen)
	if err != nil {
		return fmt.Errorf("api discovery: encode route event: %w", err)
	}
	if len(eventPayload) == 0 || len(noticePayload) == 0 {
		return fmt.Errorf("api discovery: invalid route event for %q", route)
	}
	// The ledger insert also creates durable fanout work through the trigger;
	// the CloudEvent's time still records when the route was first seen. A
	// rehearsal-enrolled app's candidate routes stay in its own inventory but
	// never fan out to the account's other apps (ADR-20261019180000).
	// ADR-20261030170000: the fanout trigger writes the receipt and skips
	// the events row, so the insert reports no rows; whether the app is
	// rehearsal-enrolled decides the notify instead.
	var enrolled bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from scenario_test_action_policies p where p.app_id = $1::uuid)`, appID).Scan(&enrolled); err != nil {
		return fmt.Errorf("api discovery: read rehearsal enrollment: %w", err)
	}
	if !enrolled {
		if _, err := tx.Exec(ctx, `insert into events (actor, kind, subject, data) values ('apid', 'event.published', $1::uuid, $2::jsonb)`,
			accountID, eventPayload); err != nil {
			return fmt.Errorf("api discovery: persist route event: %w", err)
		}
		if _, err := tx.Exec(ctx, `select pg_notify($1, $2)`, db.NotifyEventPublished, "1"); err != nil {
			return fmt.Errorf("api discovery: notify event fanout: %w", err)
		}
	}
	// The dashboard lists production's inventory; a stage's routes stay out
	// of it.
	if environmentID == "" {
		if _, err := tx.Exec(ctx, `select pg_notify($1, $2)`, db.NotifyAPIRouteDiscovered, string(noticePayload)); err != nil {
			return fmt.Errorf("api discovery: notify dashboard: %w", err)
		}
	}
	return nil
}

func updateDiscoveredRouteTx(ctx context.Context, tx pgx.Tx, accountID, appID, route string, when time.Time) (bool, error) {
	result, err := tx.Exec(ctx, `update app_api_routes
		set first_seen = least(first_seen, $4::timestamptz),
		    last_seen = greatest(last_seen, $4::timestamptz),
		    request_count = request_count + 1
		where account_id = $1::uuid and app_id = $2::uuid and route_template = $3`,
		accountID, appID, route, when)
	if err != nil {
		return false, fmt.Errorf("api discovery: update route: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *PgStore) ListDiscoveredAPIRoutes(ctx context.Context, accountID, appID string, limit int) ([]DiscoveredAPIRoute, bool, error) {
	if err := validateAuditQuery(accountID, appID, limit); err != nil {
		return nil, false, err
	}
	rows, err := s.pool.Query(ctx, `select route_template, first_seen, last_seen, request_count
		from app_api_routes where account_id = $1::uuid and app_id = $2::uuid
		and route_template <> $3 order by route_template limit $4`,
		accountID, appID, discoveredRouteOverflow, limit)
	if err != nil {
		return nil, false, err
	}
	out := make([]DiscoveredAPIRoute, 0)
	for rows.Next() {
		var route DiscoveredAPIRoute
		if err := rows.Scan(&route.RouteTemplate, &route.FirstSeen, &route.LastSeen, &route.RequestCount); err != nil {
			rows.Close()
			return nil, false, err
		}
		route.FirstSeen, route.LastSeen = route.FirstSeen.UTC(), route.LastSeen.UTC()
		out = append(out, route)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, err
	}
	rows.Close()
	var capHit bool
	if err := s.pool.QueryRow(ctx, `select exists (select 1 from app_api_routes
		where account_id = $1::uuid and app_id = $2::uuid and route_template = $3)`,
		accountID, appID, discoveredRouteOverflow).Scan(&capHit); err != nil {
		return nil, false, err
	}
	return out, capHit, nil
}
