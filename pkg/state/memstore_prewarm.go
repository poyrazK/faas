package state

import (
	"context"
	"sort"
	"time"
)

func clonePrewarmIntent(in PrewarmIntent) PrewarmIntent {
	out := in
	if in.ClaimedAt != nil {
		v := *in.ClaimedAt
		out.ClaimedAt = &v
	}
	if in.FiredAt != nil {
		v := *in.FiredAt
		out.FiredAt = &v
	}
	return out
}

func (m *MemStore) CreatePrewarmIntent(_ context.Context, appID, accountID string, count int, wakeAt, expiresAt time.Time, trigger string) (PrewarmIntent, error) {
	if err := ValidatePrewarmIntent(count, wakeAt, expiresAt, time.Now(), trigger); err != nil {
		return PrewarmIntent{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted || app.AccountID != accountID {
		return PrewarmIntent{}, ErrNotFound
	}
	now := time.Now().UTC()
	intent := PrewarmIntent{
		ID: newID(), AppID: appID, AccountID: accountID, Count: count,
		WakeAt: wakeAt.UTC(), ExpiresAt: expiresAt.UTC(), Trigger: trigger,
		Status: PrewarmStatusPending, CreatedAt: now,
	}
	m.prewarmIntents[intent.ID] = intent
	return clonePrewarmIntent(intent), nil
}

func (m *MemStore) PrewarmIntentByID(_ context.Context, id string) (PrewarmIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	intent, ok := m.prewarmIntents[id]
	if !ok {
		return PrewarmIntent{}, ErrNotFound
	}
	return clonePrewarmIntent(intent), nil
}

func (m *MemStore) ListPrewarmIntentsForApp(_ context.Context, appID string, limit int) ([]PrewarmIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := make([]PrewarmIntent, 0)
	for _, intent := range m.prewarmIntents {
		if intent.AppID == appID {
			out = append(out, clonePrewarmIntent(intent))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WakeAt.Equal(out[j].WakeAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].WakeAt.Before(out[j].WakeAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ListDuePrewarmIntents(_ context.Context, before, now time.Time, limit int) ([]PrewarmIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := make([]PrewarmIntent, 0)
	for _, intent := range m.prewarmIntents {
		if intent.Status != PrewarmStatusPending || intent.WakeAt.After(before) || !intent.ExpiresAt.After(now) {
			continue
		}
		out = append(out, clonePrewarmIntent(intent))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WakeAt.Equal(out[j].WakeAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].WakeAt.Before(out[j].WakeAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ListExpiredPrewarmIntents(_ context.Context, now time.Time, limit int) ([]PrewarmIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := make([]PrewarmIntent, 0)
	for _, intent := range m.prewarmIntents {
		if intent.Status == PrewarmStatusPending && !intent.ExpiresAt.After(now) {
			out = append(out, clonePrewarmIntent(intent))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExpiresAt.Equal(out[j].ExpiresAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ExpiresAt.Before(out[j].ExpiresAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) ActivePrewarmFloor(_ context.Context, appID string, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	floor := 0
	for _, intent := range m.prewarmIntents {
		if intent.AppID != appID || !intent.ExpiresAt.After(now) {
			continue
		}
		if intent.Status != PrewarmStatusRunning && intent.Status != PrewarmStatusSucceeded {
			continue
		}
		candidate := intent.Count
		if intent.Status == PrewarmStatusSucceeded {
			candidate = intent.AdmittedCount
		}
		if candidate > floor {
			floor = candidate
		}
	}
	return floor, nil
}

func (m *MemStore) ClaimPrewarmIntent(_ context.Context, id string, claimedAt time.Time) (PrewarmIntent, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	intent, ok := m.prewarmIntents[id]
	if !ok {
		return PrewarmIntent{}, false, ErrNotFound
	}
	if intent.Status != PrewarmStatusPending || !intent.ExpiresAt.After(claimedAt) {
		return clonePrewarmIntent(intent), false, nil
	}
	claimedAt = claimedAt.UTC()
	intent.Status = PrewarmStatusRunning
	intent.ClaimedAt = &claimedAt
	m.prewarmIntents[id] = intent
	return clonePrewarmIntent(intent), true, nil
}

func (m *MemStore) CompletePrewarmIntent(_ context.Context, id string, firedAt time.Time, admittedCount int, outcome string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	intent, ok := m.prewarmIntents[id]
	if !ok {
		return ErrNotFound
	}
	if intent.Status != PrewarmStatusRunning {
		return nil
	}
	firedAt = firedAt.UTC()
	intent.Status = PrewarmStatusSucceeded
	intent.FiredAt = &firedAt
	intent.AdmittedCount = admittedCount
	intent.Outcome = outcome
	m.prewarmIntents[id] = intent
	return nil
}

func (m *MemStore) FailPrewarmIntent(_ context.Context, id string, firedAt time.Time, cause string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	intent, ok := m.prewarmIntents[id]
	if !ok {
		return ErrNotFound
	}
	if intent.Status != PrewarmStatusRunning {
		return nil
	}
	firedAt = firedAt.UTC()
	intent.Status = PrewarmStatusFailed
	intent.FiredAt = &firedAt
	intent.LastError = cause
	m.prewarmIntents[id] = intent
	return nil
}

func (m *MemStore) ExpirePrewarmIntent(_ context.Context, id string, expiredAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	intent, ok := m.prewarmIntents[id]
	if !ok {
		return false, ErrNotFound
	}
	if intent.Status != PrewarmStatusPending || intent.ExpiresAt.After(expiredAt) {
		return false, nil
	}
	expiredAt = expiredAt.UTC()
	intent.Status = PrewarmStatusFailed
	intent.FiredAt = &expiredAt
	intent.Outcome = "expired"
	intent.LastError = "expired"
	m.prewarmIntents[id] = intent
	return true, nil
}

func (m *MemStore) CancelPrewarmIntent(_ context.Context, id, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	intent, ok := m.prewarmIntents[id]
	if !ok || intent.AccountID != accountID {
		return ErrNotFound
	}
	// Once schedd has claimed the row, admission is in flight and cannot be
	// rolled back safely. Keep cancellation pending-only so a DELETE cannot
	// race a live restore and leave instances without their temporary floor.
	if intent.Status != PrewarmStatusPending {
		return ErrNotFound
	}
	intent.Status = PrewarmStatusCancelled
	m.prewarmIntents[id] = intent
	return nil
}
