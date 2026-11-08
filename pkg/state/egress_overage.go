package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EgressOverageElection is a billing subject's election to pay for
// application egress above its plan's monthly allowance (ADR-20261104204500).
// Without one, every plan stops at the allowance (ADR-20261104090000).
//
// The election covers [EnabledAt, DisabledAt). Turning it off stamps
// DisabledAt, so egress already sent while it was on is still billed when
// meterd delivers that hour later. Turning it back on clears DisabledAt and
// keeps EnabledAt: an hour from the gap that is still undelivered is billed
// too, but the ceiling held during the gap, so at most the overshoot before
// the stop is in it.
type EgressOverageElection struct {
	EnabledAt  *time.Time
	DisabledAt *time.Time
}

// On reports whether the election is in force now: egress above the
// allowance is overage and the egress ceiling does not stop the subject.
func (e EgressOverageElection) On() bool {
	return e.EnabledAt != nil && e.DisabledAt == nil
}

// Covers reports whether the egress hour starting at hour is billable as
// overage: it starts inside [EnabledAt, DisabledAt). The hour the election
// starts in is not covered, since part of it came before the election.
func (e EgressOverageElection) Covers(hour time.Time) bool {
	if e.EnabledAt == nil || hour.Before(*e.EnabledAt) {
		return false
	}
	return e.DisabledAt == nil || hour.Before(*e.DisabledAt)
}

// EgressOverageReader reads one subject's egress overage election.
type EgressOverageReader interface {
	// EgressOverageElection returns subjectID's election; the zero value
	// when it never elected or no such subject exists.
	EgressOverageElection(ctx context.Context, subjectID string) (EgressOverageElection, error)
}

// EgressOverageStore reads and writes egress overage elections. The subject
// is an account or a non-personal organization, as for the overage cap.
type EgressOverageStore interface {
	EgressOverageReader
	// EgressOverageElections returns the elections of the subjects among ids
	// that ever elected; the others are absent.
	EgressOverageElections(ctx context.Context, ids []string) (map[string]EgressOverageElection, error)
	// SetEgressOverage turns subjectID's election on or off at now and
	// returns the result. Turning on an election that is on, or off one that
	// is off, changes nothing. ErrNotFound when no such subject exists.
	SetEgressOverage(ctx context.Context, subjectID string, on bool, now time.Time) (EgressOverageElection, error)
}

var (
	_ EgressOverageStore = (*PgStore)(nil)
	_ EgressOverageStore = (*MemStore)(nil)
)

func electionFrom(enabled, disabled pgtype.Timestamptz) EgressOverageElection {
	var e EgressOverageElection
	if enabled.Valid {
		at := enabled.Time.UTC()
		e.EnabledAt = &at
	}
	if disabled.Valid {
		at := disabled.Time.UTC()
		e.DisabledAt = &at
	}
	return e
}

// EgressOverageElection implements EgressOverageStore.
func (s *PgStore) EgressOverageElection(ctx context.Context, subjectID string) (EgressOverageElection, error) {
	if uuid.Validate(subjectID) != nil {
		return EgressOverageElection{}, ErrInvalidArgument
	}
	var enabled, disabled pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		select egress_overage_enabled_at, egress_overage_disabled_at from accounts where id = $1::uuid
		union all
		select egress_overage_enabled_at, egress_overage_disabled_at from orgs
		 where id = $1::uuid and not personal_org
		   and not exists (select 1 from accounts a where a.id = $1::uuid)
		limit 1`, subjectID).Scan(&enabled, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return EgressOverageElection{}, nil
	}
	if err != nil {
		return EgressOverageElection{}, err
	}
	return electionFrom(enabled, disabled), nil
}

// EgressOverageElections implements EgressOverageStore.
func (s *PgStore) EgressOverageElections(ctx context.Context, ids []string) (map[string]EgressOverageElection, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if validUUID(id) {
			valid = append(valid, id)
		}
	}
	out := make(map[string]EgressOverageElection)
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		select a.id::text, a.egress_overage_enabled_at, a.egress_overage_disabled_at
		  from accounts a
		 where a.id = any($1::uuid[]) and a.egress_overage_enabled_at is not null
		union all
		select o.id::text, o.egress_overage_enabled_at, o.egress_overage_disabled_at
		  from orgs o
		 where o.id = any($1::uuid[]) and not o.personal_org and o.egress_overage_enabled_at is not null
		   and not exists (select 1 from accounts a where a.id = o.id)`, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var enabled, disabled pgtype.Timestamptz
		if err := rows.Scan(&id, &enabled, &disabled); err != nil {
			return nil, err
		}
		out[id] = electionFrom(enabled, disabled)
	}
	return out, rows.Err()
}

// SetEgressOverage implements EgressOverageStore.
func (s *PgStore) SetEgressOverage(ctx context.Context, subjectID string, on bool, now time.Time) (EgressOverageElection, error) {
	if uuid.Validate(subjectID) != nil {
		return EgressOverageElection{}, ErrInvalidArgument
	}
	queries := egressOverageQueries(on)
	for _, q := range queries {
		var enabled, disabled pgtype.Timestamptz
		err := s.pool.QueryRow(ctx, q, subjectID, now.UTC()).Scan(&enabled, &disabled)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return EgressOverageElection{}, err
		}
		return electionFrom(enabled, disabled), nil
	}
	return EgressOverageElection{}, ErrNotFound
}

// egressOverageQueries is the account update, then the organization update
// (non-personal organizations only).
func egressOverageQueries(on bool) [2]string {
	if on {
		return [2]string{
			`update accounts set egress_overage_enabled_at = coalesce(egress_overage_enabled_at, $2),
			        egress_overage_disabled_at = null
			  where id = $1::uuid
			 returning egress_overage_enabled_at, egress_overage_disabled_at`,
			`update orgs set egress_overage_enabled_at = coalesce(egress_overage_enabled_at, $2),
			        egress_overage_disabled_at = null
			  where id = $1::uuid and not personal_org
			 returning egress_overage_enabled_at, egress_overage_disabled_at`,
		}
	}
	return [2]string{
		`update accounts set egress_overage_disabled_at = case
		        when egress_overage_enabled_at is null then null
		        else coalesce(egress_overage_disabled_at, greatest($2, egress_overage_enabled_at)) end
		  where id = $1::uuid
		 returning egress_overage_enabled_at, egress_overage_disabled_at`,
		`update orgs set egress_overage_disabled_at = case
		        when egress_overage_enabled_at is null then null
		        else coalesce(egress_overage_disabled_at, greatest($2, egress_overage_enabled_at)) end
		  where id = $1::uuid and not personal_org
		 returning egress_overage_enabled_at, egress_overage_disabled_at`,
	}
}

// EgressOverageElection implements EgressOverageStore.
func (m *MemStore) EgressOverageElection(_ context.Context, subjectID string) (EgressOverageElection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.egressOverage[subjectID], nil
}

// EgressOverageElections implements EgressOverageStore.
func (m *MemStore) EgressOverageElections(_ context.Context, ids []string) (map[string]EgressOverageElection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]EgressOverageElection)
	for _, id := range ids {
		if e, ok := m.egressOverage[id]; ok && e.EnabledAt != nil {
			out[id] = e
		}
	}
	return out, nil
}

// SetEgressOverage implements EgressOverageStore.
func (m *MemStore) SetEgressOverage(_ context.Context, subjectID string, on bool, now time.Time) (EgressOverageElection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, isAccount := m.accounts[subjectID]
	org, isOrg := m.orgs[subjectID]
	if !isAccount && (!isOrg || org.Personal) {
		return EgressOverageElection{}, ErrNotFound
	}
	if m.egressOverage == nil {
		m.egressOverage = map[string]EgressOverageElection{}
	}
	e := m.egressOverage[subjectID]
	now = now.UTC()
	switch {
	case on:
		if e.EnabledAt == nil {
			e.EnabledAt = &now
		}
		e.DisabledAt = nil
	case e.EnabledAt != nil && e.DisabledAt == nil:
		end := now
		if end.Before(*e.EnabledAt) {
			end = *e.EnabledAt
		}
		e.DisabledAt = &end
	}
	m.egressOverage[subjectID] = e
	return e, nil
}
