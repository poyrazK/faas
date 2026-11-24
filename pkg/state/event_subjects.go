package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EventSubjectKindLister reports which of the given subjects have an event of
// one kind since a time, in one read. App health asks it of an app's recently
// stopped instances, which would otherwise take one ListEventsBySubjectKind
// per instance (prod hunt r33).
type EventSubjectKindLister interface {
	ListEventSubjectsWithKind(ctx context.Context, subjects []string, kind string, since time.Time) (map[string]bool, error)
}

// ListEventSubjectsWithKind implements EventSubjectKindLister.
func (s *PgStore) ListEventSubjectsWithKind(ctx context.Context, subjects []string, kind string, since time.Time) (map[string]bool, error) {
	out := map[string]bool{}
	if len(subjects) == 0 {
		return out, nil
	}
	ids := make([]pgtype.UUID, 0, len(subjects))
	byID := make(map[uuid.UUID]string, len(subjects))
	for _, subject := range subjects {
		id, err := uuid.Parse(subject)
		if err != nil {
			return nil, fmt.Errorf("list event subjects by kind: subject %q: %w", subject, err)
		}
		ids = append(ids, pgtype.UUID{Bytes: id, Valid: true})
		byID[id] = subject
	}
	rows, err := sqlc.New().ListEventSubjectsWithKind(ctx, s.pool, sqlc.ListEventSubjectsWithKindParams{
		Subjects: ids, Kind: kind, Since: pgtype.Timestamptz{Time: since, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list event subjects by kind: %w", err)
	}
	for _, row := range rows {
		if subject, ok := byID[uuid.UUID(row.Bytes)]; ok {
			out[subject] = true
		}
	}
	return out, nil
}

// ListEventSubjectsWithKind mirrors PgStore.ListEventSubjectsWithKind.
func (m *MemStore) ListEventSubjectsWithKind(_ context.Context, subjects []string, kind string, since time.Time) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := make(map[uuid.UUID]string, len(subjects))
	for _, subject := range subjects {
		if id := parseSubjectID(subject); id != nil {
			want[*id] = subject
		}
	}
	out := map[string]bool{}
	for _, e := range m.events {
		if e.Subject == nil || e.Kind != kind || e.At.Before(since) {
			continue
		}
		if subject, ok := want[*e.Subject]; ok {
			out[subject] = true
		}
	}
	return out, nil
}
