package state

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

var ErrFinancialBudgetHeld = errors.New("state: workload held by a compute budget")

func FinancialBudgetHeldProblem() error {
	return errors.Join(ErrFinancialBudgetHeld, api.NewProblem(http.StatusPaymentRequired, "financial_budget_held",
		"Preview compute budget reached", "This preview is held by a monitored compute budget. Change or disable the stopping policy to resume it, or wait for its configured UTC period reset."))
}

// FinancialBudgetStopsForDeployment reads authoritative decisions on every
// admission. A cached routing app cannot hide a newly created stopping policy.
func FinancialBudgetStopsForDeployment(ctx context.Context, store Store, app App, deploymentID string, now time.Time) ([]FinancialBudgetStop, error) {
	if app.PreviewOfSlug == "" {
		return nil, nil
	}
	reader, ok := store.(FinancialBudgetStopStore)
	if !ok {
		return nil, nil // Legacy adapters cannot activate stopping policies.
	}
	stops, err := reader.ActiveFinancialBudgetStops(ctx, app.AccountID, now)
	if err != nil {
		return nil, fmt.Errorf("read preview budget decisions: %w", err)
	}
	out := []FinancialBudgetStop{}
	for _, stop := range stops {
		if stop.Spec.Scope.Kind == "environment" {
			if deploymentID == "" {
				continue
			}
			matches, err := MatchFinancialBudgetEnvironment(ctx, store, app, stop.Spec.Scope.ID, deploymentID)
			if err != nil {
				return nil, err
			}
			if matches {
				out = append(out, stop)
			}
			continue
		}
		if stop.Matches(app, "") {
			out = append(out, stop)
		}
	}
	return out, nil
}

func CheckFinancialBudgetAdmission(ctx context.Context, store Store, app App, deploymentID string, now time.Time) error {
	if err := checkFinancialComputeHold(ctx, store, app, func() (string, error) { return deploymentID, nil }, now); err != nil {
		return err
	}
	return checkFinancialBudgetStops(ctx, store, app, deploymentID, now)
}

// checkFinancialComputeHold rejects work a held strict cap stops. An
// environment cap holds only deployments in its environment; deployment is
// resolved only when one applies. Without a deployment, vmmd's acquire and
// renewal remain the environment's boundary.
func checkFinancialComputeHold(ctx context.Context, store Store, app App, deployment func() (string, error), now time.Time) error {
	compute, ok := store.(FinancialComputeStore)
	if !ok {
		return nil
	}
	policies, err := compute.FinancialComputePolicies(ctx, app.AccountID, app.ID)
	if err != nil {
		return errors.Join(err, api.ErrCapacity("compute budget admission unavailable"))
	}
	environment, resolved := "", false
	for _, p := range policies {
		if !p.Spec.Enabled || !financial.StrictCompute(p.Spec) {
			continue
		}
		if p.Spec.Scope.Kind == "environment" {
			if !resolved {
				if environment, err = financialComputeEnvironment(ctx, compute, app, deployment); err != nil {
					return errors.Join(err, api.ErrCapacity("compute budget admission unavailable"))
				}
				resolved = true
			}
			if environment == "" || p.Spec.Scope.ID != environment {
				continue
			}
		}
		balance, err := compute.FinancialComputeBalance(ctx, p, now)
		if err != nil {
			return errors.Join(err, api.ErrCapacity("compute budget authority unavailable"))
		}
		if balance.Held {
			return errors.Join(ErrFinancialBudgetHeld, api.NewProblem(402, "financial_budget_held", "Compute budget held", "A strict compute budget cannot fund another execution window. Change its policy or wait for its configured UTC reset."))
		}
	}
	return nil
}

func financialComputeEnvironment(ctx context.Context, compute FinancialComputeStore, app App, deployment func() (string, error)) (string, error) {
	id, err := deployment()
	if err != nil || id == "" {
		return "", err
	}
	environment, err := compute.FinancialComputeDeploymentEnvironment(ctx, app.AccountID, app.ID, id)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	return environment, err
}

func checkFinancialBudgetStops(ctx context.Context, store Store, app App, deploymentID string, now time.Time) error {
	stops, err := FinancialBudgetStopsForDeployment(ctx, store, app, deploymentID, now)
	if err != nil {
		return errors.Join(err, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Preview budget admission unavailable", "The preview budget state could not be checked. Retry shortly."))
	}
	if len(stops) > 0 {
		return FinancialBudgetHeldProblem()
	}
	return nil
}

// The scope comes from the authenticated routing/dispatch selection. A runtime
// environment is resolved through its deployment, never by matching its name to
// a budget's environment UUID. Policies and stops are each read once.
func CheckFinancialBudgetAdmissionForScope(ctx context.Context, store Store, app App, scope, deploymentID string, now time.Time) error {
	resolved, resolveErr := deploymentID != "", error(nil)
	deployment := func() (string, error) {
		if !resolved {
			resolved = true
			dep, err := ResolveEnvironmentDeployment(ctx, store, app.ID, scope)
			if err == nil {
				deploymentID = dep.ID
			} else if !errors.Is(err, ErrNotFound) {
				resolveErr = err
			}
		}
		return deploymentID, resolveErr
	}
	if err := checkFinancialComputeHold(ctx, store, app, deployment, now); err != nil || app.PreviewOfSlug == "" {
		return err
	}
	if _, err := deployment(); err != nil {
		// An account- or app-scoped stop still answers before the
		// unresolved environment does.
		if stopErr := checkFinancialBudgetStops(ctx, store, app, "", now); stopErr != nil {
			return stopErr
		}
		return err
	}
	return checkFinancialBudgetStops(ctx, store, app, deploymentID, now)
}

// MatchFinancialBudgetEnvironment compares retained runtime ownership. An
// unknown lifetime in the same named scope is unavailable, never proof that a
// resident VM belongs outside the budget.
func MatchFinancialBudgetEnvironment(ctx context.Context, store Store, app App, environmentID, deploymentID string) (bool, error) {
	if app.ProjectID == "" {
		return false, nil
	}
	reader, ok := store.(interface {
		ProjectEnvironmentByID(context.Context, string) (ProjectEnvironment, error)
	})
	if !ok {
		return false, fmt.Errorf("budget environment ownership reader unavailable")
	}
	env, err := reader.ProjectEnvironmentByID(ctx, environmentID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if env.AccountID != app.AccountID || env.ProjectID != app.ProjectID {
		return false, nil
	}
	runtime, ok := store.(RuntimeAppEnvStore)
	if !ok {
		return false, fmt.Errorf("budget runtime environment reader unavailable")
	}
	owner, err := runtime.RuntimeAppEnvForDeployment(ctx, app.AccountID, app.ID, deploymentID)
	if err != nil {
		return false, fmt.Errorf("resolve budget runtime environment: %w", err)
	}
	if owner.EnvironmentID == "" && workloadEnvironmentSlug(owner.Scope) == env.Slug {
		return false, fmt.Errorf("%w: budget runtime environment lifetime is unknown", ErrConflict)
	}
	return owner.EnvironmentID == env.ID, nil
}
