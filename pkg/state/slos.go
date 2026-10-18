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

// SLI kinds for customer-defined SLOs (ADR-747); mirror app_slos_sli_chk.
const (
	SLIAvailability = "availability"
	SLILatency      = "latency"
)

// ErrSLOLimit is returned when creating an SLO would exceed the app's cap.
var ErrSLOLimit = errors.New("state: app SLO limit reached")

// SLO is one customer-defined service level objective (ADR-747).
// ObjectiveBP is the target in basis points of a percent: 9990 = 99.90%.
// LatencyThresholdMS is set only for latency SLIs.
type SLO struct {
	ID                 string
	AccountID          string
	AppID              string
	Name               string
	SLI                string
	LatencyThresholdMS int
	ObjectiveBP        int
	WindowDays         int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// SLOStore is implemented by PgStore and MemStore. It is optional on the
// wider Store so test fakes that predate ADR-747 keep compiling; callers
// type-assert and treat its absence as unavailable.
type SLOStore interface {
	CreateSLO(ctx context.Context, in SLO, maxPerApp int) (SLO, error)
	ListSLOs(ctx context.Context, appID string) ([]SLO, error)
	GetSLO(ctx context.Context, appID, id string) (SLO, error)
	DeleteSLO(ctx context.Context, appID, id string) error
}

func sloFromRow(row sqlc.AppSlo) SLO {
	return SLO{
		ID:                 uuid.UUID(row.ID.Bytes).String(),
		AccountID:          uuid.UUID(row.AccountID.Bytes).String(),
		AppID:              uuid.UUID(row.AppID.Bytes).String(),
		Name:               row.Name,
		SLI:                row.Sli,
		LatencyThresholdMS: int(row.LatencyThresholdMs.Int32),
		ObjectiveBP:        int(row.ObjectiveBp),
		WindowDays:         int(row.WindowDays),
		CreatedAt:          row.CreatedAt.Time,
		UpdatedAt:          row.UpdatedAt.Time,
	}
}

func sloKeys(appID, id string) (pgtype.UUID, pgtype.UUID, error) {
	app, err := alertPgUUID(appID)
	if err != nil || !app.Valid {
		return pgtype.UUID{}, pgtype.UUID{}, ErrInvalidArgument
	}
	key, err := alertPgUUID(id)
	if err != nil || !key.Valid {
		// A malformed id cannot name a row; answer like a missing one.
		return pgtype.UUID{}, pgtype.UUID{}, ErrNotFound
	}
	return app, key, nil
}

// CreateSLO inserts a definition. The cap is part of the insert statement,
// so concurrent creates cannot overshoot it; zero rows back means the cap
// refused the row. A duplicate name maps to ErrConflict.
func (s *PgStore) CreateSLO(ctx context.Context, in SLO, maxPerApp int) (SLO, error) {
	acct, err := alertPgUUID(in.AccountID)
	if err != nil {
		return SLO{}, err
	}
	app, err := alertPgUUID(in.AppID)
	if err != nil {
		return SLO{}, err
	}
	threshold := pgtype.Int4{}
	if in.SLI == SLILatency {
		threshold = pgtype.Int4{Int32: int32(in.LatencyThresholdMS), Valid: true} //nolint:gosec // bounded by app_slos_latency_chk
	}
	row, err := sqlc.New().InsertAppSLO(ctx, s.pool, sqlc.InsertAppSLOParams{
		AccountID: acct, AppID: app, Name: in.Name, Sli: in.SLI, LatencyThresholdMs: threshold,
		ObjectiveBp: int32(in.ObjectiveBP), WindowDays: int32(in.WindowDays), MaxPerApp: int32(maxPerApp), //nolint:gosec // bounded by CHECKs and limits.go
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SLO{}, ErrSLOLimit
	}
	if err != nil {
		return SLO{}, fmt.Errorf("state: create SLO %q for app %s: %w", in.Name, in.AppID, mapErr(err))
	}
	return sloFromRow(row), nil
}

// ListSLOs returns an app's definitions in name order.
func (s *PgStore) ListSLOs(ctx context.Context, appID string) ([]SLO, error) {
	app, err := alertPgUUID(appID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListAppSLOs(ctx, s.pool, app)
	if err != nil {
		return nil, fmt.Errorf("state: list SLOs for app %s: %w", appID, err)
	}
	out := make([]SLO, 0, len(rows))
	for _, row := range rows {
		out = append(out, sloFromRow(row))
	}
	return out, nil
}

// GetSLO returns one definition, scoped to its app so an id from another
// app reads as missing.
func (s *PgStore) GetSLO(ctx context.Context, appID, id string) (SLO, error) {
	app, key, err := sloKeys(appID, id)
	if err != nil {
		return SLO{}, err
	}
	row, err := sqlc.New().GetAppSLO(ctx, s.pool, sqlc.GetAppSLOParams{AppID: app, ID: key})
	if err != nil {
		return SLO{}, mapErr(err)
	}
	return sloFromRow(row), nil
}

// DeleteSLO removes one definition; ErrNotFound when the app has no such SLO.
func (s *PgStore) DeleteSLO(ctx context.Context, appID, id string) error {
	app, key, err := sloKeys(appID, id)
	if err != nil {
		return err
	}
	n, err := sqlc.New().DeleteAppSLO(ctx, s.pool, sqlc.DeleteAppSLOParams{AppID: app, ID: key})
	if err != nil {
		return fmt.Errorf("state: delete SLO %s for app %s: %w", id, appID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateSLO mirrors PgStore: cap, then unique name per app.
func (m *MemStore) CreateSLO(_ context.Context, in SLO, maxPerApp int) (SLO, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appSLOs == nil {
		m.appSLOs = map[string][]SLO{}
	}
	existing := m.appSLOs[in.AppID]
	if len(existing) >= maxPerApp {
		return SLO{}, ErrSLOLimit
	}
	for _, slo := range existing {
		if slo.Name == in.Name {
			return SLO{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	in.ID, in.CreatedAt, in.UpdatedAt = uuid.NewString(), now, now
	if in.SLI != SLILatency {
		in.LatencyThresholdMS = 0
	}
	m.appSLOs[in.AppID] = append(existing, in)
	return in, nil
}

// ListSLOs returns the app's definitions in name order, matching PgStore.
func (m *MemStore) ListSLOs(_ context.Context, appID string) ([]SLO, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]SLO(nil), m.appSLOs[appID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetSLO returns one definition scoped to its app.
func (m *MemStore) GetSLO(_ context.Context, appID, id string) (SLO, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, slo := range m.appSLOs[appID] {
		if slo.ID == id {
			return slo, nil
		}
	}
	return SLO{}, ErrNotFound
}

// DeleteSLO removes one definition scoped to its app.
func (m *MemStore) DeleteSLO(_ context.Context, appID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	slos := m.appSLOs[appID]
	for i, slo := range slos {
		if slo.ID == id {
			m.appSLOs[appID] = append(slos[:i:i], slos[i+1:]...)
			delete(m.sloHours, id) // ON DELETE CASCADE
			return nil
		}
	}
	return ErrNotFound
}

var (
	_ SLOStore = (*PgStore)(nil)
	_ SLOStore = (*MemStore)(nil)
)
