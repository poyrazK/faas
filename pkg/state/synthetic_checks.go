package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrSyntheticCheckLimit is returned when a create would exceed the app's cap.
var ErrSyntheticCheckLimit = errors.New("state: app synthetic check limit reached")

// SyntheticCheck is one scheduled HTTP check against an app (ADR-748).
// ExpectedStatus 0 means any 2xx.
type SyntheticCheck struct {
	ID              string
	AccountID       string
	AppID           string
	Name            string
	Method          string
	Path            string
	ExpectedStatus  int
	TimeoutMS       int
	IntervalSeconds int
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SyntheticCheckStore is implemented by PgStore and MemStore; optional on
// Store so older fakes keep compiling.
type SyntheticCheckStore interface {
	CreateSyntheticCheck(ctx context.Context, in SyntheticCheck, maxPerApp int) (SyntheticCheck, error)
	ListSyntheticChecks(ctx context.Context, appID string) ([]SyntheticCheck, error)
	GetSyntheticCheck(ctx context.Context, appID, id string) (SyntheticCheck, error)
	SetSyntheticCheckEnabled(ctx context.Context, appID, id string, enabled bool) (SyntheticCheck, error)
	DeleteSyntheticCheck(ctx context.Context, appID, id string) error
}

func syntheticCheckFromRow(row sqlc.SyntheticCheck) SyntheticCheck {
	return SyntheticCheck{
		ID:              uuid.UUID(row.ID.Bytes).String(),
		AccountID:       uuid.UUID(row.AccountID.Bytes).String(),
		AppID:           uuid.UUID(row.AppID.Bytes).String(),
		Name:            row.Name,
		Method:          row.Method,
		Path:            row.Path,
		ExpectedStatus:  int(row.ExpectedStatus.Int32),
		TimeoutMS:       int(row.TimeoutMs),
		IntervalSeconds: int(row.IntervalSeconds),
		Enabled:         row.Enabled,
		CreatedAt:       row.CreatedAt.Time,
		UpdatedAt:       row.UpdatedAt.Time,
	}
}

// syntheticKeys parses an (app, check) pair; a malformed check id reads as
// missing rather than invalid.
func syntheticKeys(appID, id string) (pgtype.UUID, pgtype.UUID, error) {
	app, err := alertPgUUID(appID)
	if err != nil || !app.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidArgument
	}
	key, err := alertPgUUID(id)
	if err != nil || !key.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, ErrNotFound
	}
	return app, key, nil
}

// CreateSyntheticCheck inserts a definition; the cap is part of the insert.
func (s *PgStore) CreateSyntheticCheck(ctx context.Context, in SyntheticCheck, maxPerApp int) (SyntheticCheck, error) {
	acct, err := alertPgUUID(in.AccountID)
	if err != nil {
		return SyntheticCheck{}, err
	}
	app, err := alertPgUUID(in.AppID)
	if err != nil {
		return SyntheticCheck{}, err
	}
	status := pgtype.Int4{}
	if in.ExpectedStatus != 0 {
		status = pgtype.Int4{Int32: int32(in.ExpectedStatus), Valid: true} //nolint:gosec // bounded by synthetic_checks_status_chk
	}
	row, err := sqlc.New().InsertSyntheticCheck(ctx, s.pool, sqlc.InsertSyntheticCheckParams{
		AccountID: acct, AppID: app, Name: in.Name, Method: in.Method, Path: in.Path, ExpectedStatus: status,
		TimeoutMs: int32(in.TimeoutMS), IntervalSeconds: int32(in.IntervalSeconds), MaxPerApp: int32(maxPerApp), //nolint:gosec // bounded by CHECKs and limits.go
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SyntheticCheck{}, ErrSyntheticCheckLimit
	}
	if err != nil {
		return SyntheticCheck{}, fmt.Errorf("state: create synthetic check %q for app %s: %w", in.Name, in.AppID, mapErr(err))
	}
	return syntheticCheckFromRow(row), nil
}

// ListSyntheticChecks returns an app's checks in name order.
func (s *PgStore) ListSyntheticChecks(ctx context.Context, appID string) ([]SyntheticCheck, error) {
	app, err := alertPgUUID(appID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListSyntheticChecks(ctx, s.pool, app)
	if err != nil {
		return nil, fmt.Errorf("state: list synthetic checks for app %s: %w", appID, err)
	}
	out := make([]SyntheticCheck, 0, len(rows))
	for _, row := range rows {
		out = append(out, syntheticCheckFromRow(row))
	}
	return out, nil
}

// GetSyntheticCheck returns one check scoped to its app.
func (s *PgStore) GetSyntheticCheck(ctx context.Context, appID, id string) (SyntheticCheck, error) {
	app, key, err := syntheticKeys(appID, id)
	if err != nil {
		return SyntheticCheck{}, err
	}
	row, err := sqlc.New().GetSyntheticCheck(ctx, s.pool, sqlc.GetSyntheticCheckParams{AppID: app, ID: key})
	if err != nil {
		return SyntheticCheck{}, mapErr(err)
	}
	return syntheticCheckFromRow(row), nil
}

// SetSyntheticCheckEnabled pauses or resumes a check.
func (s *PgStore) SetSyntheticCheckEnabled(ctx context.Context, appID, id string, enabled bool) (SyntheticCheck, error) {
	app, key, err := syntheticKeys(appID, id)
	if err != nil {
		return SyntheticCheck{}, err
	}
	row, err := sqlc.New().SetSyntheticCheckEnabled(ctx, s.pool, sqlc.SetSyntheticCheckEnabledParams{Enabled: enabled, AppID: app, ID: key})
	if err != nil {
		return SyntheticCheck{}, mapErr(err)
	}
	return syntheticCheckFromRow(row), nil
}

// DeleteSyntheticCheck removes one check; ErrNotFound when absent.
func (s *PgStore) DeleteSyntheticCheck(ctx context.Context, appID, id string) error {
	app, key, err := syntheticKeys(appID, id)
	if err != nil {
		return err
	}
	n, err := sqlc.New().DeleteSyntheticCheck(ctx, s.pool, sqlc.DeleteSyntheticCheckParams{AppID: app, ID: key})
	if err != nil {
		return fmt.Errorf("state: delete synthetic check %s for app %s: %w", id, appID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateSyntheticCheck mirrors PgStore: cap, then unique name per app.
func (m *MemStore) CreateSyntheticCheck(_ context.Context, in SyntheticCheck, maxPerApp int) (SyntheticCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.syntheticChecks == nil {
		m.syntheticChecks = map[string][]SyntheticCheck{}
	}
	existing := m.syntheticChecks[in.AppID]
	if len(existing) >= maxPerApp {
		return SyntheticCheck{}, ErrSyntheticCheckLimit
	}
	for _, c := range existing {
		if c.Name == in.Name {
			return SyntheticCheck{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	in.ID, in.Enabled, in.CreatedAt, in.UpdatedAt = uuid.NewString(), true, now, now
	m.syntheticChecks[in.AppID] = append(existing, in)
	return in, nil
}

// ListSyntheticChecks returns the app's checks in name order.
func (m *MemStore) ListSyntheticChecks(_ context.Context, appID string) ([]SyntheticCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]SyntheticCheck(nil), m.syntheticChecks[appID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetSyntheticCheck returns one check scoped to its app.
func (m *MemStore) GetSyntheticCheck(_ context.Context, appID, id string) (SyntheticCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.syntheticChecks[appID] {
		if c.ID == id {
			return c, nil
		}
	}
	return SyntheticCheck{}, ErrNotFound
}

// SetSyntheticCheckEnabled pauses or resumes a check.
func (m *MemStore) SetSyntheticCheckEnabled(_ context.Context, appID, id string, enabled bool) (SyntheticCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, c := range m.syntheticChecks[appID] {
		if c.ID == id {
			c.Enabled, c.UpdatedAt = enabled, time.Now().UTC()
			m.syntheticChecks[appID][i] = c
			return c, nil
		}
	}
	return SyntheticCheck{}, ErrNotFound
}

// DeleteSyntheticCheck removes one check scoped to its app.
func (m *MemStore) DeleteSyntheticCheck(_ context.Context, appID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	checks := m.syntheticChecks[appID]
	for i, c := range checks {
		if c.ID == id {
			m.syntheticChecks[appID] = append(checks[:i:i], checks[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

var (
	_ SyntheticCheckStore = (*PgStore)(nil)
	_ SyntheticCheckStore = (*MemStore)(nil)
)
