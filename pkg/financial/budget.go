package financial

import (
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/google/uuid"
)

type BudgetScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// BudgetSpec is customer intent. Readiness and enforcement acknowledgements
// are separate facts; saving this intent must not imply that targets stopped.
type BudgetSpec struct {
	Name             string      `json:"name"`
	Scope            BudgetScope `json:"scope"`
	Currency         string      `json:"currency"`
	Meters           []string    `json:"meters"`
	Basis            string      `json:"basis"`
	LimitMillicents  int64       `json:"limit_millicents"`
	NotifyMillicents []int64     `json:"notify_millicents"`
	Mode             string      `json:"mode"`
	Action           string      `json:"action"`
	DrainSeconds     int         `json:"drain_seconds"`
	ResumeRule       string      `json:"resume_rule"`
	Enabled          bool        `json:"enabled"`
}

// ValidateBudget checks the semantic contract. Store/API adapters separately
// enforce operational bounds from pkg/api/limits.go and account ownership.
func ValidateBudget(p BudgetSpec) error {
	if strings.TrimSpace(p.Name) == "" || p.Currency != "EUR" || p.LimitMillicents < 0 || p.DrainSeconds < 0 {
		return fmt.Errorf("%w: budget name, currency, amount or drain", ErrInvalid)
	}
	switch p.Scope.Kind {
	case "account":
		if p.Scope.ID != "" {
			return fmt.Errorf("%w: account scope has no resource id", ErrInvalid)
		}
	case "project", "environment", "app", "job":
		if p.Scope.ID == "" {
			return fmt.Errorf("%w: scope requires resource id", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: budget scope", ErrInvalid)
	}
	if len(p.Meters) == 0 || !slices.IsSorted(p.Meters) {
		return fmt.Errorf("%w: meters must be sorted and unique", ErrInvalid)
	}
	for i, meter := range p.Meters {
		if (meter != "compute" && meter != "egress") || (i > 0 && p.Meters[i-1] == meter) {
			return fmt.Errorf("%w: unsupported or duplicate meter", ErrInvalid)
		}
	}
	if p.Basis != "net_usage" && p.Basis != "gross_usage" {
		return fmt.Errorf("%w: budget basis", ErrInvalid)
	}
	if p.Mode != "monitored" && p.Mode != "strict" {
		return fmt.Errorf("%w: budget mode", ErrInvalid)
	}
	switch p.Action {
	case "notify", "reject_traffic", "suspend_background", "stop_previews", "suspend_workloads":
	default:
		return fmt.Errorf("%w: budget action", ErrInvalid)
	}
	if p.Scope.Kind == "job" && (p.Action == "reject_traffic" || p.Action == "stop_previews") {
		return fmt.Errorf("%w: action cannot target jobs", ErrInvalid)
	}
	if p.Action == "notify" && p.DrainSeconds != 0 {
		return fmt.Errorf("%w: notify has no drain", ErrInvalid)
	}
	if p.ResumeRule != "manual" && p.ResumeRule != "next_period" {
		return fmt.Errorf("%w: budget resume rule", ErrInvalid)
	}
	for i, amount := range p.NotifyMillicents {
		if amount < 0 || amount > p.LimitMillicents || (i > 0 && amount <= p.NotifyMillicents[i-1]) {
			return fmt.Errorf("%w: notify thresholds must increase up to the limit", ErrInvalid)
		}
	}
	if p.Mode == "strict" {
		if len(p.Meters) != 1 || p.Meters[0] != "compute" || p.Action == "notify" || p.Action == "reject_traffic" {
			return fmt.Errorf("%w: strict compute requires a stopping action", ErrInvalid)
		}
		// An unrelated workload can consume the shared account allowance and
		// increase an app's proportional net allocation after its admission.
		// Strict scoped budgets therefore use gross compute reservations.
		if p.Scope.Kind != "account" && p.Basis != "gross_usage" {
			return fmt.Errorf("%w: strict scoped compute requires gross_usage", ErrInvalid)
		}
		if p.Action != "suspend_workloads" && p.Scope.Kind != "app" && p.Scope.Kind != "job" {
			return fmt.Errorf("%w: selective stopping cannot bound every workload in a broad scope", ErrInvalid)
		}
	}
	return nil
}

func (s BudgetScope) Matches(a Attribution) bool {
	switch s.Kind {
	case "account":
		return s.ID == ""
	case "project":
		return sameBudgetIdentity(s.ID, a.ProjectID)
	case "environment":
		return sameBudgetIdentity(s.ID, a.EnvironmentID)
	case "app":
		return sameBudgetIdentity(s.ID, a.AppID)
	case "job":
		return sameBudgetIdentity(s.ID, a.JobID)
	default:
		return false
	}
}

func sameBudgetIdentity(scope, retained string) bool {
	if scope == "" || retained == "" {
		return false
	}
	if scope == retained {
		return true
	}
	a, err := uuid.Parse(scope)
	if err != nil {
		return false
	}
	b, err := uuid.Parse(retained)
	return err == nil && a == b
}

// BudgetAmount consumes allocations from one account/period, after the shared
// allowance was applied once. It never recomputes an allowance per scope.
func BudgetAmount(p BudgetSpec, costs []ContractCosts) (int64, error) {
	if err := ValidateBudget(p); err != nil {
		return 0, err
	}
	total := new(big.Int)
	for _, cost := range costs {
		if !slices.Contains(p.Meters, cost.Meter) {
			continue
		}
		for _, contract := range cost.Contracts {
			if contract.Price.Meter != cost.Meter || contract.Price.Currency != p.Currency {
				return 0, ErrInvalid
			}
			for _, allocation := range contract.Allocations {
				if !p.Scope.Matches(allocation.Attribution) {
					continue
				}
				amount := allocation.NetMillicents
				if p.Basis == "gross_usage" {
					amount = allocation.GrossMillicents
				}
				if amount < 0 {
					return 0, ErrInvalid
				}
				total.Add(total, big.NewInt(amount))
			}
		}
	}
	if !total.IsInt64() {
		return 0, ErrOverflow
	}
	return total.Int64(), nil
}
