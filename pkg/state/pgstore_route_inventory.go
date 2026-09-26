package state

import (
	"context"
	"fmt"
	"time"

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
		if err := appendDiscoveredRouteEventTx(ctx, tx, event.AccountID, event.AppID, route, when); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `update api_consumer_usage_events
		set discovery_recorded = true where event_id = $1::uuid`, event.EventID); err != nil {
		return fmt.Errorf("api discovery: mark usage receipt: %w", err)
	}
	return nil
}

// appendDiscoveredRouteEventTx writes the first-seen route event and both
// wakeups inside the route-allocation transaction. A retry can neither
// duplicate the event nor commit a route without its durable fanout record.
func appendDiscoveredRouteEventTx(ctx context.Context, tx pgx.Tx, accountID, appID, route string, firstSeen time.Time) error {
	eventPayload, noticePayload, err := apiRouteDiscoveredPayloads(accountID, appID, route, firstSeen)
	if err != nil {
		return fmt.Errorf("api discovery: encode route event: %w", err)
	}
	if len(eventPayload) == 0 || len(noticePayload) == 0 {
		return fmt.Errorf("api discovery: invalid route event for %q", route)
	}
	// Keep the ledger's insertion time current for the bounded fanout recovery
	// sweep; the CloudEvent's time still records when the route was first seen.
	if _, err := tx.Exec(ctx, `insert into events (actor, kind, subject, data)
		values ('apid', 'event.published', $1::uuid, $2::jsonb)`, accountID, eventPayload); err != nil {
		return fmt.Errorf("api discovery: persist route event: %w", err)
	}
	if _, err := tx.Exec(ctx, `select pg_notify($1, $2)`, db.NotifyEventPublished, string(eventPayload)); err != nil {
		return fmt.Errorf("api discovery: notify event fanout: %w", err)
	}
	if _, err := tx.Exec(ctx, `select pg_notify($1, $2)`, db.NotifyAPIRouteDiscovered, string(noticePayload)); err != nil {
		return fmt.Errorf("api discovery: notify dashboard: %w", err)
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
