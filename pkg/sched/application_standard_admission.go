// adr: 435 — pending inherited intent cannot use legacy runtime entry points.
package sched

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) checkApplicationStandardAdmission(ctx context.Context, app state.App) error {
	if err := e.checkFinancialBudgetAdmission(ctx, app, ""); err != nil {
		return err
	}
	return e.checkApplicationStandardEnrollment(ctx, app)
}

// checkApplicationStandardEnrollment is checkApplicationStandardAdmission
// without its budget check, for a caller that checks the budget for the
// deployment it admits (a superset of the check without one).
func (e *Engine) checkApplicationStandardEnrollment(ctx context.Context, app state.App) error {
	if app.OrgID == "" { // Legacy unowned MemStore fixtures have no enrollment.
		return nil
	}
	reader, ok := e.store.(interface {
		GetApplicationStandardEnrollment(context.Context, string, string) (state.ApplicationStandardEnrollment, error)
	})
	if !ok {
		return fmt.Errorf("sched: application standard enrollment reader unavailable")
	}
	enrollment, err := reader.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return fmt.Errorf("sched: read application standard enrollment: %w", err)
	}
	if err != nil || !state.ApplicationStandardEnrollmentPermitsRuntime(app, enrollment) {
		return errors.Join(state.ErrApplicationStandardsPending, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardsPending,
			"Application standards pending", "Inherited application controls must be persisted before this service accepts runtime admission."))
	}
	return nil
}

func (e *Engine) checkCapturedApplicationStandardAdmission(ctx context.Context, id string, app state.App, account state.Account, deployment state.Deployment) error {
	_, _, err := e.checkedCapturedApplicationStandardAdmission(ctx, id, app, account, deployment)
	return err
}

// checkedCapturedApplicationStandardAdmission also returns the capture it
// checked; read is false for an app outside an organization, which has none.
func (e *Engine) checkedCapturedApplicationStandardAdmission(ctx context.Context, id string, app state.App, account state.Account, deployment state.Deployment) (state.InstanceApplicationStandardAdmission, bool, error) {
	if err := e.checkFinancialBudgetAdmission(ctx, app, deployment.ID); err != nil {
		return state.InstanceApplicationStandardAdmission{}, false, err
	}
	if app.OrgID == "" {
		return state.InstanceApplicationStandardAdmission{}, false, nil
	}
	store, ok := e.store.(state.InstanceApplicationStandardAdmissionStore)
	if !ok {
		return state.InstanceApplicationStandardAdmission{}, false, fmt.Errorf("sched: runtime standards capture reader unavailable")
	}
	capture, err := state.CheckedInstanceApplicationStandardAdmission(ctx, store, id, app, account, deployment)
	if err != nil {
		return state.InstanceApplicationStandardAdmission{}, false, fmt.Errorf("sched: validate captured runtime inputs: %w", applicationStandardRuntimeProblem(err))
	}
	return capture, true, nil
}

func applicationStandardRuntimeProblem(err error) error {
	if !errors.Is(err, state.ErrApplicationStandardsPending) && !errors.Is(err, state.ErrApplicationStandardRuntimeStale) && !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
		return err
	}
	if errors.Is(err, state.ErrApplicationStandardRuntimeBusy) && !errors.Is(err, state.ErrApplicationStandardsPending) && !errors.Is(err, state.ErrApplicationStandardRuntimeStale) {
		// A busy fence means another operation on the app held its runtime
		// state; nothing about its standards changed. The old wording sent a
		// customer whose deploy hit a busy fence looking for a policy update
		// (Hunt 14 H14-32).
		return errors.Join(err, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardsPending,
			"Runtime state busy", "Another operation on this app was updating its runtime state. Retry the request; it is safe to repeat."))
	}
	return errors.Join(err, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardsPending,
		"Application standards pending", "Application standards changed during startup. Retry the request once the policy update has finished."))
}
