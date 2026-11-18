package managedpostgres

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AccountingCoverage is one catalog resource and its effective ledger coverage.
// Shared restores use their accounting root's creation and shutdown times.
type AccountingCoverage struct {
	DatabaseID, Name, AccountingDatabaseID string
	State                                  State
	AccountingRequired, IdentityKnown      bool
	CreatedAt, LeaseUntil                  time.Time
	Progress                               UsageProgress
}

// AccountingDiagnosticsStore reads a bounded, account-scoped catalog page.
// Implementations must resolve catalog and coverage in one database snapshot.
type AccountingDiagnosticsStore interface {
	ListAccountingCoverage(context.Context, string, string, int) ([]AccountingCoverage, error)
}

type AccountingDiagnostic struct {
	AccountingCoverage
	Blocking                                          bool
	Reasons                                           []string
	RequiredFrom, RequiredUntil, CorrectionRequiredAt time.Time
}

type AccountingDiagnostics struct {
	AccountID, NextCursor string
	EvaluatedAt           time.Time
	PolicyEnabled         bool
	Window                time.Duration
	Items                 []AccountingDiagnostic
}

// AccountingDiagnostics performs no provider calls and never modifies evidence.
func (s *Service) AccountingDiagnostics(ctx context.Context, accountID, afterID string, limit int, now time.Time) (AccountingDiagnostics, error) {
	if s == nil || s.registry == nil || s.store == nil {
		return AccountingDiagnostics{}, ErrUnavailable
	}
	if accountID == "" || now.IsZero() || limit < 1 || limit > 100 || (afterID != "" && uuid.Validate(afterID) != nil) {
		return AccountingDiagnostics{}, ErrInvalid
	}
	store, ok := s.store.(AccountingDiagnosticsStore)
	if afterID != "" {
		afterID = uuid.MustParse(afterID).String()
	}
	if !ok {
		return AccountingDiagnostics{}, ErrUnavailable
	}
	rows, err := store.ListAccountingCoverage(ctx, accountID, afterID, limit+1)
	if err != nil {
		return AccountingDiagnostics{}, err
	}
	policy := s.registry.UsagePolicy()
	page := AccountingDiagnostics{AccountID: accountID, EvaluatedAt: now.UTC(), PolicyEnabled: policy.Enabled,
		Window: policy.Window, Items: make([]AccountingDiagnostic, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = rows[len(rows)-1].DatabaseID
	}
	for _, row := range rows {
		page.Items = append(page.Items, accountingDiagnostic(row, policy, page.EvaluatedAt))
	}
	return page, nil
}

func accountingDiagnostic(row AccountingCoverage, policy UsagePolicy, now time.Time) AccountingDiagnostic {
	issues := row.Progress.blockingIssues(policy, now)
	// A pending final window blocks only an account without headroom, which
	// is an account-wide question (UsageSnapshot.Stale), so one row's
	// diagnostic reports it without calling it blocking.
	d := AccountingDiagnostic{AccountingCoverage: row, Blocking: issues.hard(), Reasons: make([]string, 0)}
	if validUsageWindow(policy.Window) {
		if !row.CreatedAt.IsZero() {
			d.RequiredFrom = row.CreatedAt.UTC().Truncate(policy.Window)
		}
		d.RequiredUntil = now.UTC().Truncate(policy.Window)
		if row.Progress.Terminal {
			d.RequiredUntil = usageEnd(row.Progress.EndedAt, policy.Window)
			if row.Progress.Unresolved {
				d.RequiredUntil = time.Time{}
			}
			if !d.RequiredUntil.IsZero() {
				d.CorrectionRequiredAt = finalCorrectionDeadline(d.RequiredUntil, policy.Window)
			}
		}
	}
	for _, reason := range []struct {
		issue usageIssues
		code  string
	}{
		{usageIdentityUnknown, "identity_unknown"}, {usageCoverageMissing, "coverage_missing"},
		{usageWindowMismatch, "window_mismatch"}, {usageShutdownUnconfirmed, "shutdown_unconfirmed"},
		{usageCoverageIncomplete, "coverage_incomplete"}, {usageObservationStale, "observation_stale"},
		{usageFinalCorrectionPending, "final_correction_pending"},
		{usageFinalWindowPending, "final_window_pending"},
		{usageLatestWindowPending, "latest_window_pending"},
	} {
		if issues&reason.issue != 0 {
			code := reason.code
			if reason.issue == usageIdentityUnknown && row.State == StateDeleted {
				code = "legacy_identity_unknown"
			}
			d.Reasons = append(d.Reasons, code)
		}
	}
	return d
}

type usageIssues uint16

const (
	usageIdentityUnknown usageIssues = 1 << iota
	usageCoverageMissing
	usageWindowMismatch
	usageShutdownUnconfirmed
	usageCoverageIncomplete
	usageObservationStale
	usageFinalCorrectionPending
	// usageFinalWindowPending: a deleted resource owes only the window it was
	// deleted in, which has not closed or closed within StaleAfter. Admission
	// waits for it only when the account lacks headroom under every monthly
	// ceiling (ADR-20261115190000).
	usageFinalWindowPending
	// usageLatestWindowPending: a live resource's only uncollected window is
	// the most recent closed one, closed within StaleAfter. The provider
	// publishes an hour's usage some minutes after it ends, so without it
	// every hour began with the account paused. Like a final window, it
	// blocks only an account without headroom (ADR-20261115190000
	// amendment 1).
	usageLatestWindowPending

	// usageSoftIssues are admitted while the account has headroom.
	usageSoftIssues = usageFinalWindowPending | usageLatestWindowPending
)

// hard reports an issue that blocks admission whatever the account's usage.
func (i usageIssues) hard() bool {
	return i&^usageSoftIssues != 0
}

// finalWindowOnSchedule reports a deleted resource whose only missing
// coverage is its final window (the one it was deleted in, ending at end),
// while that window is open or closed no more than StaleAfter ago. Earlier
// windows, when owed, must already be covered: owedFrom is the earliest
// window still uncollected.
func finalWindowOnSchedule(owedFrom, end time.Time, policy UsagePolicy, now time.Time) bool {
	return !end.IsZero() && !owedFrom.Before(end.Add(-policy.Window)) && now.Sub(end) <= policy.StaleAfter
}

// creationInFlight reports an unresolved obligation whose provider call is
// still running: the resource is provisioning under a held lease and was
// reserved inside the current window, so it cannot owe a completed window
// yet (the H9-P113 rule). A lost reply lets the lease lapse, and the window
// closing ends the exemption too; either way the obligation blocks again
// until its identity is recovered (ADR-20261104203253, amending ADR-581).
func (p UsageProgress) creationInFlight(policy UsagePolicy, now time.Time) bool {
	return !p.Terminal && p.CreationLeaseUntil.After(now) && !p.CreatedAt.IsZero() &&
		!p.CreatedAt.Before(now.UTC().Truncate(policy.Window))
}

// finalCorrectionDeadline is when every window of a deleted resource's final
// correction tail, which ends at end, has been replayed (ADR-569).
func finalCorrectionDeadline(end time.Time, window time.Duration) time.Time {
	return end.Add(recentUsageCorrectionWindows * window)
}

// blockingIssues is shared with admission. Diagnosing evidence must not relax
// the fail-closed guardrail or introduce a different notion of freshness.
func (p UsageProgress) blockingIssues(policy UsagePolicy, now time.Time) usageIssues {
	if !policy.Enabled {
		return 0
	}
	if p.Unresolved {
		if p.creationInFlight(policy, now) {
			return 0
		}
		return usageIdentityUnknown
	}
	if p.Window == 0 || p.ObservedAt.IsZero() {
		// Collection records completed windows only. Until the window a live
		// resource was created in closes, nothing is owed, and treating it as
		// missing made every create block the whole account for up to an
		// hour (production-us hunt #9, H9-P113).
		if !p.Terminal && !p.CreatedAt.IsZero() && !p.CreatedAt.Before(now.UTC().Truncate(policy.Window)) {
			return 0
		}
		// Created in the window that just closed: that window is its first
		// and only one owed (amendment 1).
		if !p.Terminal && !p.CreatedAt.IsZero() && latestWindowOnSchedule(p.CreatedAt.UTC().Truncate(policy.Window), policy, now) {
			return usageLatestWindowPending
		}
		// A resource created and deleted inside one window owes only that
		// window (ADR-20261115190000).
		if p.Terminal && !p.CreatedAt.IsZero() &&
			finalWindowOnSchedule(p.CreatedAt.UTC().Truncate(policy.Window), usageEnd(p.EndedAt, policy.Window), policy, now) {
			return usageFinalWindowPending
		}
		return usageCoverageMissing
	}
	if p.Window != policy.Window {
		return usageWindowMismatch
	}
	var issues usageIssues
	if p.Terminal {
		end := usageEnd(p.EndedAt, policy.Window)
		if end.IsZero() {
			return usageShutdownUnconfirmed
		}
		if p.CollectedUntil.Before(end) {
			if finalWindowOnSchedule(p.CollectedUntil, end, policy, now) {
				issues |= usageFinalWindowPending
			} else {
				issues |= usageCoverageIncomplete
			}
		}
		// The final correction tail revises windows that already have a
		// reading, so admission does not wait for it: it blocks only once the
		// tail is overdue by the same bound an active resource's observation
		// gets. A provider that keeps failing still fails closed
		// (ADR-20261104143000).
		settledBy := finalCorrectionDeadline(end, policy.Window)
		if p.CorrectionObservedAt.Before(settledBy) && now.Sub(settledBy) > policy.StaleAfter {
			issues |= usageFinalCorrectionPending
		}
		return issues
	}
	if now.Sub(p.ObservedAt) > policy.StaleAfter {
		issues |= usageObservationStale
	}
	if p.CollectedUntil.Before(now.UTC().Truncate(policy.Window)) {
		if latestWindowOnSchedule(p.CollectedUntil, policy, now) {
			issues |= usageLatestWindowPending
		} else {
			issues |= usageCoverageIncomplete
		}
	}
	return issues
}

// latestWindowOnSchedule reports a live resource whose coverage, collected up
// to owedFrom, misses only the most recent closed window, and that window
// closed no more than StaleAfter ago (ADR-20261115190000 amendment 1).
func latestWindowOnSchedule(owedFrom time.Time, policy UsagePolicy, now time.Time) bool {
	latestEnd := now.UTC().Truncate(policy.Window)
	return !owedFrom.Before(latestEnd.Add(-policy.Window)) && now.Sub(latestEnd) <= policy.StaleAfter
}
