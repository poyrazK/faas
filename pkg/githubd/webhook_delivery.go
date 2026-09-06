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
	webhookMaxAttempts = 8
	webhookPollEvery   = 500 * time.Millisecond
)

var ErrNoWebhookDelivery = errors.New("githubd: no webhook delivery due")
var ErrCheckRunNotFound = errors.New("githubd: check run not found")

// WebhookDelivery is one authenticated GitHub delivery stored before the
// HTTP request is acknowledged.
type WebhookDelivery struct {
	DeliveryID string
	EventType  string
	Payload    []byte
	Attempts   int
}

// WebhookDeliveryRecord is the operator-safe delivery view. Payload is
// deliberately omitted because webhook bodies can contain customer metadata.
type WebhookDeliveryRecord struct {
	DeliveryID  string     `json:"delivery_id"`
	EventType   string     `json:"event_type"`
	Status      string     `json:"status"`
	Attempts    int        `json:"attempts"`
	NextAttempt time.Time  `json:"next_attempt_at"`
	LastError   string     `json:"last_error,omitempty"`
	ReceivedAt  time.Time  `json:"received_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// WebhookDeliveryStore is the durable inbox contract. Enqueue returns false
// for a GitHub redelivery that is already present.
type WebhookDeliveryStore interface {
	Enqueue(ctx context.Context, delivery WebhookDelivery) (bool, error)
	Claim(ctx context.Context) (WebhookDelivery, error)
	Complete(ctx context.Context, deliveryID string) error
	Fail(ctx context.Context, deliveryID, message string, nextAttempt time.Time, dead bool) error
	Prune(ctx context.Context, before time.Time) error
}

type PGWebhookStore struct{ pool *pgxpool.Pool }

func NewPGWebhookDeliveryStore(pool *pgxpool.Pool) *PGWebhookStore {
	return &PGWebhookStore{pool: pool}
}

func (s *PGWebhookStore) Enqueue(ctx context.Context, delivery WebhookDelivery) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		insert into github_webhook_deliveries (delivery_id, event_type, payload)
		values ($1, $2, $3)
		on conflict (delivery_id) do nothing`,
		delivery.DeliveryID, delivery.EventType, delivery.Payload)
	if err != nil {
		return false, fmt.Errorf("githubd: enqueue webhook delivery: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PGWebhookStore) Claim(ctx context.Context) (WebhookDelivery, error) {
	var out WebhookDelivery
	err := s.pool.QueryRow(ctx, `
		with candidate as (
			select delivery_id
			from github_webhook_deliveries
			where (status = 'pending' and next_attempt_at <= now())
			   or (status = 'processing' and updated_at < now() - interval '5 minutes')
			order by next_attempt_at, received_at
			for update skip locked
			limit 1
		)
		update github_webhook_deliveries d
		set status = 'processing', attempts = attempts + 1, updated_at = now()
		from candidate c
		where d.delivery_id = c.delivery_id
		returning d.delivery_id, d.event_type, d.payload, d.attempts`).Scan(
		&out.DeliveryID, &out.EventType, &out.Payload, &out.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookDelivery{}, ErrNoWebhookDelivery
	}
	if err != nil {
		return WebhookDelivery{}, fmt.Errorf("githubd: claim webhook delivery: %w", err)
	}
	return out, nil
}

func (s *PGWebhookStore) Complete(ctx context.Context, deliveryID string) error {
	_, err := s.pool.Exec(ctx, `
		update github_webhook_deliveries
		set status = 'succeeded', processed_at = now(), updated_at = now(), last_error = ''
		where delivery_id = $1`, deliveryID)
	if err != nil {
		return fmt.Errorf("githubd: complete webhook delivery: %w", err)
	}
	return nil
}

func (s *PGWebhookStore) Fail(ctx context.Context, deliveryID, message string, nextAttempt time.Time, dead bool) error {
	status := "pending"
	if dead {
		status = "dead"
	}
	_, err := s.pool.Exec(ctx, `
		update github_webhook_deliveries
		set status = $2, next_attempt_at = $3, last_error = left($4, 2048),
		    processed_at = case when $2 = 'dead' then now() else null end,
		    updated_at = now()
		where delivery_id = $1`, deliveryID, status, nextAttempt, message)
	if err != nil {
		return fmt.Errorf("githubd: fail webhook delivery: %w", err)
	}
	return nil
}

func (s *PGWebhookStore) Prune(ctx context.Context, before time.Time) error {
	_, err := s.pool.Exec(ctx, `
		delete from github_webhook_deliveries
		where status = 'succeeded' and processed_at < $1`, before)
	if err != nil {
		return fmt.Errorf("githubd: prune webhook deliveries: %w", err)
	}
	return nil
}

func (s *PGWebhookStore) ListWebhookDeliveries(ctx context.Context, status string, limit int) ([]WebhookDeliveryRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if status != "" && status != "pending" && status != "processing" && status != "succeeded" && status != "dead" {
		return nil, fmt.Errorf("githubd: invalid webhook delivery status %q", status)
	}
	rows, err := s.pool.Query(ctx, `
		select delivery_id, event_type, status, attempts, next_attempt_at,
		       last_error, received_at, processed_at, updated_at
		from github_webhook_deliveries
		where ($1 = '' or status = $1)
		order by received_at desc
		limit $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("githubd: list webhook deliveries: %w", err)
	}
	defer rows.Close()
	out := make([]WebhookDeliveryRecord, 0)
	for rows.Next() {
		var record WebhookDeliveryRecord
		if err := rows.Scan(&record.DeliveryID, &record.EventType, &record.Status,
			&record.Attempts, &record.NextAttempt, &record.LastError,
			&record.ReceivedAt, &record.ProcessedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("githubd: scan webhook delivery: %w", err)
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// RetryWebhookDelivery moves one dead delivery back to the pending queue.
// The operation is a CAS so an active or already-retried delivery is untouched.
func (s *PGWebhookStore) RetryWebhookDelivery(ctx context.Context, deliveryID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		update github_webhook_deliveries
		set status = 'pending', attempts = 0, next_attempt_at = now(),
		    last_error = '', processed_at = null, updated_at = now()
		where delivery_id = $1 and status = 'dead'`, deliveryID)
	if err != nil {
		return false, fmt.Errorf("githubd: retry webhook delivery: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *PGWebhookStore) CheckRunID(ctx context.Context, repoFullName, commitSHA, checkName string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		select check_run_id from github_check_runs
		where repo_full_name = $1 and commit_sha = $2 and check_name = $3`,
		repoFullName, commitSHA, checkName).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrCheckRunNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("githubd: load check run id: %w", err)
	}
	return id, nil
}

func (s *PGWebhookStore) SaveCheckRunID(ctx context.Context, repoFullName, commitSHA, checkName string, id int64) error {
	_, err := s.pool.Exec(ctx, `
		insert into github_check_runs (repo_full_name, commit_sha, check_name, check_run_id)
		values ($1, $2, $3, $4)
		on conflict (repo_full_name, commit_sha, check_name) do update
		set check_run_id = excluded.check_run_id, updated_at = now()`,
		repoFullName, commitSHA, checkName, id)
	if err != nil {
		return fmt.Errorf("githubd: save check run id: %w", err)
	}
	return nil
}

// RunWebhookDeliveryWorker drains authenticated deliveries until ctx is
// cancelled. Business errors are retried with bounded exponential backoff;
// ignored/unbound/rejected events are successful deliveries and are retained
// for idempotency until the retention prune.
func RunWebhookDeliveryWorker(ctx context.Context, store WebhookDeliveryStore, service *Service, log *slog.Logger) {
	if store == nil || service == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(webhookPollEvery)
	defer ticker.Stop()
	prune := time.NewTicker(6 * time.Hour)
	defer prune.Stop()

	for {
		for {
			delivery, err := store.Claim(ctx)
			if errors.Is(err, ErrNoWebhookDelivery) {
				break
			}
			if err != nil {
				log.Error("githubd: claim webhook delivery", "err", err)
				break
			}
			dispatchErr := service.HandleWebhookDelivery(ctx, delivery)
			if dispatchErr == nil || IsNoBinding(dispatchErr) || IsIgnored(dispatchErr) || IsSkipDeploy(dispatchErr) || isReleaseTagRejected(dispatchErr) {
				if err := store.Complete(ctx, delivery.DeliveryID); err != nil {
					log.Error("githubd: complete webhook delivery", "delivery_id", delivery.DeliveryID, "err", err)
				}
				continue
			}
			dead := delivery.Attempts >= webhookMaxAttempts
			delay := time.Second << min(delivery.Attempts, 8)
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}
			if err := store.Fail(ctx, delivery.DeliveryID, dispatchErr.Error(), time.Now().Add(delay), dead); err != nil {
				log.Error("githubd: reschedule webhook delivery", "delivery_id", delivery.DeliveryID, "err", err)
			}
			log.Warn("githubd: webhook delivery failed", "delivery_id", delivery.DeliveryID,
				"event", delivery.EventType, "attempt", delivery.Attempts, "dead", dead, "err", dispatchErr)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-prune.C:
			if err := store.Prune(ctx, time.Now().Add(-7*24*time.Hour)); err != nil {
				log.Warn("githubd: prune webhook deliveries", "err", err)
			}
		}
	}
}
