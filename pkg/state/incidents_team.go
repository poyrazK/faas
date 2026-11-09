package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TeamIncidentStore reads alert episodes by app, whichever account holds
// them (ADR-20261109120000). An organization's apps keep their alert rules,
// and so their episodes, under the account of the member who created the
// app; a teammate's incident list reads them by the apps it may see, and the
// caller decides that visibility.
type TeamIncidentStore interface {
	// ListAlertEpisodesForApps returns the open episodes of rules on apps
	// plus those resolved at or after resolvedSince, newest opened first, at
	// most limit. Each app's AccountID names the account holding its rows.
	ListAlertEpisodesForApps(ctx context.Context, apps []App, resolvedSince time.Time, limit int) ([]AlertEpisode, error)
	// AlertEpisodeByIDAnyAccount returns an episode whichever account holds
	// it; the caller authorizes it by its app.
	AlertEpisodeByIDAnyAccount(ctx context.Context, id string) (AlertEpisode, error)
}

var (
	_ TeamIncidentStore = (*PgStore)(nil)
	_ TeamIncidentStore = (*MemStore)(nil)
)

func (s *PgStore) ListAlertEpisodesForApps(ctx context.Context, apps []App, resolvedSince time.Time, limit int) ([]AlertEpisode, error) {
	ids, accounts := make([]string, 0, len(apps)), make([]string, 0, len(apps))
	for _, app := range apps {
		if _, err := uuid.Parse(app.ID); err != nil {
			continue
		}
		if _, err := uuid.Parse(app.AccountID); err != nil {
			continue
		}
		ids = append(ids, app.ID)
		if !slices.Contains(accounts, app.AccountID) {
			accounts = append(accounts, app.AccountID)
		}
	}
	if len(ids) == 0 || limit <= 0 {
		return []AlertEpisode{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		with scoped as (
			-- account_id first, so the alert_episodes account indexes apply.
			select e.id, e.resolved_at from alert_episodes e left join alert_rules r on r.id = e.rule_id
			 where e.account_id = any($4::uuid[]) and coalesce(r.app_id, e.app_id) = any($1::uuid[])
		), open_episodes as (
			select id from scoped where resolved_at is null
		), recent as (
			select id from scoped where resolved_at >= $2 order by resolved_at desc limit $3
		)
		select `+alertEpisodeColumns+`
		  from alert_episodes e left join alert_rules r on r.id = e.rule_id
		 where e.id in (select id from open_episodes union all select id from recent)
		 order by e.opened_at desc, e.id desc
		 limit $3`, ids, resolvedSince.UTC(), limit, accounts)
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

func (s *PgStore) AlertEpisodeByIDAnyAccount(ctx context.Context, id string) (AlertEpisode, error) {
	if _, err := uuid.Parse(id); err != nil {
		return AlertEpisode{}, ErrNotFound
	}
	ep, err := scanAlertEpisode(s.pool.QueryRow(ctx, `
		select `+alertEpisodeColumns+`
		  from alert_episodes e left join alert_rules r on r.id = e.rule_id
		 where e.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertEpisode{}, ErrNotFound
	}
	return ep, err
}

func (m *MemStore) ListAlertEpisodesForApps(_ context.Context, apps []App, resolvedSince time.Time, limit int) ([]AlertEpisode, error) {
	appIDs := make([]string, 0, len(apps))
	for _, app := range apps {
		appIDs = append(appIDs, app.ID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []AlertEpisode{}
	for _, ep := range m.alertEpisodes {
		if ep.ResolvedAt != nil && ep.ResolvedAt.Before(resolvedSince) {
			continue
		}
		ep = m.withRuleLocked(ep)
		if ep.AppID == "" || !slices.Contains(appIDs, ep.AppID) {
			continue
		}
		out = append(out, ep)
	}
	sortEpisodesNewestFirst(out)
	return out[:min(len(out), max(limit, 0))], nil
}

func (m *MemStore) AlertEpisodeByIDAnyAccount(_ context.Context, id string) (AlertEpisode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ep, ok := m.alertEpisodes[id]
	if !ok {
		return AlertEpisode{}, ErrNotFound
	}
	return m.withRuleLocked(ep), nil
}
