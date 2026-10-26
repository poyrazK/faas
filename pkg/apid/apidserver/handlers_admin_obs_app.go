package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

// obsLoadApp resolves the {id} of GET /v1/admin/obs/apps/{id}: the app's
// UUID, or its slug, which is what an operator usually has from a customer
// report or a hostname.
func (s *server) obsLoadApp(ctx context.Context, idOrSlug string) (state.App, *api.Problem) {
	var (
		app state.App
		err error
	)
	switch {
	case uuid.Validate(idOrSlug) == nil:
		app, err = s.store.AppByID(ctx, idOrSlug)
	case validSlug(idOrSlug):
		app, err = s.store.AppBySlug(ctx, idOrSlug)
	default:
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad app id", "expected the app's UUID or slug")
	}
	if errors.Is(err, state.ErrNotFound) {
		return state.App{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "App not found", "no app has this id or slug")
	}
	if err != nil {
		return state.App{}, api.ErrCapacity("could not load the app")
	}
	return app, nil
}

// obsBuildAppDetail reads everything the support view shows. Health,
// snapshots and wakes are best effort: during an incident the rest of the
// page must still answer when one of them cannot be read.
func (s *server) obsBuildAppDetail(ctx context.Context, app state.App, rng string) (api.ObsAppDetailResponse, *api.Problem) {
	deployments, err := s.store.ListDeploymentsForApp(ctx, app.ID, 100, 0)
	if err != nil {
		return api.ObsAppDetailResponse{}, api.ErrCapacity("could not list app deployments")
	}
	instances, err := s.store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		return api.ObsAppDetailResponse{}, api.ErrCapacity("could not list app instances")
	}
	invocations, err := s.store.ListInvocationsForApp(ctx, app.ID)
	if err != nil {
		return api.ObsAppDetailResponse{}, api.ErrCapacity("could not list app invocations")
	}
	if len(invocations) > obsOpsActivityLimitMax {
		invocations = invocations[:obsOpsActivityLimitMax]
	}
	health := s.obsAppHealth(ctx, app, rng)
	snapshots, err := operatorobs.AppSnapshots(ctx, s.store, app.ID)
	if err != nil {
		s.log.Warn("obs app: snapshots", "app_id", app.ID, "err", err)
		snapshots = []api.ObsAppSnapshot{}
	}
	resp := api.ObsAppDetailResponse{
		App: api.ObsAppDetail{
			ID:             app.ID,
			AccountID:      app.AccountID,
			Slug:           app.Slug,
			Type:           string(app.Type),
			Runtime:        app.Runtime,
			Status:         string(app.Status),
			RAMMB:          app.RAMMB,
			MaxConcurrency: app.MaxConcurrency,
			MinInstances:   app.MinInstances,
			CreatedAt:      app.CreatedAt,
		},
		Deployments: projectObsDeployments(deployments),
		Instances:   projectObsInstances(instances, map[string]string{app.ID: app.Slug}, map[string]string{app.ID: app.AccountID}, s.nodeNames(ctx)),
		Invocations: projectObsInvocations(invocations, map[string]string{app.ID: app.Slug}, obsOpsActivityLimitMax),
		Health:      health,
		Snapshots:   snapshots,
	}
	now := time.Now().UTC()
	if wakes, err := buildAppWakeTimelineWindow(ctx, s.store, app, s.log, state.Account{ID: app.AccountID}, now.Add(-24*time.Hour), now); err == nil {
		resp.Wakes = &wakes
	}
	return resp, nil
}
