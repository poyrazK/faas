package githubd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	checkUpdateMaxAttempts = 12
	checkUpdatePollEvery   = 500 * time.Millisecond
)

var ErrNoCheckUpdate = errors.New("githubd: no check update due")

// CheckUpdate is one claimed generation of the coalescing Check Run outbox.
type CheckUpdate struct {
	DeploymentID string
	Generation   int64
	Attempts     int
}

type CheckUpdateRecord struct {
	DeploymentID string     `json:"deployment_id"`
	Generation   int64      `json:"generation"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	NextAttempt  time.Time  `json:"next_attempt_at"`
	LastError    string     `json:"last_error,omitempty"`
	ProcessedAt  *time.Time `json:"processed_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type CheckUpdateStore interface {
	Claim(ctx context.Context) (CheckUpdate, error)
	Complete(ctx context.Context, update CheckUpdate) error
	Fail(ctx context.Context, update CheckUpdate, message string, nextAttempt time.Time, dead bool) error
}

type PGCheckUpdateStore struct{ pool *pgxpool.Pool }

func NewPGCheckUpdateStore(pool *pgxpool.Pool) *PGCheckUpdateStore {
	return &PGCheckUpdateStore{pool: pool}
}

func (s *PGCheckUpdateStore) Claim(ctx context.Context) (CheckUpdate, error) {
	var out CheckUpdate
	err := s.pool.QueryRow(ctx, `
		with candidate as (
			select deployment_id
			from github_check_updates
			where (status = 'pending' and next_attempt_at <= now())
			   or (status = 'processing' and updated_at < now() - interval '5 minutes')
			order by next_attempt_at, updated_at
			for update skip locked
			limit 1
		)
		update github_check_updates u
		set status = 'processing', attempts = attempts + 1, updated_at = now()
		from candidate c
		where u.deployment_id = c.deployment_id
		returning u.deployment_id, u.generation, u.attempts`).Scan(
		&out.DeploymentID, &out.Generation, &out.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckUpdate{}, ErrNoCheckUpdate
	}
	if err != nil {
		return CheckUpdate{}, fmt.Errorf("githubd: claim check update: %w", err)
	}
	return out, nil
}

func (s *PGCheckUpdateStore) Complete(ctx context.Context, update CheckUpdate) error {
	_, err := s.pool.Exec(ctx, `
		update github_check_updates
		set status = 'succeeded', processed_at = now(), updated_at = now(), last_error = ''
		where deployment_id = $1 and generation = $2`, update.DeploymentID, update.Generation)
	if err != nil {
		return fmt.Errorf("githubd: complete check update: %w", err)
	}
	return nil
}

func (s *PGCheckUpdateStore) Fail(ctx context.Context, update CheckUpdate, message string, nextAttempt time.Time, dead bool) error {
	status := "pending"
	if dead {
		status = "dead"
	}
	_, err := s.pool.Exec(ctx, `
		update github_check_updates
		set status = $3, next_attempt_at = $4, last_error = left($5, 2048),
		    processed_at = case when $3 = 'dead' then now() else null end,
		    updated_at = now()
		where deployment_id = $1 and generation = $2`,
		update.DeploymentID, update.Generation, status, nextAttempt, message)
	if err != nil {
		return fmt.Errorf("githubd: fail check update: %w", err)
	}
	return nil
}

func (s *PGCheckUpdateStore) ListCheckUpdates(ctx context.Context, status string, limit int) ([]CheckUpdateRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if status != "" && status != "pending" && status != "processing" && status != "succeeded" && status != "dead" {
		return nil, fmt.Errorf("githubd: invalid check update status %q", status)
	}
	rows, err := s.pool.Query(ctx, `
		select deployment_id, generation, status, attempts, next_attempt_at,
		       last_error, processed_at, updated_at
		from github_check_updates
		where ($1 = '' or status = $1)
		order by updated_at desc
		limit $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("githubd: list check updates: %w", err)
	}
	defer rows.Close()
	out := make([]CheckUpdateRecord, 0)
	for rows.Next() {
		var record CheckUpdateRecord
		if err := rows.Scan(&record.DeploymentID, &record.Generation, &record.Status,
			&record.Attempts, &record.NextAttempt, &record.LastError,
			&record.ProcessedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("githubd: scan check update: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *PGCheckUpdateStore) RetryCheckUpdate(ctx context.Context, deploymentID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		update github_check_updates
		set generation = generation + 1, status = 'pending', attempts = 0,
		    next_attempt_at = now(), last_error = '', processed_at = null,
		    updated_at = now()
		where deployment_id = $1 and status = 'dead'`, deploymentID)
	if err != nil {
		return false, fmt.Errorf("githubd: retry check update: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RunCheckUpdateWorker drains Check Run projections until ctx is cancelled.
// The outbox coalesces rapid transitions and sync always reads current durable
// deployment state, so delayed work cannot regress a newer GitHub status.
func RunCheckUpdateWorker(ctx context.Context, store CheckUpdateStore, syncCheck func(context.Context, string) error, log *slog.Logger) {
	if store == nil || syncCheck == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(checkUpdatePollEvery)
	defer ticker.Stop()

	for {
		for {
			update, err := store.Claim(ctx)
			if errors.Is(err, ErrNoCheckUpdate) {
				break
			}
			if err != nil {
				log.Error("githubd: claim check update", "err", err)
				break
			}
			if syncErr := syncCheck(ctx, update.DeploymentID); syncErr == nil {
				if err := store.Complete(ctx, update); err != nil {
					log.Error("githubd: complete check update", "deployment_id", update.DeploymentID, "err", err)
				}
				continue
			} else {
				dead := update.Attempts >= checkUpdateMaxAttempts
				delay := time.Second << min(update.Attempts, 8)
				if delay > 5*time.Minute {
					delay = 5 * time.Minute
				}
				if err := store.Fail(ctx, update, syncErr.Error(), time.Now().Add(delay), dead); err != nil {
					log.Error("githubd: reschedule check update", "deployment_id", update.DeploymentID, "err", err)
				}
				log.Warn("githubd: check update failed", "deployment_id", update.DeploymentID,
					"generation", update.Generation, "attempt", update.Attempts, "dead", dead, "err", syncErr)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
