package apidserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	pipelineapi "github.com/onebox-faas/faas/pkg/apid/datapipelines"
	"github.com/onebox-faas/faas/pkg/datapipeline"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) configurePipelineDataAPI(service *datapipeline.Service) {
	if s.managedPostgresBindings == nil || s.notif == nil {
		return
	}
	setup := &pipelineapi.DataAPISetup{Store: s.store, Bindings: s.managedPostgresBindings, Notifier: s.notif, Log: s.log, Spool: spoolRoot(), Origin: s.dashboardAppOrigin, AppCreated: s.emitAppCreated,
		AppVisible: func(ctx context.Context, acct state.Account, app state.App) bool {
			return accountViewAppVisible(ctx, s.store, s.log, acct, app)
		}}
	setup.BuildApp = func(a state.Account, name string) (state.App, error) {
		requireAuthn := false
		app, problem := s.buildApp(a, api.CreateAppRequest{Slug: name, Type: "app", RequireAuthn: &requireAuthn}, api.MustLimitsFor(a.Plan))
		if problem != nil {
			return app, datapipeline.ErrInvalid
		}
		return app, nil
	}
	setup.ConfigureApp = s.configurePipelineAPIApp
	setup.AdmitDeploy = func(ctx context.Context, a state.Account, app state.App) error {
		if problem := s.enforceSecurityPostureGate(ctx, app); problem != nil {
			return datapipeline.ErrUnavailable
		}
		snapshot, err := s.consumeAccountDeployRate(ctx, a, time.Now().UTC())
		if err != nil {
			return datapipeline.ErrUnavailable
		}
		if !snapshot.Allowed {
			return datapipeline.ErrQuota
		}
		return nil
	}
	service.PlanDataAPI, service.PrepareDataAPI, service.DeployDataAPI, service.DataAPIState = setup.Plan, setup.Prepare, setup.Deploy, setup.State
}

func (s *server) configurePipelineAPIApp(ctx context.Context, a state.Account, app state.App) error {
	r := httptest.NewRequest(http.MethodPatch, "/v1/apps/"+app.Slug, nil).WithContext(ctx)
	r.SetPathValue("slug", app.Slug)
	ports, requireAuthn := []int{5432}, false
	response := s.forwardDashboardJSON(r, a, http.MethodPatch, r.URL.Path, api.DefaultEnvScope, "", api.UpdateAppRequest{EgressPorts: &ports, RequireAuthn: &requireAuthn, PublicAuth: &api.PublicAuthBlock{Mode: "open"}, SetPublicAuth: true}, s.updateApp)
	if response.Code < 200 || response.Code >= 300 {
		return datapipeline.ErrUnavailable
	}
	return nil
}
