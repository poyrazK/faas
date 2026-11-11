package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Failed password checks per email address, shared by every apid (ADR
// 20261111150000). Kept out of Store; apid type-asserts
// LoginFailureBudgetStore and falls back to its own process counts.

// LoginFailureBudget bounds failed password checks for one address: at most
// MaxFailures per fixed Window.
type LoginFailureBudget struct {
	MaxFailures int
	Window      time.Duration
}

// LoginFailureBudgetStore is implemented by MemStore and PgStore. Keys are
// keyed email hashes (auth.HashEmail), so unbound addresses count too.
type LoginFailureBudgetStore interface {
	// RecordLoginFailure counts one failure for key. It reports true when
	// this failure used up the budget and the owner was not told within
	// the window.
	RecordLoginFailure(ctx context.Context, key string, budget LoginFailureBudget) (notify bool, err error)
	// LoginFailureRetryAfter reports how long password checks for key stay
	// refused; zero when they may run.
	LoginFailureRetryAfter(ctx context.Context, key string, budget LoginFailureBudget) (time.Duration, error)
	// ResetLoginFailures forgets key's failures.
	ResetLoginFailures(ctx context.Context, key string) error
}

var (
	_ LoginFailureBudgetStore = (*MemStore)(nil)
	_ LoginFailureBudgetStore = (*PgStore)(nil)
)

func validLoginFailureInput(key string, budget LoginFailureBudget) error {
	if len(key) < 16 || len(key) > 128 {
		return errors.New("state: login failure key must be a keyed email hash")
	}
	if budget.MaxFailures <= 0 || budget.Window <= 0 {
		return fmt.Errorf("state: login failure budget must be positive, got %d per %s", budget.MaxFailures, budget.Window)
	}
	return nil
}

func clampLoginFailureMax(n int) int32 {
	return int32(min(n, 1_000_000)) //nolint:gosec // clamped
}

type memLoginFailures struct {
	windowStarted, notified time.Time
	failures                int
}

func (m *MemStore) RecordLoginFailure(_ context.Context, key string, budget LoginFailureBudget) (bool, error) {
	if err := validLoginFailureInput(key, budget); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.accountFlow.loginFailures == nil {
		m.accountFlow.loginFailures = map[string]*memLoginFailures{}
	}
	f := m.accountFlow.loginFailures[key]
	if f != nil && !f.windowStarted.After(now.Add(-budget.Window)) {
		f.windowStarted, f.failures = now, 0
	}
	if f == nil {
		f = &memLoginFailures{windowStarted: now}
		m.accountFlow.loginFailures[key] = f
	}
	f.failures++
	if f.failures < budget.MaxFailures || (!f.notified.IsZero() && f.notified.After(now.Add(-budget.Window))) {
		return false, nil
	}
	f.notified = now
	return true, nil
}

func (m *MemStore) LoginFailureRetryAfter(_ context.Context, key string, budget LoginFailureBudget) (time.Duration, error) {
	if err := validLoginFailureInput(key, budget); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.accountFlow.loginFailures[key]
	if f == nil || f.failures < budget.MaxFailures {
		return 0, nil
	}
	return max(time.Until(f.windowStarted.Add(budget.Window)), 0), nil
}

func (m *MemStore) ResetLoginFailures(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accountFlow.loginFailures, key)
	return nil
}

func (s *PgStore) RecordLoginFailure(ctx context.Context, key string, budget LoginFailureBudget) (bool, error) {
	if err := validLoginFailureInput(key, budget); err != nil {
		return false, err
	}
	q := sqlc.New()
	notify, err := q.RecordLoginFailure(ctx, s.pool, sqlc.RecordLoginFailureParams{
		EmailHash:     key,
		MaxFailures:   clampLoginFailureMax(budget.MaxFailures),
		WindowSeconds: budget.Window.Seconds(),
	})
	if err != nil {
		return false, fmt.Errorf("record login failure: %w", mapErr(err))
	}
	// Best effort: an unswept row only costs space until a later failure.
	_, _ = q.SweepLoginFailures(ctx, s.pool, budget.Window.Seconds())
	return notify, nil
}

func (s *PgStore) LoginFailureRetryAfter(ctx context.Context, key string, budget LoginFailureBudget) (time.Duration, error) {
	if err := validLoginFailureInput(key, budget); err != nil {
		return 0, err
	}
	secs, err := sqlc.New().LoginFailureRetryAfter(ctx, s.pool, sqlc.LoginFailureRetryAfterParams{
		EmailHash:     key,
		MaxFailures:   clampLoginFailureMax(budget.MaxFailures),
		WindowSeconds: budget.Window.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read login failure budget: %w", mapErr(err))
	}
	return max(time.Duration(secs*float64(time.Second)), 0), nil
}

func (s *PgStore) ResetLoginFailures(ctx context.Context, key string) error {
	if err := sqlc.New().ResetLoginFailures(ctx, s.pool, key); err != nil {
		return fmt.Errorf("reset login failures: %w", mapErr(err))
	}
	return nil
}
