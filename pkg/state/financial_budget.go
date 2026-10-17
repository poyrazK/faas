package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

var ErrFinancialBudgetLimit = errors.New("financial budget policy limit exceeded")

type FinancialBudget struct {
	ID        string               `json:"id"`
	AccountID string               `json:"account_id"`
	Revision  int64                `json:"revision"`
	Spec      financial.BudgetSpec `json:"spec"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
	DeletedAt *time.Time           `json:"deleted_at,omitempty"`
}

type FinancialBudgetRevision struct {
	AccountID  string               `json:"account_id"`
	PolicyID   string               `json:"policy_id"`
	Revision   int64                `json:"revision"`
	Actor      string               `json:"actor"`
	Mutation   string               `json:"mutation"`
	Spec       financial.BudgetSpec `json:"spec"`
	RecordedAt time.Time            `json:"recorded_at"`
}

// FinancialBudgetStore persists customer intent and its revision audit in one
// transaction. It does not change account status, workload intent or instances.
type FinancialBudgetStore interface {
	CreateFinancialBudget(context.Context, string, string, string, financial.BudgetSpec) (FinancialBudget, error)
	UpdateFinancialBudget(context.Context, string, string, int64, string, financial.BudgetSpec) (FinancialBudget, error)
	DeleteFinancialBudget(context.Context, string, string, int64, string) (FinancialBudget, error)
	GetFinancialBudget(context.Context, string, string) (FinancialBudget, error)
	ListFinancialBudgets(context.Context, string) ([]FinancialBudget, error)
	ListFinancialBudgetRevisions(context.Context, string, string, int64, int) ([]FinancialBudgetRevision, error)
	ValidateFinancialBudgetScope(context.Context, string, financial.BudgetSpec) error
}

func ValidateFinancialBudgetSpec(p financial.BudgetSpec) error {
	if financial.ValidateBudget(p) != nil || len(p.Name) > api.FinancialBudgetNameBytes || len(p.NotifyMillicents) > api.FinancialBudgetThresholds || p.LimitMillicents > api.FinancialBudgetMoneyMax || p.DrainSeconds > api.FinancialBudgetDrainMax {
		return ErrInvalidArgument
	}
	if p.Scope.Kind != "account" && uuid.Validate(p.Scope.ID) != nil {
		return ErrInvalidArgument
	}
	return nil
}

func validFinancialBudgetMutation(account, id, actor string, expected int64) bool {
	return uuid.Validate(account) == nil && uuid.Validate(id) == nil && strings.TrimSpace(actor) != "" && len(actor) <= api.FinancialBudgetActorBytes && expected >= 0 && expected < api.FinancialBudgetRevisionMax
}

func cloneFinancialBudgetSpec(p financial.BudgetSpec) financial.BudgetSpec {
	p.Meters = append([]string{}, p.Meters...)
	p.NotifyMillicents = append([]int64{}, p.NotifyMillicents...)
	return p
}

func cloneFinancialBudget(p FinancialBudget) FinancialBudget {
	p.Spec = cloneFinancialBudgetSpec(p.Spec)
	if p.DeletedAt != nil {
		at := *p.DeletedAt
		p.DeletedAt = &at
	}
	return p
}

// ADR-1500 delivery outcomes for a budget threshold notification.
const (
	FinancialBudgetNotificationPending   = "pending"
	FinancialBudgetNotificationSent      = "sent"
	FinancialBudgetNotificationFailed    = "failed" // an attempt failed; the row stays pending
	FinancialBudgetNotificationAbandoned = "abandoned"
	FinancialBudgetNotificationCancelled = "cancelled"
)

// FinancialBudgetNotification is one crossed threshold of one budget in one
// UTC month (ADR-1500). The row exists before the email is sent.
type FinancialBudgetNotification struct {
	AccountID           string     `json:"account_id"`
	PolicyID            string     `json:"policy_id"`
	PeriodStart         time.Time  `json:"period_start"`
	ThresholdMillicents int64      `json:"threshold_millicents"`
	PolicyRevision      int64      `json:"policy_revision"`
	KnownMillicents     int64      `json:"known_millicents"`
	CoverageComplete    bool       `json:"coverage_complete"`
	Status              string     `json:"status"`
	Attempts            int        `json:"attempts"`
	LastError           string     `json:"last_error"`
	ClaimedAt           time.Time  `json:"claimed_at"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
}

// FinancialBudgetNotificationStore is meterd's ADR-1500 surface. It reads
// customer intent across accounts and owns only the notification rows.
type FinancialBudgetNotificationStore interface {
	// ListFinancialBudgetNotifyPolicies returns enabled monitored notify
	// budgets across accounts, ordered by account and id, at most limit.
	ListFinancialBudgetNotifyPolicies(ctx context.Context, limit int) ([]FinancialBudget, error)
	// ClaimFinancialBudgetNotification inserts a pending row and reports
	// whether it was new. An existing row for the same budget, month and
	// threshold is left unchanged.
	ClaimFinancialBudgetNotification(ctx context.Context, n FinancialBudgetNotification) (bool, error)
	ListPendingFinancialBudgetNotifications(ctx context.Context, limit int) ([]FinancialBudgetNotification, error)
	// RecordFinancialBudgetNotificationOutcome applies one delivery outcome
	// to every pending row of the budget and month whose threshold is at most
	// maxThreshold, and returns how many rows it changed.
	RecordFinancialBudgetNotificationOutcome(ctx context.Context, policyID string, periodStart time.Time, maxThreshold int64, outcome, lastError string) (int64, error)
}

func validFinancialBudgetNotification(n FinancialBudgetNotification) bool {
	return uuid.Validate(n.AccountID) == nil && uuid.Validate(n.PolicyID) == nil && validFinancialBudgetPeriod(n.PeriodStart) &&
		n.ThresholdMillicents >= 0 && n.ThresholdMillicents <= api.FinancialBudgetMoneyMax &&
		n.PolicyRevision >= 1 && n.PolicyRevision <= api.FinancialBudgetRevisionMax &&
		n.KnownMillicents >= n.ThresholdMillicents && n.KnownMillicents <= api.FinancialBudgetMoneyMax
}

func validFinancialBudgetPeriod(start time.Time) bool {
	start = start.UTC()
	return !start.IsZero() && start.Equal(time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC))
}

func validFinancialBudgetNotificationOutcome(policyID string, periodStart time.Time, maxThreshold int64, outcome, lastError string) bool {
	switch outcome {
	case FinancialBudgetNotificationSent, FinancialBudgetNotificationFailed, FinancialBudgetNotificationAbandoned, FinancialBudgetNotificationCancelled:
	default:
		return false
	}
	return uuid.Validate(policyID) == nil && validFinancialBudgetPeriod(periodStart) && maxThreshold >= 0 &&
		len(lastError) <= api.FinancialBudgetNotifyErrorBytes
}
