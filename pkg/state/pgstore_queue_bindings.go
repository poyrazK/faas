package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) CreateQueueBinding(ctx context.Context, in QueueBinding) (QueueBinding, error) {
	policy := in.RetryPolicyJSON
	if len(policy) == 0 {
		policy = []byte(`{}`)
	}
	row := s.pool.QueryRow(ctx, `
		insert into queue_bindings
			(account_id, app_id, name, queue_name, mode, workload_class,
			 enabled, max_concurrency, retry_policy)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
		returning id, account_id, app_id, name, queue_name, mode,
		          workload_class, enabled, max_concurrency, retry_policy,
		          created_at, updated_at
	`, in.AccountID, in.AppID, in.Name, in.QueueName, in.Mode,
		string(in.WorkloadClass), in.Enabled, in.MaxConcurrency, policy)
	b, err := scanQueueBinding(row)
	if err != nil {
		if isUniqueViolation(err) {
			return QueueBinding{}, ErrConflict
		}
		return QueueBinding{}, fmt.Errorf("state: insert queue binding: %w", err)
	}
	return b, nil
}

func (s *PgStore) QueueBindingByID(ctx context.Context, accountID, appID, id string) (QueueBinding, error) {
	row := s.pool.QueryRow(ctx, `
		select id, account_id, app_id, name, queue_name, mode,
		       workload_class, enabled, max_concurrency, retry_policy,
		       created_at, updated_at
		  from queue_bindings
		 where id = $1 and account_id = $2 and app_id = $3
	`, id, accountID, appID)
	b, err := scanQueueBinding(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return QueueBinding{}, ErrNotFound
		}
		return QueueBinding{}, fmt.Errorf("state: read queue binding: %w", err)
	}
	return b, nil
}

func (s *PgStore) ListQueueBindingsForApp(ctx context.Context, accountID, appID string) ([]QueueBinding, error) {
	rows, err := s.pool.Query(ctx, `
		select id, account_id, app_id, name, queue_name, mode,
		       workload_class, enabled, max_concurrency, retry_policy,
		       created_at, updated_at
		  from queue_bindings
		 where account_id = $1 and app_id = $2
		 order by created_at asc, id asc
	`, accountID, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list queue bindings: %w", err)
	}
	defer rows.Close()
	out := make([]QueueBinding, 0)
	for rows.Next() {
		b, scanErr := scanQueueBinding(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("state: scan queue binding: %w", scanErr)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list queue bindings rows: %w", err)
	}
	return out, nil
}

func (s *PgStore) UpdateQueueBinding(ctx context.Context, accountID, appID, id string, p UpdateQueueBindingParams) (QueueBinding, error) {
	current, err := s.QueueBindingByID(ctx, accountID, appID, id)
	if err != nil {
		return QueueBinding{}, err
	}
	if p.QueueName != nil {
		current.QueueName = *p.QueueName
	}
	if p.Mode != nil {
		current.Mode = *p.Mode
	}
	if p.WorkloadClass != nil {
		current.WorkloadClass = *p.WorkloadClass
	}
	if p.Enabled != nil {
		current.Enabled = *p.Enabled
	}
	if p.MaxConcurrency != nil {
		current.MaxConcurrency = *p.MaxConcurrency
	}
	if p.RetryPolicyJSON != nil {
		current.RetryPolicyJSON = append([]byte(nil), (*p.RetryPolicyJSON)...)
	}
	if len(current.RetryPolicyJSON) == 0 {
		current.RetryPolicyJSON = []byte(`{}`)
	}
	row := s.pool.QueryRow(ctx, `
		update queue_bindings set
			queue_name = $4, mode = $5, workload_class = $6,
			enabled = $7, max_concurrency = $8, retry_policy = $9::jsonb,
			updated_at = now()
		where id = $1 and account_id = $2 and app_id = $3
		returning id, account_id, app_id, name, queue_name, mode,
		          workload_class, enabled, max_concurrency, retry_policy,
		          created_at, updated_at
	`, id, accountID, appID, current.QueueName, current.Mode,
		string(current.WorkloadClass), current.Enabled, current.MaxConcurrency, current.RetryPolicyJSON)
	b, err := scanQueueBinding(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return QueueBinding{}, ErrNotFound
		}
		if isUniqueViolation(err) {
			return QueueBinding{}, ErrConflict
		}
		return QueueBinding{}, fmt.Errorf("state: update queue binding: %w", err)
	}
	return b, nil
}

func (s *PgStore) DeleteQueueBinding(ctx context.Context, accountID, appID, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from queue_bindings where id = $1 and account_id = $2 and app_id = $3`, id, accountID, appID)
	if err != nil {
		return fmt.Errorf("state: delete queue binding: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type queueBindingScanner interface {
	Scan(dest ...any) error
}

func scanQueueBinding(row queueBindingScanner) (QueueBinding, error) {
	var b QueueBinding
	var class string
	var policy []byte
	if err := row.Scan(&b.ID, &b.AccountID, &b.AppID, &b.Name, &b.QueueName,
		&b.Mode, &class, &b.Enabled, &b.MaxConcurrency, &policy,
		&b.CreatedAt, &b.UpdatedAt); err != nil {
		return QueueBinding{}, err
	}
	b.WorkloadClass = WorkloadClass(class)
	if len(policy) == 0 {
		policy = json.RawMessage(`{}`)
	}
	b.RetryPolicyJSON = append([]byte(nil), policy...)
	return b, nil
}
