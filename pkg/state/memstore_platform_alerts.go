package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

// platformAlertNoticeMeta is the delivery state PlatformAlertNotice does
// not carry.
type platformAlertNoticeMeta struct {
	status      string
	nextAttempt time.Time
	finishedAt  time.Time
	lastError   string
}

// QueuePlatformAlertNotice implements PlatformAlertNoticeStore.
func (m *MemStore) QueuePlatformAlertNotice(_ context.Context, n PlatformAlertNotice, cooldown time.Duration) (bool, error) {
	// Mirrors the table's NOT NULL and CHECK constraints.
	if n.AccountID == "" || n.AppID == "" || n.SubjectID == "" || len(n.SubjectID) > 128 ||
		!validPlatformAlertKind(n.Kind) {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queuePlatformAlertNoticeLocked(n, cooldown, time.Now()), nil
}

func (m *MemStore) queuePlatformAlertNoticeLocked(n PlatformAlertNotice, cooldown time.Duration, now time.Time) bool {
	for _, existing := range m.platformAlertNotices {
		if existing.Kind != n.Kind || existing.SubjectID != n.SubjectID {
			continue
		}
		if m.platformAlertMeta[existing.ID].status == PlatformAlertPending || now.Sub(existing.CreatedAt) < cooldown {
			return false
		}
	}
	if m.platformAlertMeta == nil {
		m.platformAlertMeta = map[string]platformAlertNoticeMeta{}
	}
	n.ID = uuid.NewString()
	n.CreatedAt = now
	n.Attempts = 0
	if len(n.Detail) == 0 {
		n.Detail = json.RawMessage(`{}`)
	}
	m.platformAlertNotices = append(m.platformAlertNotices, n)
	m.platformAlertMeta[n.ID] = platformAlertNoticeMeta{status: PlatformAlertPending, nextAttempt: now}
	return true
}

// ClaimPlatformAlertNotices implements PlatformAlertNoticeStore.
func (m *MemStore) ClaimPlatformAlertNotices(_ context.Context, limit int, lease time.Duration) ([]PlatformAlertNotice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var due []int
	for i, n := range m.platformAlertNotices {
		meta := m.platformAlertMeta[n.ID]
		if meta.status == PlatformAlertPending && !meta.nextAttempt.After(now) {
			due = append(due, i)
		}
	}
	sort.Slice(due, func(a, b int) bool {
		return m.platformAlertMeta[m.platformAlertNotices[due[a]].ID].nextAttempt.Before(m.platformAlertMeta[m.platformAlertNotices[due[b]].ID].nextAttempt)
	})
	var out []PlatformAlertNotice
	for _, i := range due {
		if len(out) >= limit {
			break
		}
		n := &m.platformAlertNotices[i]
		n.Attempts++
		meta := m.platformAlertMeta[n.ID]
		meta.nextAttempt = now.Add(lease)
		m.platformAlertMeta[n.ID] = meta
		out = append(out, *n)
	}
	return out, nil
}

// FinishPlatformAlertNotice implements PlatformAlertNoticeStore.
func (m *MemStore) FinishPlatformAlertNotice(_ context.Context, id, status, lastError string) error {
	if status != PlatformAlertSent && status != PlatformAlertSkipped && status != PlatformAlertFailed {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, ok := m.platformAlertMeta[id]
	if !ok || meta.status != PlatformAlertPending {
		return nil
	}
	meta.status, meta.finishedAt, meta.lastError = status, time.Now(), truncatePlatformAlertError(lastError)
	m.platformAlertMeta[id] = meta
	return nil
}

// RetryPlatformAlertNotice implements PlatformAlertNoticeStore.
func (m *MemStore) RetryPlatformAlertNotice(_ context.Context, id string, at time.Time, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, ok := m.platformAlertMeta[id]
	if !ok || meta.status != PlatformAlertPending {
		return nil
	}
	meta.nextAttempt, meta.lastError = at, truncatePlatformAlertError(lastError)
	m.platformAlertMeta[id] = meta
	return nil
}

// TrimPlatformAlertNotices implements PlatformAlertNoticeStore.
func (m *MemStore) TrimPlatformAlertNotices(_ context.Context, before time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.platformAlertNotices[:0]
	trimmed := 0
	for _, n := range m.platformAlertNotices {
		meta := m.platformAlertMeta[n.ID]
		if meta.status != PlatformAlertPending && meta.finishedAt.Before(before) {
			delete(m.platformAlertMeta, n.ID)
			trimmed++
			continue
		}
		kept = append(kept, n)
	}
	m.platformAlertNotices = kept
	return trimmed, nil
}

// PauseCronIfFailing implements CronFailurePauseStore; see PgStore.
func (m *MemStore) PauseCronIfFailing(_ context.Context, cronID string, runs int, span, cooldown time.Duration) (bool, error) {
	if runs <= 0 {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var lastPause time.Time
	for _, n := range m.platformAlertNotices {
		if n.Kind == PlatformAlertCronFailingPaused && n.SubjectID == cronID && n.CreatedAt.After(lastPause) {
			lastPause = n.CreatedAt
		}
	}
	var recent []Invocation
	for _, inv := range m.invocations {
		if inv.CronID == nil || *inv.CronID != cronID || inv.Source != InvocationCron || !inv.CreatedAt.After(lastPause) {
			continue
		}
		if inv.State == InvocationCompleted || ((inv.State == InvocationFailed || inv.State == InvocationDeadLetter) && inv.ReceivedAt != nil && !PlatformFailedBeforeApp(inv.LastError)) {
			recent = append(recent, inv)
		}
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].CreatedAt.After(recent[j].CreatedAt) })
	// The streak: every finished run newer than the newest success.
	for i, inv := range recent {
		if inv.State == InvocationCompleted {
			recent = recent[:i]
			break
		}
	}
	if len(recent) == 0 {
		return false, nil
	}
	oldest := recent[len(recent)-1].DueAt
	if !cronFailureStreakPauses(len(recent), &oldest, runs, span) {
		return false, nil
	}
	c, ok := m.crons[cronID]
	if !ok || !c.Enabled || c.SuspendedReason != "" || len(c.Command) > 0 {
		return false, nil
	}
	app, ok := m.apps[c.AppID]
	if !ok {
		return false, nil
	}
	c.SuspendedReason = CronSuspendedFailing
	m.crons[cronID] = c
	detail, _ := json.Marshal(cronFailingDetail{Path: c.Path, FailedRuns: len(recent), Since: oldest.UTC(), LastError: recent[0].LastError})
	m.queuePlatformAlertNoticeLocked(PlatformAlertNotice{AccountID: app.AccountID, AppID: c.AppID,
		Kind: PlatformAlertCronFailingPaused, SubjectID: cronID, Detail: detail}, cooldown, time.Now())
	return true, nil
}
