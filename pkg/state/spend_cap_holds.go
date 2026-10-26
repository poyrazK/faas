package state

import (
	"context"
	"time"
)

// SpendCapHold records that an account's billable overage for one UTC month,
// compute plus every live secondary meter, reached its overage cap
// (ADR-20261026120843). meterd writes it because only meterd knows the
// provider's meter policies; schedd honours it while Month is the current
// month and CapCents is the account's current cap.
type SpendCapHold struct {
	AccountID     string
	Month         time.Time
	CapCents      int64
	ObservedCents int64
	ReachedAt     time.Time
}

// Holds reports whether the hold still applies to an account whose cap is
// capCents at now.
func (h SpendCapHold) Holds(capCents int64, now time.Time) bool {
	now = now.UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return h.CapCents == capCents && h.Month.UTC().Equal(month)
}

// SpendCapHoldStore keeps one hold per account.
type SpendCapHoldStore interface {
	PutSpendCapHold(ctx context.Context, hold SpendCapHold) error
	ClearSpendCapHold(ctx context.Context, accountID string) error
	GetSpendCapHold(ctx context.Context, accountID string) (SpendCapHold, bool, error)
}

func validSpendCapHold(hold SpendCapHold) bool {
	month := hold.Month.UTC()
	return hold.AccountID != "" && hold.CapCents >= 0 && hold.ObservedCents >= hold.CapCents &&
		month.Equal(time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC))
}
