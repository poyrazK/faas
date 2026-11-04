package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BuildMinutesStore reads the build time a billing subject's source builds
// ran, for the monthly build-minute quota (ADR-20261103131742 amendment 2).
type BuildMinutesStore interface {
	// BillingSubjectBuildSeconds is the build time the subject's builds that
	// started in [start, end) ran: each until it finished, or until now while
	// it runs.
	BillingSubjectBuildSeconds(ctx context.Context, subjectID string, start, end time.Time) (int64, error)
}

var (
	_ BuildMinutesStore = (*PgStore)(nil)
	_ BuildMinutesStore = (*MemStore)(nil)
)

// BillingSubjectBuildSeconds is the build time the subject's builds that
// started in [start, end) ran.
func (s *PgStore) BillingSubjectBuildSeconds(ctx context.Context, subjectID string, start, end time.Time) (int64, error) {
	id, err := parsePgUUID(subjectID)
	if err != nil {
		return 0, err
	}
	seconds, err := sqlc.New().BillingSubjectBuildSeconds(ctx, s.pool, sqlc.BillingSubjectBuildSecondsParams{
		SubjectID: id, PeriodStart: pgTime(start), PeriodEnd: pgTime(end),
	})
	if err != nil {
		return 0, fmt.Errorf("state: billing subject build seconds: %w", err)
	}
	return seconds, nil
}

// BillingSubjectBuildSeconds is the build time the subject's builds that
// started in [start, end) ran.
func (m *MemStore) BillingSubjectBuildSeconds(ctx context.Context, subjectID string, start, end time.Time) (int64, error) {
	type run struct {
		appID   string
		seconds int64
	}
	m.mu.Lock()
	now := m.clock()
	var runs []run
	for _, b := range m.builds {
		if b.StartedAt.IsZero() || b.StartedAt.Before(start) || !b.StartedAt.Before(end) {
			continue
		}
		finished := b.FinishedAt
		if finished.IsZero() {
			finished = now
			if end.Before(finished) {
				finished = end
			}
		}
		runs = append(runs, run{appID: m.deployments[b.DeploymentID].AppID, seconds: max(int64(finished.Sub(b.StartedAt)/time.Second), 0)})
	}
	m.mu.Unlock()
	subjects := map[string]string{}
	var total int64
	for _, r := range runs {
		subject, seen := subjects[r.appID]
		if !seen {
			app, err := m.AppByID(ctx, r.appID)
			if err != nil {
				return 0, err
			}
			owner, err := AppResourceAccount(ctx, m, app)
			if err != nil {
				return 0, err
			}
			subject = owner.ResourceOwnerID()
			subjects[r.appID] = subject
		}
		if subject == subjectID {
			total += r.seconds
		}
	}
	return total, nil
}

// SetBuildTimesForTest stamps a build's start and finish, which
// UpdateBuildStatus takes from the wall clock, so a test can give a build a
// known run time. A zero finished leaves the build running. Not on the Store
// interface.
func (m *MemStore) SetBuildTimesForTest(id string, started, finished time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.builds[id]
	if !ok {
		return ErrNotFound
	}
	b.StartedAt, b.FinishedAt = started, finished
	m.builds[id] = b
	return nil
}
