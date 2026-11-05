package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var _ DashboardStore = (*PgStore)(nil)

const dashboardColumns = `id::text, account_id::text, name, widgets, created_at, updated_at`

// CreateDashboard inserts under a per-account transaction lock, so two
// concurrent creates cannot both take the last slot.
func (s *PgStore) CreateDashboard(ctx context.Context, d Dashboard, limit int) (Dashboard, error) {
	if err := validateDashboard(d); err != nil {
		return Dashboard{}, err
	}
	var out Dashboard
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended('dashboards:' || $1, 0))`, d.AccountID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `select count(*) from dashboards where account_id = $1`, d.AccountID).Scan(&count); err != nil {
			return err
		}
		if count >= limit {
			return &DashboardQuotaError{Limit: limit, Observed: count}
		}
		return scanDashboard(tx.QueryRow(ctx,
			`insert into dashboards (account_id, name, widgets) values ($1, $2, $3) returning `+dashboardColumns,
			d.AccountID, d.Name, d.Widgets), &out)
	})
	return out, dashboardWriteErr("create", err)
}

// ReplaceDashboard implements DashboardStore.
func (s *PgStore) ReplaceDashboard(ctx context.Context, d Dashboard) (Dashboard, error) {
	if err := validateDashboard(d); err != nil {
		return Dashboard{}, err
	}
	var out Dashboard
	err := scanDashboard(s.pool.QueryRow(ctx,
		`update dashboards set name = $3, widgets = $4, updated_at = now()
		  where account_id = $1 and id = $2 returning `+dashboardColumns,
		d.AccountID, d.ID, d.Name, d.Widgets), &out)
	return out, dashboardWriteErr("replace", err)
}

// ListDashboards implements DashboardStore.
func (s *PgStore) ListDashboards(ctx context.Context, accountID string) ([]Dashboard, error) {
	rows, err := s.pool.Query(ctx, `select `+dashboardColumns+` from dashboards where account_id = $1 order by name`, accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list dashboards: %w", err)
	}
	defer rows.Close()
	var out []Dashboard
	for rows.Next() {
		var d Dashboard
		if err := scanDashboard(rows, &d); err != nil {
			return nil, fmt.Errorf("state: scan dashboard: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list dashboards: %w", err)
	}
	return out, nil
}

// DashboardByID implements DashboardStore.
func (s *PgStore) DashboardByID(ctx context.Context, accountID, id string) (Dashboard, error) {
	var out Dashboard
	err := scanDashboard(s.pool.QueryRow(ctx,
		`select `+dashboardColumns+` from dashboards where account_id = $1 and id::text = $2`, accountID, id), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return Dashboard{}, ErrNotFound
	}
	if err != nil {
		return Dashboard{}, fmt.Errorf("state: read dashboard: %w", err)
	}
	return out, nil
}

// DeleteDashboard implements DashboardStore.
func (s *PgStore) DeleteDashboard(ctx context.Context, accountID, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from dashboards where account_id = $1 and id::text = $2`, accountID, id)
	if err != nil {
		return fmt.Errorf("state: delete dashboard: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanDashboard(row pgx.Row, out *Dashboard) error {
	if err := row.Scan(&out.ID, &out.AccountID, &out.Name, &out.Widgets, &out.CreatedAt, &out.UpdatedAt); err != nil {
		return err
	}
	out.CreatedAt, out.UpdatedAt = out.CreatedAt.UTC(), out.UpdatedAt.UTC()
	return nil
}

// dashboardWriteErr maps no row to ErrNotFound and a duplicate name to
// ErrConflict, and passes the quota error through.
func dashboardWriteErr(op string, err error) error {
	var quota *DashboardQuotaError
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &quota):
		return quota
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		return ErrConflict
	}
	return fmt.Errorf("state: %s dashboard: %w", op, err)
}
