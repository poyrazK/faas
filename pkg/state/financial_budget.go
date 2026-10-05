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
