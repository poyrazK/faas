package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EgressOverageChoice is one time a billing subject turned egress overage on
// or off (ADR-20261103131742 amendment 2).
type EgressOverageChoice struct {
	ChangedAt time.Time
	Enabled   bool
}

// EgressOverageStore records whether a paid billing subject, an account or a
// company organization, lets its apps keep serving past the plan's monthly
// egress, billed as overage under its spend cap. Off is the default. Every
// change is kept, so an hour used while it was on is billed even when the
// subject turns it off before meterd delivers that hour.
type EgressOverageStore interface {
	// SetEgressOverage records subject's choice at at.
	SetEgressOverage(ctx context.Context, subjectID string, enabled bool, at time.Time) error
	// EgressOverageEnabled reports subject's current choice.
	EgressOverageEnabled(ctx context.Context, subjectID string) (bool, error)
	// EgressOverageEnabledSubjects lists the subjects whose current choice is on.
	EgressOverageEnabledSubjects(ctx context.Context) (map[string]bool, error)
	// EgressOverageChoices returns subject's choices made in [start, end)
	// and the last one made at or before start, oldest first.
	EgressOverageChoices(ctx context.Context, subjectID string, start, end time.Time) ([]EgressOverageChoice, error)
}

var (
	_ EgressOverageStore = (*PgStore)(nil)
	_ EgressOverageStore = (*MemStore)(nil)
)

// EgressOverageOnDuring reports whether egress overage was on at any moment
// of [start, end), given the subject's choices oldest first as
// EgressOverageChoices returns them.
func EgressOverageOnDuring(choices []EgressOverageChoice, start, end time.Time) bool {
	on := false
	for _, c := range choices {
		if !c.ChangedAt.After(start) {
			on = c.Enabled // the choice in force when the window opens
			continue
		}
		if on {
			return true
		}
		if !c.ChangedAt.Before(end) {
			break
		}
		on = c.Enabled
	}
	return on
}

// SetEgressOverage records subject's egress overage choice at at.
func (s *PgStore) SetEgressOverage(ctx context.Context, subjectID string, enabled bool, at time.Time) error {
	id, err := parsePgUUID(subjectID)
	if err != nil {
		return err
	}
	if err := sqlc.New().RecordEgressOverageChoice(ctx, s.pool, sqlc.RecordEgressOverageChoiceParams{
		SubjectID: id, ChangedAt: pgTime(at), Enabled: enabled,
	}); err != nil {
		return fmt.Errorf("state: record egress overage choice: %w", err)
	}
	return nil
}

// EgressOverageEnabled reports subject's current egress overage choice.
func (s *PgStore) EgressOverageEnabled(ctx context.Context, subjectID string) (bool, error) {
	id, err := parsePgUUID(subjectID)
	if err != nil {
		return false, err
	}
	enabled, err := sqlc.New().EgressOverageChoice(ctx, s.pool, id)
	if err != nil {
		return false, fmt.Errorf("state: read egress overage choice: %w", err)
	}
	return enabled, nil
}

// EgressOverageEnabledSubjects lists the subjects whose current choice is on.
func (s *PgStore) EgressOverageEnabledSubjects(ctx context.Context) (map[string]bool, error) {
	ids, err := sqlc.New().EgressOverageEnabledSubjects(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list egress overage subjects: %w", err)
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// EgressOverageChoices returns subject's choices in [start, end) and the last
// one at or before start, oldest first.
func (s *PgStore) EgressOverageChoices(ctx context.Context, subjectID string, start, end time.Time) ([]EgressOverageChoice, error) {
	id, err := parsePgUUID(subjectID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().EgressOverageChoices(ctx, s.pool, sqlc.EgressOverageChoicesParams{
		SubjectID: id, StartAt: pgTime(start), EndAt: pgTime(end),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list egress overage choices: %w", err)
	}
	out := make([]EgressOverageChoice, 0, len(rows))
	for _, row := range rows {
		out = append(out, EgressOverageChoice{ChangedAt: row.ChangedAt.Time.UTC(), Enabled: row.Enabled})
	}
	return out, nil
}

// SetEgressOverage records subject's egress overage choice at at.
func (m *MemStore) SetEgressOverage(_ context.Context, subjectID string, enabled bool, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.egressOverage == nil {
		m.egressOverage = map[string][]EgressOverageChoice{}
	}
	at = at.UTC()
	choices := m.egressOverage[subjectID]
	for i := range choices {
		if choices[i].ChangedAt.Equal(at) {
			choices[i].Enabled = enabled
			return nil
		}
	}
	choices = append(choices, EgressOverageChoice{ChangedAt: at, Enabled: enabled})
	sort.Slice(choices, func(i, j int) bool { return choices[i].ChangedAt.Before(choices[j].ChangedAt) })
	m.egressOverage[subjectID] = choices
	return nil
}

// EgressOverageEnabled reports subject's current egress overage choice.
func (m *MemStore) EgressOverageEnabled(_ context.Context, subjectID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	choices := m.egressOverage[subjectID]
	return len(choices) > 0 && choices[len(choices)-1].Enabled, nil
}

// EgressOverageEnabledSubjects lists the subjects whose current choice is on.
func (m *MemStore) EgressOverageEnabledSubjects(_ context.Context) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for id, choices := range m.egressOverage {
		if len(choices) > 0 && choices[len(choices)-1].Enabled {
			out[id] = true
		}
	}
	return out, nil
}

// EgressOverageChoices returns subject's choices in [start, end) and the last
// one at or before start, oldest first.
func (m *MemStore) EgressOverageChoices(_ context.Context, subjectID string, start, end time.Time) ([]EgressOverageChoice, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []EgressOverageChoice
	for _, c := range m.egressOverage[subjectID] {
		if !c.ChangedAt.After(start) {
			out = append(out[:0], c)
			continue
		}
		if c.ChangedAt.Before(end) {
			out = append(out, c)
		}
	}
	return out, nil
}
