package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const queuePlatformAlertNoticeSQL = `
	insert into platform_alert_notices (account_id, app_id, kind, subject_id, detail)
	select $1::uuid, $2::uuid, $3, $4, $5::jsonb
	 where not exists (select 1 from platform_alert_notices
	                    where kind = $3 and subject_id = $4
	                      and (status = 'pending' or created_at > now() - $6::interval))
	on conflict do nothing`

// QueuePlatformAlertNotice implements PlatformAlertNoticeStore.
func (s *PgStore) QueuePlatformAlertNotice(ctx context.Context, n PlatformAlertNotice, cooldown time.Duration) (bool, error) {
	tag, err := s.pool.Exec(ctx, queuePlatformAlertNoticeSQL,
		n.AccountID, n.AppID, string(n.Kind), n.SubjectID, platformAlertDetail(n.Detail), cooldown)
	if err != nil {
		return false, fmt.Errorf("state: queue platform alert notice: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ClaimPlatformAlertNotices implements PlatformAlertNoticeStore.
func (s *PgStore) ClaimPlatformAlertNotices(ctx context.Context, limit int, lease time.Duration) ([]PlatformAlertNotice, error) {
	rows, err := s.pool.Query(ctx, `
		update platform_alert_notices n
		   set attempts = n.attempts + 1,
		       lease_expires_at = now() + $2::interval,
		       next_attempt_at = now() + $2::interval
		 where n.id in (select id from platform_alert_notices
		                 where status = 'pending' and next_attempt_at <= now()
		                 order by next_attempt_at, id
		                 for update skip locked
		                 limit $1)
		 returning n.id::text, n.account_id::text, n.app_id::text, n.kind, n.subject_id, n.detail, n.created_at, n.attempts`,
		limit, lease)
	if err != nil {
		return nil, fmt.Errorf("state: claim platform alert notices: %w", err)
	}
	defer rows.Close()
	var out []PlatformAlertNotice
	for rows.Next() {
		var n PlatformAlertNotice
		var kind string
		if err := rows.Scan(&n.ID, &n.AccountID, &n.AppID, &kind, &n.SubjectID, &n.Detail, &n.CreatedAt, &n.Attempts); err != nil {
			return nil, err
		}
		n.Kind = PlatformAlertKind(kind)
		out = append(out, n)
	}
	return out, rows.Err()
}

// FinishPlatformAlertNotice implements PlatformAlertNoticeStore.
func (s *PgStore) FinishPlatformAlertNotice(ctx context.Context, id, status, lastError string) error {
	if status != PlatformAlertSent && status != PlatformAlertSkipped && status != PlatformAlertFailed {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx, `
		update platform_alert_notices
		   set status = $2, finished_at = now(), last_error = $3, lease_expires_at = null
		 where id = $1::uuid and status = 'pending'`, id, status, truncatePlatformAlertError(lastError))
	if err != nil {
		return fmt.Errorf("state: finish platform alert notice: %w", err)
	}
	return nil
}

// RetryPlatformAlertNotice implements PlatformAlertNoticeStore.
func (s *PgStore) RetryPlatformAlertNotice(ctx context.Context, id string, at time.Time, lastError string) error {
	_, err := s.pool.Exec(ctx, `
		update platform_alert_notices
		   set next_attempt_at = $2, last_error = $3, lease_expires_at = null
		 where id = $1::uuid and status = 'pending'`, id, at, truncatePlatformAlertError(lastError))
	if err != nil {
		return fmt.Errorf("state: retry platform alert notice: %w", err)
	}
	return nil
}

// TrimPlatformAlertNotices implements PlatformAlertNoticeStore.
func (s *PgStore) TrimPlatformAlertNotices(ctx context.Context, before time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from platform_alert_notices where status <> 'pending' and finished_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("state: trim platform alert notices: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// PauseCronIfFailing implements CronFailurePauseStore. A run counts once
// it started: an occurrence that missed its start deadline (received_at
// null) is a scheduling miss, not the app failing.
func (s *PgStore) PauseCronIfFailing(ctx context.Context, cronID string, runs int, span, cooldown time.Duration) (bool, error) {
	if runs <= 0 {
		return false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var total, failed int
	var oldest *time.Time
	var lastError string
	// The streak is every finished run since the newest success. Reading only
	// the newest `runs` runs and requiring the oldest of them to be `span` old
	// could never pause a cron firing more often than span/runs (a */15 cron's
	// newest 10 runs span 2.5 h), the very case the pause exists for.
	err = tx.QueryRow(ctx, `
		with counted as (
			select state, created_at, due_at, coalesce(last_error, '') as last_error
			  from invocations
			 where cron_id = $1::uuid and source = 'cron'
			   and (state = 'completed' or (state in ('failed','dead_letter') and received_at is not null
			        -- A run the platform failed before the app ran (the wake, the
			        -- version pin, binding the instance) is not the app failing
			        -- (Observe audit 2026-11-21, finding 20).
			        and coalesce(last_error, '') !~ '^(wake|version pin|bind operation instance): '))
			   -- Runs before the last pause do not count again after a resume.
			   and created_at > coalesce((select max(n.created_at) from platform_alert_notices n
			                               where n.kind = 'cron_failing_paused' and n.subject_id = $1::text), '-infinity')
		), streak as (
			select * from counted
			 where created_at > coalesce((select max(created_at) from counted where state = 'completed'), '-infinity')
		)
		select count(*), count(*), min(due_at),
		       coalesce((select last_error from streak order by created_at desc limit 1), '')
		  from streak`, cronID).Scan(&total, &failed, &oldest, &lastError)
	if err != nil {
		return false, fmt.Errorf("state: read recent cron runs: %w", err)
	}
	if !cronFailureStreakPauses(total, oldest, runs, span) {
		return false, nil
	}
	var accountID, appID, path string
	err = tx.QueryRow(ctx, `
		update crons c set suspended_reason = 'failing'
		  from apps a
		 where c.id = $1::uuid and a.id = c.app_id
		   and c.enabled and c.suspended_reason = '' and cardinality(c.command) = 0
		 returning a.account_id::text, c.app_id::text, c.path`, cronID).Scan(&accountID, &appID, &path)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: pause failing cron: %w", err)
	}
	detail, _ := json.Marshal(cronFailingDetail{Path: path, FailedRuns: failed, Since: oldest.UTC(), LastError: lastError})
	if _, err := tx.Exec(ctx, queuePlatformAlertNoticeSQL, accountID, appID,
		string(PlatformAlertCronFailingPaused), cronID, string(detail), cooldown); err != nil {
		return false, fmt.Errorf("state: queue cron paused notice: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// cronFailingDetail is a cron_failing_paused notice's detail.
type cronFailingDetail struct {
	Path       string    `json:"path"`
	FailedRuns int       `json:"failed_runs"`
	Since      time.Time `json:"since"`
	LastError  string    `json:"last_error,omitempty"`
}

func platformAlertDetail(d json.RawMessage) string {
	if len(d) == 0 {
		return "{}"
	}
	return string(d)
}
