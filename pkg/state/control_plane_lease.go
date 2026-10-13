package state

// Control-plane singleton leases (ADR-1152 §4).
//
// A cell runs its control plane on three nodes, but some work must run on
// exactly one of them: the central scheduler's cron and placement, meterd's
// sampling and billing, and similar loops. Each such singleton holds a named
// row in control_plane_leases. The holder renews it before it expires; another
// node takes over only after the row has expired by the database clock, never
// by its own clock. Every takeover draws a new epoch from one sequence, so
// work started under an older epoch can be refused with ControlPlaneLeaseHeld.
//
// The lease lives in the cell's Postgres on purpose: the fencing check must
// commit with the write it guards (ADR-1152, rejected alternatives).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ControlPlaneLease is one held lease as the database recorded it.
// CheckedAt is the database clock at acquisition, so ExpiresAt.Sub(CheckedAt)
// is the remaining time independent of the caller's clock.
type ControlPlaneLease struct {
	Name       string
	Holder     string
	Epoch      int64
	AcquiredAt time.Time
	ExpiresAt  time.Time
	CheckedAt  time.Time
}

// ControlPlaneLeaseStore is the narrow surface pkg/singleton needs.
type ControlPlaneLeaseStore interface {
	// AcquireControlPlaneLease acquires name for holder, or renews it when
	// holder already holds it. held=false means another holder's unexpired
	// row exists. A takeover, or re-acquisition after holder's own row
	// expired, returns a new epoch.
	AcquireControlPlaneLease(ctx context.Context, name, holder string, ttl time.Duration) (lease ControlPlaneLease, held bool, err error)
	// ReleaseControlPlaneLease gives the lease up early so a standby can take
	// over without waiting for expiry. Releasing a lease that is no longer
	// held by holder at epoch is a no-op.
	ReleaseControlPlaneLease(ctx context.Context, name, holder string, epoch int64) error
	// ControlPlaneLeaseHeld is the fencing check: true only while epoch is the
	// current, unexpired epoch of name.
	ControlPlaneLeaseHeld(ctx context.Context, name string, epoch int64) (bool, error)
}

var errControlPlaneLeaseArgs = errors.New("state: control-plane lease needs a name, a holder and a TTL of at least one whole second")

func validControlPlaneLeaseArgs(name, holder string, ttl time.Duration) error {
	if name == "" || holder == "" || ttl < time.Second || ttl%time.Second != 0 {
		return errControlPlaneLeaseArgs
	}
	return nil
}

func (s *PgStore) AcquireControlPlaneLease(ctx context.Context, name, holder string, ttl time.Duration) (ControlPlaneLease, bool, error) {
	if err := validControlPlaneLeaseArgs(name, holder, ttl); err != nil {
		return ControlPlaneLease{}, false, err
	}
	row, err := sqlc.New().AcquireControlPlaneLease(ctx, s.pool, sqlc.AcquireControlPlaneLeaseParams{
		Name: name, Holder: holder, TtlSeconds: int64(ttl / time.Second),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ControlPlaneLease{}, false, nil
	}
	if err != nil {
		return ControlPlaneLease{}, false, fmt.Errorf("state: acquire control-plane lease %s: %w", name, err)
	}
	return ControlPlaneLease{
		Name: name, Holder: holder, Epoch: row.Epoch,
		AcquiredAt: row.AcquiredAt.Time, ExpiresAt: row.ExpiresAt.Time, CheckedAt: row.CheckedAt.Time,
	}, true, nil
}

func (s *PgStore) ReleaseControlPlaneLease(ctx context.Context, name, holder string, epoch int64) error {
	if _, err := sqlc.New().ReleaseControlPlaneLease(ctx, s.pool, sqlc.ReleaseControlPlaneLeaseParams{
		Name: name, Holder: holder, Epoch: epoch,
	}); err != nil {
		return fmt.Errorf("state: release control-plane lease %s: %w", name, err)
	}
	return nil
}

func (s *PgStore) ControlPlaneLeaseHeld(ctx context.Context, name string, epoch int64) (bool, error) {
	held, err := sqlc.New().ControlPlaneLeaseHeld(ctx, s.pool, sqlc.ControlPlaneLeaseHeldParams{Name: name, Epoch: epoch})
	if err != nil {
		return false, fmt.Errorf("state: check control-plane lease %s: %w", name, err)
	}
	return held, nil
}

// MemStore mirrors the SQL with time.Now as the database clock.

func (m *MemStore) AcquireControlPlaneLease(_ context.Context, name, holder string, ttl time.Duration) (ControlPlaneLease, bool, error) {
	if err := validControlPlaneLeaseArgs(name, holder, ttl); err != nil {
		return ControlPlaneLease{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.controlPlaneLeases == nil {
		m.controlPlaneLeases = map[string]ControlPlaneLease{}
	}
	now := time.Now()
	cur, exists := m.controlPlaneLeases[name]
	live := exists && cur.ExpiresAt.After(now)
	if live && cur.Holder != holder {
		return ControlPlaneLease{}, false, nil
	}
	next := ControlPlaneLease{Name: name, Holder: holder, ExpiresAt: now.Add(ttl), CheckedAt: now}
	if live {
		next.Epoch, next.AcquiredAt = cur.Epoch, cur.AcquiredAt
	} else {
		m.controlPlaneLeaseEpoch++
		next.Epoch, next.AcquiredAt = m.controlPlaneLeaseEpoch, now
	}
	m.controlPlaneLeases[name] = next
	return next, true, nil
}

func (m *MemStore) ReleaseControlPlaneLease(_ context.Context, name, holder string, epoch int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.controlPlaneLeases[name]; ok && cur.Holder == holder && cur.Epoch == epoch {
		delete(m.controlPlaneLeases, name)
	}
	return nil
}

func (m *MemStore) ControlPlaneLeaseHeld(_ context.Context, name string, epoch int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.controlPlaneLeases[name]
	return ok && cur.Epoch == epoch && cur.ExpiresAt.After(time.Now()), nil
}

var _ ControlPlaneLeaseStore = (*PgStore)(nil)
var _ ControlPlaneLeaseStore = (*MemStore)(nil)
