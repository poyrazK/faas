package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/apid/serviceadditions"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) validateProjectAdditionRequest(r *http.Request, req *scanPlanRequest) *api.Problem {
	header := r.Header.Get(api.ProjectAddServicesHeader)
	if err := serviceadditions.ValidateHeader(header, len(r.Header.Values(api.ProjectAddServicesHeader)), r.Header.Get(api.ProjectSafeReleaseHeader), len(req.ReleaseConfig) > 0, r.Header.Get(api.ProjectWorkerHealthHeader) != "", r.Header.Get(api.ProjectProgressiveHeader) != ""); err != nil {
		return api.ErrValidation(err.Error())
	}
	if header != "" && !s.atomicServiceAdditionsEnabled {
		return api.ErrCapacity("atomic service additions are disabled pending native qualification")
	}
	return nil
}

func (s *server) stageSafeProjectAdditions(ctx context.Context, account state.Account, projectID string, safe *api.ProjectSafeRelease, apps []state.App) ([]state.App, *api.Problem) {
	if !safe.AddServices {
		return apps, nil
	}
	tracker := deploystaging.ProjectSubmissionFromContext(ctx)
	store, ok := s.store.(state.ProjectReleaseAdditionsStore)
	if tracker == nil || !ok {
		return nil, api.ErrCapacity("service addition reservations are unavailable")
	}
	selected, err := serviceadditions.Stage(ctx, store, account.ID, tracker.Receipt.AttemptID, projectID, safe, apps)
	if err != nil {
		return nil, serviceadditions.ReservationProblem(err, account.Plan)
	}
	serviceadditions.AuditReservations(ctx, s.audit, account.ID, tracker.Receipt.AttemptID, projectID, safe)
	return selected, nil
}

func (s *server) WithAtomicServiceAdditionsEnabled(enabled bool) *server {
	s.atomicServiceAdditionsEnabled = enabled
	return s
}
