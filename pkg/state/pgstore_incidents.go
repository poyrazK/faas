package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// setAlertRuleStateSQL changes a rule's state and its episode in one
// statement (ADR-20261022193512): turning firing opens an episode unless one
// is open and trims the rule's resolved episodes to the newest
// AlertEpisodesPerRule-1; turning ok resolves the open one. degraded and
// unknown leave the episode alone. Every CTE reads the same snapshot, so the
// trim never sees the episode it is making room for.
const setAlertRuleStateSQL = `
	with changed as (
		update alert_rules
		   set state      = $2,
		       updated_at = $3
		 where id = $1 and state <> $2
		returning id, account_id, state
	), opened as (
		insert into alert_episodes (id, rule_id, account_id, opened_at)
		select $4::uuid, id, account_id, $3::timestamptz from changed where state = 'firing'
		on conflict (rule_id) where resolved_at is null do nothing
		returning rule_id
	), trimmed as (
		delete from alert_episodes
		 where rule_id in (select rule_id from opened)
		   and resolved_at is not null
		   and id not in (select id from alert_episodes where rule_id = $1 order by opened_at desc, id desc limit $5)
	), resolved as (
		update alert_episodes e
		   set resolved_at = greatest($3::timestamptz, e.opened_at)
		  from changed
		 where changed.state = 'ok' and e.rule_id = changed.id and e.resolved_at is null
	)
	select count(*) from changed`

const alertEpisodeColumns = `e.id::text, e.rule_id::text, e.account_id::text, coalesce(r.app_id::text, ''),
	r.name, r.metric, r.state, e.opened_at, e.resolved_at`

func (s *PgStore) ListAlertEpisodes(ctx context.Context, accountID, appID string, resolvedSince time.Time, limit int) ([]AlertEpisode, error) {
	rows, err := s.pool.Query(ctx, `
		with open_episodes as (
			select e.id from alert_episodes e join alert_rules r on r.id = e.rule_id
			 where e.account_id = $1 and e.resolved_at is null and ($4 = '' or r.app_id::text = $4)
		), recent as (
			select e.id from alert_episodes e join alert_rules r on r.id = e.rule_id
			 where e.account_id = $1 and e.resolved_at >= $2 and ($4 = '' or r.app_id::text = $4)
			 order by e.resolved_at desc limit $3
		)
		select `+alertEpisodeColumns+`
		  from alert_episodes e join alert_rules r on r.id = e.rule_id
		 where e.id in (select id from open_episodes union all select id from recent)
		 order by e.opened_at desc, e.id desc
		 limit $3`, accountID, resolvedSince.UTC(), limit, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AlertEpisode{}
	for rows.Next() {
		ep, err := scanAlertEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ep)
	}
	return out, rows.Err()
}

func (s *PgStore) AlertEpisodeByID(ctx context.Context, accountID, id string) (AlertEpisode, error) {
	if _, err := uuid.Parse(id); err != nil {
		return AlertEpisode{}, ErrNotFound
	}
	ep, err := scanAlertEpisode(s.pool.QueryRow(ctx, `
		select `+alertEpisodeColumns+`
		  from alert_episodes e join alert_rules r on r.id = e.rule_id
		 where e.id = $1 and e.account_id = $2`, id, accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertEpisode{}, ErrNotFound
	}
	return ep, err
}

func scanAlertEpisode(row pgx.Row) (AlertEpisode, error) {
	var ep AlertEpisode
	var metric, ruleState string
	if err := row.Scan(&ep.ID, &ep.RuleID, &ep.AccountID, &ep.AppID, &ep.RuleName, &metric, &ruleState, &ep.OpenedAt, &ep.ResolvedAt); err != nil {
		return AlertEpisode{}, err
	}
	ep.Metric, ep.RuleState = AlertMetric(metric), AlertState(ruleState)
	ep.OpenedAt = ep.OpenedAt.UTC()
	if ep.ResolvedAt != nil {
		at := ep.ResolvedAt.UTC()
		ep.ResolvedAt = &at
	}
	return ep, nil
}

// ListRouteMonitorIncidentSummaries reads metadata columns only; the saved
// entry can be half a megabyte. The app subquery keeps the read on the
// (app_id, opened_at) history index.
func (s *PgStore) ListRouteMonitorIncidentSummaries(ctx context.Context, accountID, appID string, closedSince time.Time, limit int) ([]RouteMonitorIncidentSummary, error) {
	rows, err := s.pool.Query(ctx, `
		select id::text, app_id::text, deployment_id::text, status, opened_at, closed_at
		  from route_monitor_incidents
		 where app_id in (select id from apps where account_id = $1 and ($4 = '' or id::text = $4))
		   and account_id = $1
		   and (status = 'open' or closed_at >= $2)
		 order by opened_at desc, id desc
		 limit $3`, accountID, closedSince.UTC(), limit, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RouteMonitorIncidentSummary{}
	for rows.Next() {
		inc, err := scanRouteMonitorIncidentSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

func (s *PgStore) RouteMonitorIncidentSummaryByID(ctx context.Context, accountID, id string) (RouteMonitorIncidentSummary, error) {
	if _, err := uuid.Parse(id); err != nil {
		return RouteMonitorIncidentSummary{}, ErrNotFound
	}
	inc, err := scanRouteMonitorIncidentSummary(s.pool.QueryRow(ctx, `
		select id::text, app_id::text, deployment_id::text, status, opened_at, closed_at
		  from route_monitor_incidents
		 where id = $1 and account_id = $2`, id, accountID))
	if errors.Is(err, pgx.ErrNoRows) {
		return RouteMonitorIncidentSummary{}, ErrNotFound
	}
	return inc, err
}

func scanRouteMonitorIncidentSummary(row pgx.Row) (RouteMonitorIncidentSummary, error) {
	var inc RouteMonitorIncidentSummary
	if err := row.Scan(&inc.ID, &inc.AppID, &inc.DeploymentID, &inc.Status, &inc.OpenedAt, &inc.ClosedAt); err != nil {
		return RouteMonitorIncidentSummary{}, err
	}
	inc.OpenedAt = inc.OpenedAt.UTC()
	if inc.ClosedAt != nil {
		at := inc.ClosedAt.UTC()
		inc.ClosedAt = &at
	}
	return inc, nil
}

// alertEpisodeKeep is how many resolved episodes survive an opening.
const alertEpisodeKeep = api.AlertEpisodesPerRule - 1
