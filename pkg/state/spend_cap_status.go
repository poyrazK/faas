package state

import (
	"context"
	"time"
)

// SpendCapReader is what SpendCapStatusFor reads: the subject's cap and its
// current-month overage. A store that also implements SpendCapHoldStore
// contributes meterd's hold for the secondary meters only it prices.
type SpendCapReader interface {
	GetAccountOverageCapCents(ctx context.Context, accountID string) (int64, bool, error)
	CurrentMonthOverageCents(ctx context.Context, accountID string) (int64, error)
}

// SpendCapStatus is a billing subject's spend cap at one moment.
type SpendCapStatus struct {
	// Capped reports a cap is set; Reached that the overage met it.
	Capped, Reached         bool
	ObservedCents, CapCents int64
}

// SpendCapStatusFor reports whether subject's monthly spend cap is reached:
// a cap is set and the current-month overage, or meterd's hold for the
// meters only it prices (ADR-20261026120843), meets it. schedd's wake gate,
// its cron sweep and drain, and apid's manual cron runs all decide by it, so
// they agree. On a read error it returns the error with what it read; callers
// fail open.
func SpendCapStatusFor(ctx context.Context, store SpendCapReader, subject string, now time.Time) (SpendCapStatus, error) {
	capCents, capped, err := store.GetAccountOverageCapCents(ctx, subject)
	if err != nil || !capped {
		return SpendCapStatus{}, err
	}
	status := SpendCapStatus{Capped: true, CapCents: capCents}
	observed, err := store.CurrentMonthOverageCents(ctx, subject)
	if err != nil {
		return status, err
	}
	status.ObservedCents = observed
	if observed >= capCents {
		status.Reached = true
		return status, nil
	}
	if holds, ok := store.(SpendCapHoldStore); ok {
		if hold, found, holdErr := holds.GetSpendCapHold(ctx, subject); holdErr == nil && found && hold.Holds(capCents, now) {
			status.Reached, status.ObservedCents = true, max(observed, hold.ObservedCents)
		}
	}
	return status, nil
}

// SpendCapSubject is the billing subject whose spend cap governs app: its
// organization when the organization owns and is billed for its resources,
// else its account (ADR-20261030093512). A failed ownership read falls back
// to the account, as the cap check fails open.
func SpendCapSubject(ctx context.Context, store AppOwnerReader, app App) string {
	if app.OrgID == "" {
		return app.AccountID
	}
	owner, err := AppResourceAccount(ctx, store, app)
	if err == nil && owner.ResourceOwnerID() == app.OrgID {
		return app.OrgID
	}
	return app.AccountID
}
