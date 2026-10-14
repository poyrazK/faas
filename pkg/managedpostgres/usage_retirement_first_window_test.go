package managedpostgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewlyDeletedDatabaseDoesNotDemandUnfinishedUsageWindow(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC)
	store, registry, _, database := retirementFixture(t, "memory", now.Add(-10*time.Minute))
	retirementDelete(t, store, database, now.Add(-7*time.Minute), true)
	policy := registry.UsagePolicy()
	if err := policy.Admit(context.Background(), store, database.AccountID, now); err != nil {
		t.Fatalf("same-window create/delete blocked before usage could exist: %v", err)
	}
	closed := now.Truncate(policy.Window).Add(policy.Window)
	for _, at := range []time.Time{closed, closed.Add(time.Hour), closed.AddDate(0, 1, 0)} {
		if err := policy.Admit(context.Background(), store, database.AccountID, at); !errors.Is(err, ErrUsageStale) {
			t.Fatalf("missing retired usage was forgiven after its window closed at %s: %v", at, err)
		}
	}
}

func TestUnfinishedRetiredUsageGraceRejectsUnconfirmedAndPastObligations(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 30, 0, 0, time.UTC)
	policy := UsagePolicy{Enabled: true, Window: time.Hour}
	base := UsageProgress{Terminal: true, CreatedAt: now.Add(-10 * time.Minute), EndedAt: now.Add(-7 * time.Minute)}
	for _, tc := range []struct {
		name   string
		change func(*UsageProgress)
	}{
		{"unknown identity", func(p *UsageProgress) { p.Unresolved = true }},
		{"unconfirmed shutdown", func(p *UsageProgress) { p.EndedAt = time.Time{} }},
		{"shutdown before creation", func(p *UsageProgress) { p.EndedAt = p.CreatedAt.Add(-time.Second) }},
		{"future shutdown", func(p *UsageProgress) { p.EndedAt = now.Add(time.Second) }},
		{"future creation", func(p *UsageProgress) { p.CreatedAt = now.Add(time.Second) }},
		{"unknown creation", func(p *UsageProgress) { p.CreatedAt = time.Time{} }},
		{"earlier uncollected window", func(p *UsageProgress) { p.CreatedAt = now.Add(-time.Hour) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			progress := base
			tc.change(&progress)
			if progress.blockingIssues(policy, now) == 0 {
				t.Fatal("invalid or completed obligation received unfinished-window grace")
			}
		})
	}
	if base.blockingIssues(policy, now.In(time.FixedZone("Istanbul", 3*3600))) != 0 {
		t.Fatal("unfinished-window boundary did not use UTC")
	}
}
