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
	d := AccountingDiagnostic{AccountingCoverage: row, Blocking: issues != 0, Reasons: make([]string, 0)}
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
				d.CorrectionRequiredAt = d.RequiredUntil.Add(recentUsageCorrectionWindows * policy.Window)
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

type usageIssues uint8

const (
	usageIdentityUnknown usageIssues = 1 << iota
	usageCoverageMissing
	usageWindowMismatch
	usageShutdownUnconfirmed
	usageCoverageIncomplete
	usageObservationStale
	usageFinalCorrectionPending
)

// blockingIssues is shared with admission. Diagnosing evidence must not relax
// the fail-closed guardrail or introduce a different notion of freshness.
func (p UsageProgress) blockingIssues(policy UsagePolicy, now time.Time) usageIssues {
	if !policy.Enabled {
		return 0
	}
	if p.Unresolved {
		return usageIdentityUnknown
	}
	if p.Window == 0 || p.ObservedAt.IsZero() {
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
			issues |= usageCoverageIncomplete
		}
		if p.CorrectionObservedAt.Before(end.Add(recentUsageCorrectionWindows * policy.Window)) {
			issues |= usageFinalCorrectionPending
		}
		return issues
	}
	if now.Sub(p.ObservedAt) > policy.StaleAfter {
		issues |= usageObservationStale
	}
	if p.CollectedUntil.Before(now.UTC().Truncate(policy.Window)) {
		issues |= usageCoverageIncomplete
	}
	return issues
}
