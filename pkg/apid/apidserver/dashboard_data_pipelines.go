package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	pipelineapi "github.com/onebox-faas/faas/pkg/apid/datapipelines"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardPipelinesAction = "data_pipelines"
const dashboardPipelinesCookie = "gregale_data_pipelines_csrf"

func (s *server) renderDashboardPipelines(w http.ResponseWriter, r *http.Request) {
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	data, err := s.dataPipelines.Workspace(r.Context(), account.ID, r.URL.Query().Get("connection"))
	if err != nil {
		pipelineapi.WriteProblem(w, err)
		return
	}
	data.CSRFToken = s.issueDashboardConfigCSRF(w, s.log, dashboardPipelinesAction, dashboardPipelinesCookie, account.ID, "")
	if data.Enabled && data.DataAPIEnabled {
		s.pipelineAuthChoices(r, account.ID, &data)
	}
	if s.managedPostgres != nil && data.Enabled {
		databases, err := s.managedPostgres.List(r.Context(), account.ID)
		if err != nil {
			data.Message = "Managed database choices are temporarily unavailable."
		} else {
			for _, item := range projectManagedPostgresDashboard(databases) {
				if item.State == "ready" && item.PostgresMajor == 16 {
					data.Databases = append(data.Databases, item)
				}
			}
		}
	}
	count, _ := s.store.CountDeployedApps(r.Context(), account.ID)
	page := dashboard.Page{Title: "Data connections", Body: "data_pipelines", Account: dashboardAccountView(account, count), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) pipelineAuthChoices(r *http.Request, account string, data *dashboard.DataPipelinesData) {
	apps, err := s.store.ListApps(r.Context(), account)
	if err != nil {
		data.Message = "Auth app choices are temporarily unavailable."
		return
	}
	for _, app := range apps {
		if app.Status != state.AppActive || app.Type != state.AppTypeApp {
			continue
		}
		isAuth, err := s.appHasAuthBinding(r.Context(), state.Account{ID: account}, app)
		if err != nil {
			data.Message = "Auth app choices are temporarily unavailable."
			return
		}
		if !isAuth {
			continue
		}
		data.AuthApps = append(data.AuthApps, dashboard.DataPipelineAuthApp{ID: app.ID, Name: app.Slug})
	}
}

func (s *server) dashboardPipelineAction(w http.ResponseWriter, r *http.Request) {
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.DataPipelineRequestMaxBytes)
	if !s.verifyDashboardConfigCSRF(w, r, dashboardPipelinesAction, dashboardPipelinesCookie, account.ID) {
		return
	}
	s.dataPipelines.DashboardAction(w, r, account.ID)
}
