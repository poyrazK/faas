package state

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// BillingUsageIntentStore freezes the quantity before any external effect.
// Retries and replicas must use the first quantity for the provider event key,
// even if a cap, allowance or plan changes before the receipt is recorded.
type BillingUsageIntentStore interface {
	ReserveBillingUsageIntent(context.Context, string, string, BillingMeter, time.Time, int64) (int64, error)
}

// SpendCapNoticeStore is the durable, fenced outbox created atomically with a
// spend-cap hold. Clearing the hold does not discard an undelivered notice.
type SpendCapNoticeStore interface {
	ClaimSpendCapNotice(context.Context, time.Time) (SpendCapNoticeDelivery, error)
	SettleSpendCapNotice(context.Context, string, string, time.Time, bool) error
}

type SpendCapNoticeDelivery struct {
	SpendCapHold
	ID, LeaseToken string
}

func spendCapNoticeID(h SpendCapHold) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(h.AccountID+":"+h.Month.UTC().Format("2006-01")+":"+strconv.FormatInt(h.CapCents, 10))).String()
}

// PlanActivationStore retains every mutation, including returns to unchanged
// terms between meterd passes. The head invalidates the process pricing cache.
type PlanActivationStore interface {
	BillingPlanActivationHead(context.Context) (int64, error)
	ListBillingPlanActivations(context.Context, string, time.Time, time.Time) ([]BillingPlanActivation, error)
}

type BillingPlanActivation struct {
	Sequence int64
	From, To api.Plan
	At       time.Time
}

// FinancialBudgetJobLister includes image-run jobs, whose execution participates
// in account funding, separately from the dashboard's filtered job index.
type FinancialBudgetJobLister interface {
	FinancialBudgetJobs(context.Context, string, int) ([]Job, error)
}
