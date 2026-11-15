package state

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	risksql "github.com/onebox-faas/faas/pkg/state/accountrisk/sqlc"
)

var (
	_ AccountRiskStore = (*PgStore)(nil)
	_ AccountRiskStore = (*MemStore)(nil)
)

// AccountAbuseStrikes implements AccountRiskStore.
func (s *PgStore) AccountAbuseStrikes(ctx context.Context, accountID string, since time.Time) ([]AccountAbuseStrike, error) {
	id := mustPgUUID(accountID)
	if !id.Valid {
		return nil, ErrInvalidArgument
	}
	rows, err := risksql.New().ListStrikes(ctx, s.pool, risksql.ListStrikesParams{AccountID: id, Since: pgTime(since)})
	if err != nil {
		return nil, fmt.Errorf("state: abuse strikes %s: %w", accountID, mapErr(err))
	}
	out := make([]AccountAbuseStrike, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountAbuseStrike{Reason: r.Reason, AppID: pgUUIDString(r.AppID), StruckAt: r.StruckAt.Time.UTC()})
	}
	return out, nil
}

// HeldLinksToAccount implements AccountRiskStore.
func (s *PgStore) HeldLinksToAccount(ctx context.Context, heldAccountID string) ([]AccountHeldLink, error) {
	id := mustPgUUID(heldAccountID)
	if !id.Valid {
		return nil, ErrInvalidArgument
	}
	rows, err := risksql.New().HeldLinksTo(ctx, s.pool, id)
	if err != nil {
		return nil, fmt.Errorf("state: held links to %s: %w", heldAccountID, mapErr(err))
	}
	out := make([]AccountHeldLink, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountHeldLink{
			AccountID: pgUUIDString(r.AccountID), HeldAccountID: heldAccountID, HeldOrgID: pgUUIDString(r.HeldOrgID),
			HeldReason: r.HeldReason, Signal: r.Signal, DetectedAt: r.DetectedAt.Time.UTC(),
		})
	}
	return out, nil
}

// RecentHeldLinks implements AccountRiskStore.
func (s *PgStore) RecentHeldLinks(ctx context.Context, since time.Time, limit int) ([]RecentHeldLink, error) {
	if limit <= 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := risksql.New().RecentHeldLinks(ctx, s.pool, risksql.RecentHeldLinksParams{Since: pgTime(since), MaxRows: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("state: recent held links: %w", mapErr(err))
	}
	out := make([]RecentHeldLink, 0, len(rows))
	for _, r := range rows {
		out = append(out, RecentHeldLink{
			AccountHeldLink: AccountHeldLink{
				AccountID: pgUUIDString(r.AccountID), HeldAccountID: pgUUIDString(r.HeldAccountID), HeldOrgID: pgUUIDString(r.HeldOrgID),
				HeldReason: r.HeldReason, Signal: r.Signal, DetectedAt: r.DetectedAt.Time.UTC(),
			},
			Country: r.Country, EmailDomain: r.EmailDomain, DisposableEmail: r.DisposableEmail,
		})
	}
	return out, nil
}

// AccountAbuseEvents implements AccountRiskStore.
func (s *PgStore) AccountAbuseEvents(ctx context.Context, accountID string, kinds []string, since time.Time, limit int) ([]AccountAbuseEvent, error) {
	id := mustPgUUID(accountID)
	if !id.Valid || limit <= 0 || len(kinds) == 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := risksql.New().AbuseEvents(ctx, s.pool, risksql.AbuseEventsParams{
		Since: pgTime(since), Kinds: kinds, AccountID: id, MaxRows: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: abuse events %s: %w", accountID, mapErr(err))
	}
	out := make([]AccountAbuseEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountAbuseEvent{At: r.At.Time.UTC(), Actor: r.Actor, Kind: r.Kind, Subject: pgUUIDString(r.Subject), Data: r.Data})
	}
	return out, nil
}

// AccountAbuseStrikes implements AccountRiskStore.
func (m *MemStore) AccountAbuseStrikes(_ context.Context, accountID string, since time.Time) ([]AccountAbuseStrike, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AccountAbuseStrike
	for _, s := range m.abuseStrikes[accountID] {
		if s.StruckAt.After(since) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StruckAt.After(out[j].StruckAt) })
	return out, nil
}

// HeldLinksToAccount implements AccountRiskStore.
func (m *MemStore) HeldLinksToAccount(_ context.Context, heldAccountID string) ([]AccountHeldLink, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AccountHeldLink
	for _, links := range m.heldLinks {
		for _, l := range links {
			if l.HeldAccountID == heldAccountID {
				out = append(out, l)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DetectedAt.Equal(out[j].DetectedAt) {
			return out[i].DetectedAt.After(out[j].DetectedAt)
		}
		return out[i].AccountID+out[i].Signal < out[j].AccountID+out[j].Signal
	})
	return out, nil
}

// RecentHeldLinks implements AccountRiskStore.
func (m *MemStore) RecentHeldLinks(_ context.Context, since time.Time, limit int) ([]RecentHeldLink, error) {
	if limit <= 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RecentHeldLink
	for _, links := range m.heldLinks {
		for _, l := range links {
			if l.DetectedAt.Before(since) {
				continue
			}
			f := m.signupFacts[l.AccountID]
			out = append(out, RecentHeldLink{AccountHeldLink: l, Country: f.Country, EmailDomain: f.EmailDomain, DisposableEmail: f.DisposableEmail})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].DetectedAt.Equal(out[j].DetectedAt) {
			return out[i].DetectedAt.After(out[j].DetectedAt)
		}
		return out[i].AccountID+out[i].HeldAccountID+out[i].Signal < out[j].AccountID+out[j].HeldAccountID+out[j].Signal
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AccountAbuseEvents implements AccountRiskStore.
func (m *MemStore) AccountAbuseEvents(_ context.Context, accountID string, kinds []string, since time.Time, limit int) ([]AccountAbuseEvent, error) {
	if accountID == "" || limit <= 0 || len(kinds) == 0 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Events key subjects through parseSubjectID (MemStore ids need not be
	// UUIDs), so compare in that form.
	subjects := map[string]bool{}
	add := func(id string) {
		if u := parseSubjectID(id); u != nil {
			subjects[u.String()] = true
		}
	}
	add(accountID)
	for id, app := range m.apps {
		if app.AccountID == accountID {
			add(id)
		}
	}
	for id, d := range m.deployments {
		if app, ok := m.apps[d.AppID]; ok && app.AccountID == accountID {
			add(id)
		}
	}
	for id, ins := range m.instances {
		if app, ok := m.apps[ins.AppID]; ok && app.AccountID == accountID {
			add(id)
		}
	}
	var out []AccountAbuseEvent
	for i := len(m.events) - 1; i >= 0 && len(out) < limit; i-- {
		e := m.events[i]
		if e.Subject == nil || e.At.Before(since) || !slices.Contains(kinds, e.Kind) || !subjects[e.Subject.String()] {
			continue
		}
		out = append(out, AccountAbuseEvent{At: e.At.UTC(), Actor: e.Actor, Kind: e.Kind, Subject: e.Subject.String(), Data: e.Data})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}
