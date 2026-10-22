package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
)

// MemStore mirrors PgStore's ADR-732 fork exec rules.

func (m *MemStore) CreateAppForkExec(_ context.Context, params CreateAppForkExecParams) (AppForkExec, error) {
	p, err := validateCreateAppForkExec(params)
	if err != nil {
		return AppForkExec{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appForkExecs == nil {
		m.appForkExecs = make(map[string]AppForkExec)
	}
	fork, ok := m.appForks[p.ForkID]
	if !ok || fork.AppID != p.AppID || fork.AccountID != p.AccountID || fork.Status != AppForkRunning ||
		fork.CancelRequested != nil || !fork.ExpiresAt.After(p.CreatedAt) {
		return AppForkExec{}, ErrAppForkExecRefused
	}
	pending := 0
	for _, e := range m.appForkExecs {
		if e.ForkID == p.ForkID && !e.Status.Terminal() {
			pending++
		}
	}
	if pending >= p.MaxPending {
		return AppForkExec{}, ErrAppForkExecRefused
	}
	e := AppForkExec{
		ID: uuid.NewString(), ForkID: p.ForkID, AccountID: p.AccountID, AppID: p.AppID, RequestedBy: p.RequestedBy,
		Command: slices.Clone(p.Command), CommandShell: p.CommandShell, TimeoutSeconds: p.TimeoutSeconds,
		MaxOutputBytes: p.MaxOutputBytes, Status: AppForkExecQueued, Stdout: []byte{}, Stderr: []byte{},
		CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt,
	}
	m.appForkExecs[e.ID] = e
	return e, nil
}

func (m *MemStore) AppForkExecByID(_ context.Context, accountID, appID, forkID, execID string) (AppForkExec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.appForkExecs[execID]
	if !ok || e.AccountID != accountID || e.AppID != appID || e.ForkID != forkID {
		return AppForkExec{}, ErrNotFound
	}
	return e, nil
}

func (m *MemStore) ListAppForkExecs(_ context.Context, accountID, appID, forkID string, limit int) ([]AppForkExec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppForkExec, 0)
	for _, e := range m.appForkExecs {
		if e.AccountID == accountID && e.AppID == appID && e.ForkID == forkID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if limit = clampAppForkListLimit(limit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ClaimNextAppForkExec(_ context.Context, owner string, now time.Time) (AppForkExec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	running := map[string]bool{}
	for _, e := range m.appForkExecs {
		if e.Status == AppForkExecRunning {
			running[e.ForkID] = true
		}
	}
	var next AppForkExec
	found := false
	for _, e := range m.appForkExecs {
		fork, ok := m.appForks[e.ForkID]
		if e.Status != AppForkExecQueued || running[e.ForkID] || !ok || fork.Status != AppForkRunning ||
			fork.LeaseOwner == nil || *fork.LeaseOwner != owner {
			continue
		}
		if !found || e.CreatedAt.Before(next.CreatedAt) || (e.CreatedAt.Equal(next.CreatedAt) && e.ID < next.ID) {
			next, found = e, true
		}
	}
	if !found {
		return AppForkExec{}, ErrNotFound
	}
	at := memTime(now)
	next.Status, next.StartedAt = AppForkExecRunning, &at
	if at.After(next.UpdatedAt) {
		next.UpdatedAt = at
	}
	m.appForkExecs[next.ID] = next
	return next, nil
}

func (m *MemStore) FinishAppForkExec(_ context.Context, p FinishAppForkExecParams) (AppForkExec, error) {
	if err := p.validate(); err != nil {
		return AppForkExec{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.appForkExecs[p.ID]
	if !ok || e.Status != AppForkExecRunning {
		return AppForkExec{}, ErrNotFound
	}
	m.finishForkExecLocked(&e, p)
	return e, nil
}

func (m *MemStore) finishForkExecLocked(e *AppForkExec, p FinishAppForkExecParams) {
	at := memTime(p.FinishedAt)
	e.Status, e.ExitCode, e.OutputTruncated, e.FinishedAt = p.Status, p.ExitCode, p.OutputTruncated, &at
	e.Stdout, e.Stderr = nonNilBytes(slices.Clone(p.Stdout)), nonNilBytes(slices.Clone(p.Stderr))
	if p.FailureCode != "" {
		code, msg := p.FailureCode, p.FailureMessage
		e.FailureCode, e.FailureMessage = &code, &msg
	}
	if e.StartedAt == nil {
		e.StartedAt = &at
	}
	if at.After(e.UpdatedAt) {
		e.UpdatedAt = at
	}
	m.appForkExecs[e.ID] = *e
}

func (m *MemStore) FailOrphanedAppForkExecs(_ context.Context, grace time.Duration, now time.Time) ([]AppForkExec, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppForkExec, 0)
	for _, e := range m.appForkExecs {
		if e.Status.Terminal() {
			continue
		}
		fork, ok := m.appForks[e.ForkID]
		overdue := e.Status == AppForkExecRunning && e.StartedAt != nil &&
			e.StartedAt.Add(time.Duration(e.TimeoutSeconds)*time.Second+grace).Before(now)
		if ok && fork.Status == AppForkRunning && !overdue {
			continue
		}
		m.finishForkExecLocked(&e, FinishAppForkExecParams{
			ID: e.ID, Status: AppForkExecFailed, FailureCode: "fork_ended",
			FailureMessage: "the fork ended before the command finished", FinishedAt: now,
		})
		out = append(out, e)
	}
	return out, nil
}
