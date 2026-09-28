package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type AppWorkPolicy struct {
	AccountID string
	AppID     string
	Policy    workpolicy.Policy
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AppWorkPolicyStore interface {
	UpsertAppWorkPolicy(context.Context, string, string, workpolicy.Policy) (AppWorkPolicy, error)
	AppWorkPolicyByName(context.Context, string, string) (AppWorkPolicy, error)
	ListAppWorkPolicies(context.Context, string) ([]AppWorkPolicy, error)
	DeleteAppWorkPolicy(context.Context, string, string, string) error
}

func scanAppWorkPolicy(row pgx.Row) (AppWorkPolicy, error) {
	var record AppWorkPolicy
	var pending string
	var debounceMS, expiresAfterMS int64
	err := row.Scan(&record.AccountID, &record.AppID, &record.Policy.Name, &record.Revision,
		&record.Policy.MaxRunningPerKey, &pending, &debounceMS, &expiresAfterMS,
		&record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppWorkPolicy{}, ErrNotFound
	}
	if err != nil {
		return AppWorkPolicy{}, err
	}
	record.Policy.PendingUpdates = workpolicy.PendingUpdates(pending)
	record.Policy.Debounce = time.Duration(debounceMS) * time.Millisecond
	record.Policy.ExpiresAfter = time.Duration(expiresAfterMS) * time.Millisecond
	return record, nil
}

const appWorkPolicyColumns = `account_id, app_id, name, revision,
  max_running_per_key, pending_updates, debounce_ms, expires_after_ms,
  created_at, updated_at`

func (s *PgStore) UpsertAppWorkPolicy(ctx context.Context, accountID, appID string, policy workpolicy.Policy) (AppWorkPolicy, error) {
	if err := policy.Validate(); err != nil {
		return AppWorkPolicy{}, err
	}
	if policy.Debounce%time.Millisecond != 0 || policy.ExpiresAfter%time.Millisecond != 0 {
		return AppWorkPolicy{}, fmt.Errorf("state: policy durations must use whole milliseconds")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AppWorkPolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from apps where id = $1 and account_id = $2
		and status <> 'deleted' for update`, appID, accountID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppWorkPolicy{}, ErrNotFound
		}
		return AppWorkPolicy{}, err
	}
	var count int
	var exists bool
	if err := tx.QueryRow(ctx, `select count(*), coalesce(bool_or(name = $2), false)
		from app_work_policies where app_id = $1`, appID, policy.Name).Scan(&count, &exists); err != nil {
		return AppWorkPolicy{}, err
	}
	if !exists && count >= api.MaxWorkPoliciesPerApp {
		return AppWorkPolicy{}, ErrQuotaExceeded
	}
	pending := policy.PendingUpdates
	if pending == "" {
		pending = workpolicy.PendingAll
	}
	record, err := scanAppWorkPolicy(tx.QueryRow(ctx, `
		insert into app_work_policies as p
		  (account_id, app_id, name, max_running_per_key, pending_updates,
		   debounce_ms, expires_after_ms)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (app_id, name) do update set
		  revision = p.revision + case when
		    (p.max_running_per_key, p.pending_updates, p.debounce_ms, p.expires_after_ms)
		    is distinct from
		    (excluded.max_running_per_key, excluded.pending_updates,
		     excluded.debounce_ms, excluded.expires_after_ms)
		    then 1 else 0 end,
		  updated_at = case when
		    (p.max_running_per_key, p.pending_updates, p.debounce_ms, p.expires_after_ms)
		    is distinct from
		    (excluded.max_running_per_key, excluded.pending_updates,
		     excluded.debounce_ms, excluded.expires_after_ms)
		    then now() else p.updated_at end,
		  max_running_per_key = excluded.max_running_per_key,
		  pending_updates = excluded.pending_updates,
		  debounce_ms = excluded.debounce_ms,
		  expires_after_ms = excluded.expires_after_ms
		returning `+appWorkPolicyColumns,
		accountID, appID, policy.Name, policy.MaxRunningPerKey, string(pending),
		policy.Debounce.Milliseconds(), policy.ExpiresAfter.Milliseconds()))
	if err != nil {
		return AppWorkPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppWorkPolicy{}, err
	}
	return record, nil
}

func (s *PgStore) AppWorkPolicyByName(ctx context.Context, appID, name string) (AppWorkPolicy, error) {
	return scanAppWorkPolicy(s.pool.QueryRow(ctx, `select `+appWorkPolicyColumns+`
		from app_work_policies where app_id = $1 and name = $2`, appID, name))
}

func (s *PgStore) ListAppWorkPolicies(ctx context.Context, appID string) ([]AppWorkPolicy, error) {
	rows, err := s.pool.Query(ctx, `select `+appWorkPolicyColumns+`
		from app_work_policies where app_id = $1 order by name`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AppWorkPolicy
	for rows.Next() {
		record, err := scanAppWorkPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *PgStore) DeleteAppWorkPolicy(ctx context.Context, accountID, appID, name string) error {
	tag, err := s.pool.Exec(ctx, `delete from app_work_policies
		where account_id = $1 and app_id = $2 and name = $3`, accountID, appID, name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation &&
			pgErr.ConstraintName == "event_subscription_work_bindings_app_id_policy_name_fkey" {
			return ErrConflict
		}
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func memWorkPolicyKey(appID, name string) string { return canonicalMemUUID(appID) + "\x00" + name }

func (m *MemStore) UpsertAppWorkPolicy(_ context.Context, accountID, appID string, policy workpolicy.Policy) (AppWorkPolicy, error) {
	if err := policy.Validate(); err != nil {
		return AppWorkPolicy{}, err
	}
	if policy.Debounce%time.Millisecond != 0 || policy.ExpiresAfter%time.Millisecond != 0 {
		return AppWorkPolicy{}, fmt.Errorf("state: policy durations must use whole milliseconds")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok {
		app, ok = m.apps[canonicalMemUUID(appID)]
	}
	if !ok || !sameMemUUID(app.AccountID, accountID) || app.Status == AppDeleted {
		return AppWorkPolicy{}, ErrNotFound
	}
	key := memWorkPolicyKey(appID, policy.Name)
	record, exists := m.workPolicies[key]
	if !exists {
		count := 0
		for _, old := range m.workPolicies {
			if sameMemUUID(old.AppID, appID) {
				count++
			}
		}
		if count >= api.MaxWorkPoliciesPerApp {
			return AppWorkPolicy{}, ErrQuotaExceeded
		}
	}
	if policy.PendingUpdates == "" {
		policy.PendingUpdates = workpolicy.PendingAll
	}
	now := time.Now().UTC()
	if !exists {
		record = AppWorkPolicy{AccountID: canonicalMemUUID(accountID),
			AppID: canonicalMemUUID(appID), Revision: 1, CreatedAt: now, UpdatedAt: now}
	} else if record.Policy != policy {
		record.Revision++
		record.UpdatedAt = now
	}
	record.Policy = policy
	m.workPolicies[key] = record
	return record, nil
}

func (m *MemStore) AppWorkPolicyByName(_ context.Context, appID, name string) (AppWorkPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.workPolicies[memWorkPolicyKey(appID, name)]
	if !ok {
		return AppWorkPolicy{}, ErrNotFound
	}
	return record, nil
}

func (m *MemStore) ListAppWorkPolicies(_ context.Context, appID string) ([]AppWorkPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppWorkPolicy
	for _, record := range m.workPolicies {
		if sameMemUUID(record.AppID, appID) {
			out = append(out, record)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Policy.Name < out[j].Policy.Name })
	return out, nil
}

func (m *MemStore) DeleteAppWorkPolicy(_ context.Context, accountID, appID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := memWorkPolicyKey(appID, name)
	record, ok := m.workPolicies[key]
	if !ok || !sameMemUUID(record.AccountID, accountID) {
		return ErrNotFound
	}
	for _, binding := range m.eventWorkBindings {
		if sameMemUUID(binding.AppID, appID) && binding.PolicyName == name {
			return ErrConflict
		}
	}
	delete(m.workPolicies, key)
	return nil
}
