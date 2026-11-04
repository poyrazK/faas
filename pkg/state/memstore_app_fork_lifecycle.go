package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

// MemStore mirrors PgStore's app_forks lifecycle: the same claim order,
// lease guards, terminal rules and sweeps.

func memTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// sortedAppForksLocked returns forks matching keep, ordered by key then id.
func (m *MemStore) sortedAppForksLocked(keep func(AppFork) bool, key func(AppFork) time.Time) []AppFork {
	out := make([]AppFork, 0)
	for _, fork := range m.appForks {
		if keep(fork) {
			out = append(out, fork)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := key(out[i]), key(out[j]); !a.Equal(b) {
			return a.Before(b)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *MemStore) leaseAppForkLocked(fork AppFork, owner string, now time.Time, lease time.Duration) AppFork {
	token, expires := uuid.NewString(), memTime(now.Add(lease))
	fork.LeaseToken, fork.LeaseOwner, fork.LeaseExpiresAt = &token, &owner, &expires
	if at := memTime(now); at.After(fork.UpdatedAt) {
		fork.UpdatedAt = at
	}
	m.appForks[fork.ID] = fork
	return fork
}

// heldAppForkLocked returns the fork only when leaseToken holds it in one of
// the given statuses.
func (m *MemStore) heldAppForkLocked(forkID, leaseToken string, statuses ...AppForkStatus) (AppFork, error) {
	fork, ok := m.appForks[forkID]
	if !ok || fork.LeaseToken == nil || *fork.LeaseToken != leaseToken {
		return AppFork{}, ErrAppForkLeaseLost
	}
	for _, status := range statuses {
		if fork.Status == status {
			return fork, nil
		}
	}
	return AppFork{}, ErrAppForkLeaseLost
}

func (m *MemStore) ClaimNextAppFork(_ context.Context, owner string, now time.Time, lease time.Duration) (AppFork, error) {
	if err := validLease(owner, now, lease); err != nil {
		return AppFork{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	queued := m.sortedAppForksLocked(func(f AppFork) bool {
		return f.Status == AppForkQueued && f.CancelRequested == nil && f.ExpiresAt.After(now) && m.crashCaptureClaimableLocked(f)
	}, func(f AppFork) time.Time { return f.CreatedAt })
	if len(queued) == 0 {
		return AppFork{}, ErrNotFound
	}
	fork := queued[0]
	fork.Status = AppForkRestoring
	return m.leaseAppForkLocked(fork, owner, now, lease), nil
}

func (m *MemStore) RenewAppForkLease(_ context.Context, forkID, leaseToken string, now time.Time, lease time.Duration) (AppFork, error) {
	if forkID == "" || leaseToken == "" || now.IsZero() || lease <= 0 {
		return AppFork{}, ErrAppForkInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fork, err := m.heldAppForkLocked(forkID, leaseToken, AppForkRestoring, AppForkRunning)
	if err != nil {
		return AppFork{}, err
	}
	expires := memTime(now.Add(lease))
	fork.LeaseExpiresAt = &expires
	if at := memTime(now); at.After(fork.UpdatedAt) {
		fork.UpdatedAt = at
	}
	m.appForks[forkID] = fork
	return fork, nil
}

func (m *MemStore) MarkAppForkRunning(_ context.Context, forkID, leaseToken, snapshotID, instanceID string, now time.Time) (AppFork, error) {
	if forkID == "" || leaseToken == "" || snapshotID == "" || instanceID == "" || now.IsZero() {
		return AppFork{}, ErrAppForkInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fork, err := m.heldAppForkLocked(forkID, leaseToken, AppForkRestoring)
	if err != nil {
		return AppFork{}, err
	}
	at := memTime(now)
	fork.Status, fork.SnapshotID, fork.InstanceID, fork.StartedAt = AppForkRunning, &snapshotID, &instanceID, &at
	if at.After(fork.UpdatedAt) {
		fork.UpdatedAt = at
	}
	m.appForks[forkID] = fork
	return fork, nil
}

func (m *MemStore) FinishAppFork(_ context.Context, p FinishAppForkParams) (AppFork, error) {
	if err := p.validate(); err != nil {
		return AppFork{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fork, err := m.heldAppForkLocked(p.ForkID, p.LeaseToken, AppForkRestoring, AppForkRunning)
	if err != nil {
		return AppFork{}, err
	}
	at := memTime(p.FinishedAt)
	fork.Status, fork.FinishedAt = p.Status, &at
	fork.LeaseToken, fork.LeaseOwner, fork.LeaseExpiresAt = nil, nil, nil
	if p.FailureCode != "" {
		code, msg := p.FailureCode, p.FailureMessage
		fork.FailureCode, fork.FailureMessage = &code, &msg
	}
	if at.After(fork.UpdatedAt) {
		fork.UpdatedAt = at
	}
	m.appForks[p.ForkID] = fork
	return fork, nil
}

func (m *MemStore) ExpireUnclaimedAppForks(_ context.Context, now time.Time) ([]AppFork, error) {
	if now.IsZero() {
		return nil, ErrAppForkInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	at := memTime(now)
	out := make([]AppFork, 0)
	for id, fork := range m.appForks {
		if fork.Status != AppForkQueued || (fork.ExpiresAt.After(now) && fork.CancelRequested == nil) {
			continue
		}
		fork.Status = AppForkExpired
		if fork.CancelRequested != nil {
			fork.Status = AppForkCancelled
		}
		fork.FinishedAt = &at
		if at.After(fork.UpdatedAt) {
			fork.UpdatedAt = at
		}
		m.appForks[id] = fork
		out = append(out, fork)
	}
	return out, nil
}

func (m *MemStore) AppForksDueForTeardown(_ context.Context, owner string, now time.Time, limit int) ([]AppFork, error) {
	if owner == "" || now.IsZero() {
		return nil, ErrAppForkInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	due := m.sortedAppForksLocked(func(f AppFork) bool {
		return f.LeaseOwner != nil && *f.LeaseOwner == owner &&
			(f.Status == AppForkRestoring || f.Status == AppForkRunning) &&
			(!f.ExpiresAt.After(now) || f.CancelRequested != nil)
	}, func(f AppFork) time.Time { return f.ExpiresAt })
	if limit = clampAppForkListLimit(limit); len(due) > limit {
		due = due[:limit]
	}
	return due, nil
}

func (m *MemStore) TakeOverAbandonedAppFork(_ context.Context, owner string, now time.Time, lease time.Duration) (AppFork, error) {
	if err := validLease(owner, now, lease); err != nil {
		return AppFork{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	abandoned := m.sortedAppForksLocked(func(f AppFork) bool {
		return (f.Status == AppForkRestoring || f.Status == AppForkRunning) &&
			f.LeaseExpiresAt != nil && f.LeaseExpiresAt.Before(now)
	}, func(f AppFork) time.Time { return *f.LeaseExpiresAt })
	if len(abandoned) == 0 {
		return AppFork{}, ErrNotFound
	}
	return m.leaseAppForkLocked(abandoned[0], owner, now, lease), nil
}
