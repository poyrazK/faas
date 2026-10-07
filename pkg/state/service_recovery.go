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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ServiceRecovery is scheduler-owned retry state, independent of customer intent.
// ClaimToken fences a worker that resumes after another scheduler reclaimed it.
type ServiceRecovery struct {
	AppID, Revision, ClaimToken, Status  string
	Failures                             int
	NextAttemptAt, LeaseUntil, UpdatedAt time.Time
}

func serviceRecoveryFromSQL(r sqlc.ServiceRecovery) ServiceRecovery {
	var token string
	if r.ClaimToken.Valid {
		token = uuid.UUID(r.ClaimToken.Bytes).String()
	}
	return ServiceRecovery{AppID: uuid.UUID(r.AppID.Bytes).String(), Revision: r.Revision,
		ClaimToken: token, Status: r.Status, Failures: int(r.Failures),
		NextAttemptAt: r.NextAttemptAt.Time, LeaseUntil: r.LeaseUntil.Time, UpdatedAt: r.UpdatedAt.Time}
}

func serviceRecoveryUUID(s string) pgtype.UUID {
	id, err := uuid.Parse(s)
	return pgtype.UUID{Bytes: id, Valid: err == nil}
}

func serviceRecoveryTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func (s *PgStore) ListServiceRecoveryApps(ctx context.Context, owner string, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	rows, err := sqlc.New().ListServiceRecoveryApps(ctx, s.pool, sqlc.ListServiceRecoveryAppsParams{
		OwnerNodeID: owner, SampledAt: serviceRecoveryTime(now), BatchLimit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("state: list service recovery: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, id := range rows {
		ids = append(ids, uuid.UUID(id.Bytes).String())
	}
	return ids, nil
}

func (s *PgStore) ClaimServiceRecovery(ctx context.Context, appID, revision, token string, now, until time.Time) (ServiceRecovery, bool, error) {
	if !validServiceRecoveryClaim(revision, token, now, until) {
		return ServiceRecovery{}, false, ErrInvalidArgument
	}
	r, err := sqlc.New().ClaimServiceRecovery(ctx, s.pool, sqlc.ClaimServiceRecoveryParams{
		AppID: serviceRecoveryUUID(appID), Revision: revision, ClaimToken: serviceRecoveryUUID(token),
		SampledAt: serviceRecoveryTime(now), LeaseUntil: serviceRecoveryTime(until)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceRecovery{}, false, nil
	}
	if err != nil {
		return ServiceRecovery{}, false, fmt.Errorf("state: claim service recovery: %w", err)
	}
	return serviceRecoveryFromSQL(r), true, nil
}

func (s *PgStore) CompleteServiceRecovery(ctx context.Context, token string, r ServiceRecovery) error {
	if !validServiceRecoveryCompletion(r) {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().CompleteServiceRecovery(ctx, s.pool, sqlc.CompleteServiceRecoveryParams{
		AppID: serviceRecoveryUUID(r.AppID), ClaimToken: serviceRecoveryUUID(token),
		Status: r.Status, Failures: int32(r.Failures), NextAttemptAt: serviceRecoveryTime(r.NextAttemptAt), UpdatedAt: serviceRecoveryTime(r.UpdatedAt)})
	if err != nil {
		return fmt.Errorf("state: complete service recovery: %w", err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ServiceRecoveryByApp(ctx context.Context, id string) (ServiceRecovery, error) {
	r, err := sqlc.New().ServiceRecoveryByApp(ctx, s.pool, serviceRecoveryUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceRecovery{}, ErrNotFound
	}
	if err != nil {
		return ServiceRecovery{}, fmt.Errorf("state: read service recovery: %w", err)
	}
	return serviceRecoveryFromSQL(r), nil
}

func (m *MemStore) ListServiceRecoveryApps(_ context.Context, owner string, now time.Time, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, app := range m.apps {
		if app.Status != AppActive || app.Manifest.ExecutionMode != api.ExecutionModeService || (owner != "" && app.NodeID != "" && app.NodeID != owner) {
			continue
		}
		account, ok := m.accounts[app.AccountID]
		if !ok || !account.Active() {
			continue
		}
		r := m.serviceRecovery[id]
		if r.LeaseUntil.After(now) || r.NextAttemptAt.After(now) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.serviceRecovery[ids[i]].NextAttemptAt, m.serviceRecovery[ids[j]].NextAttemptAt
		if !a.Equal(b) {
			return a.Before(b)
		}
		return ids[i] < ids[j]
	})
	if limit <= 0 {
		return []string{}, nil
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

func (m *MemStore) ClaimServiceRecovery(_ context.Context, id, revision, token string, now, until time.Time) (ServiceRecovery, bool, error) {
	if !validServiceRecoveryClaim(revision, token, now, until) {
		return ServiceRecovery{}, false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[id]
	if !ok {
		return ServiceRecovery{}, false, nil
	}
	if app.Status != AppActive || app.Manifest.ExecutionMode != api.ExecutionModeService {
		return ServiceRecovery{}, false, nil
	}
	account, ok := m.accounts[app.AccountID]
	if !ok || !account.Active() {
		return ServiceRecovery{}, false, nil
	}
	r := m.serviceRecovery[id]
	if r.LeaseUntil.After(now) || (r.Revision == revision && serviceRecoveryBackoff(r.Status) && r.NextAttemptAt.After(now)) {
		return ServiceRecovery{}, false, nil
	}
	if r.Revision != revision {
		r.Failures = 0
	}
	r.AppID, r.Revision, r.ClaimToken, r.Status = id, revision, token, "reconciling"
	r.LeaseUntil, r.UpdatedAt = until.UTC(), now.UTC()
	if r.NextAttemptAt.IsZero() {
		r.NextAttemptAt = now.UTC()
	}
	if m.serviceRecovery == nil {
		m.serviceRecovery = make(map[string]ServiceRecovery)
	}
	m.serviceRecovery[id] = r
	return r, true, nil
}

func serviceRecoveryBackoff(status string) bool {
	return status == "waiting_capacity" || status == "retrying_startup" || status == "waiting_dependency"
}

func validServiceRecoveryClaim(revision, token string, now, until time.Time) bool {
	_, err := uuid.Parse(token)
	return revision != "" && err == nil && token != uuid.Nil.String() && !now.IsZero() && until.After(now)
}

func validServiceRecoveryCompletion(r ServiceRecovery) bool {
	if r.Failures < 0 || r.Failures > api.ServiceRecoveryFailureCountMax || r.UpdatedAt.IsZero() || r.NextAttemptAt.Before(r.UpdatedAt) {
		return false
	}
	switch r.Status {
	case "ready", "starting", "draining", "rolling_out", "waiting_capacity", "retrying_startup", "waiting_dependency":
		return true
	default:
		return false
	}
}

func (m *MemStore) CompleteServiceRecovery(_ context.Context, token string, r ServiceRecovery) error {
	if !validServiceRecoveryCompletion(r) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.serviceRecovery[r.AppID]
	if !ok || old.ClaimToken != token || token == "" {
		return ErrConflict
	}
	old.Status, old.Failures, old.NextAttemptAt, old.UpdatedAt = r.Status, r.Failures, r.NextAttemptAt.UTC(), r.UpdatedAt.UTC()
	old.ClaimToken, old.LeaseUntil = "", time.Unix(0, 0).UTC()
	m.serviceRecovery[r.AppID] = old
	return nil
}

func (m *MemStore) ServiceRecoveryByApp(_ context.Context, id string) (ServiceRecovery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.serviceRecovery[id]
	if !ok {
		return ServiceRecovery{}, ErrNotFound
	}
	return r, nil
}
