// adr: 385 — pending inherited intent cannot use legacy runtime entry points.
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

func (e *Engine) checkApplicationStandardAdmissionByID(ctx context.Context, appID string) error {
	app, err := e.store.AppByID(ctx, appID)
	if err != nil {
		return fmt.Errorf("sched: read application for standard admission: %w", err)
	}
	return e.checkApplicationStandardAdmission(ctx, app)
}
