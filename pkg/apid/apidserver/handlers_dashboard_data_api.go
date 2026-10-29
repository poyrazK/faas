// adr: 968 — owner tooling runs schema inspection inside the deployed app.
package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/dataapiusage"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dataapi"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardDataAPIAction = "data_api_tools"
const dashboardDataAPICookie = "gregale_data_api_csrf"

func (s *server) dashboardDataAPIApp(w http.ResponseWriter, r *http.Request) (state.Account, state.App, bool) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return state.Account{}, state.App{}, false
	}
	slug := r.PathValue("slug")
	if !validSlug(slug) {
		http.NotFound(w, r)
		return acct, state.App{}, false
	}
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID || app.Status == state.AppDeleted {
		http.NotFound(w, r)
		return acct, state.App{}, false
	}
	return acct, app, true
}

// dataAPITaskCommand is the dashboard's subset of the contract tasks: the
// schema view and the TypeScript download.
func dataAPITaskCommand(kind string) []string {
	if kind == string(api.DataAPIContractSchema) || kind == string(api.DataAPIContractTypes) {
		return api.DataAPIContractTaskKind(kind).Command()
	}
	return nil
}

func dataAPITaskKind(task state.AppTask) string {
	kind := string(api.DataAPIContractTaskKindOf(task.Command, task.CommandShell))
	if dataAPITaskCommand(kind) == nil {
		return ""
	}
	return kind
}

func (s *server) renderDashboardDataAPI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), api.AppOperationalReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	acct, app, ok := s.dashboardDataAPIApp(w, r)
	if !ok {
		return
	}
	data := dashboard.DataAPIData{Slug: app.Slug, Enabled: s.appTaskAPIEnabled}
	data.CSRFToken = s.issueDashboardConfigCSRF(w, s.log, dashboardDataAPIAction, dashboardDataAPICookie, acct.ID, app.ID)
	s.populateDataAPIWorkspace(r, acct, app, &data)
	count, _ := s.store.CountDeployedApps(ctx, acct.ID)
	page := dashboard.Page{Title: "Data API — " + app.Slug, Body: "data_api", Account: dashboardAccountView(acct, count), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) populateDataAPIWorkspace(r *http.Request, acct state.Account, app state.App, data *dashboard.DataAPIData) {
	if s.managedPostgresBindings != nil {
		bindings, err := s.managedPostgresBindings.ListForApp(r.Context(), acct.ID, app.ID, "default")
		if err != nil {
			data.Message = "Database binding status is unavailable."
		} else {
			for _, binding := range bindings {
				if binding.Access == "data_api" {
					data.Bindings = append(data.Bindings, binding)
				}
			}
		}
	}
	s.populateDataAPIUsageAndAccess(r, acct, app, data)
	id := r.URL.Query().Get("task")
	if id == "" || !s.appTaskAPIEnabled {
		return
	}
	task, err := s.store.AppTaskByID(r.Context(), acct.ID, app.ID, id)
	if err != nil || dataAPITaskKind(task) == "" {
		data.Message = "The selected Data API operation is unavailable."
		return
	}
	data.TaskID, data.TaskStatus = task.ID, string(task.Status)
	if !task.Status.Terminal() {
		data.RefreshSeconds = int(api.AppRestartDashboardRefresh / time.Second)
		return
	}
	if !s.currentDataAPITask(r, app, task) {
		data.Message = "This result is incomplete, failed, or belongs to an earlier deployment. Run the operation again."
		return
	}
	if dataAPITaskKind(task) == "schema" {
		contract, err := dataapi.Parse([]byte(task.StdoutTail))
		if err != nil {
			data.Message = "The app returned an invalid schema contract."
			return
		}
		data.Fingerprint, data.Schema = contract.Fingerprint, &contract.Parsed
	} else if fingerprint := dashboardDataAPITypeFingerprint(task.StdoutTail); fingerprint != "" {
		data.Fingerprint, data.TypesReady = fingerprint, true
	} else {
		data.Message = "The app returned an invalid TypeScript contract."
	}
}

// populateDataAPIUsageAndAccess adds the last week's usage and which owner
// settings are present (ADR-20261019102108).
func (s *server) populateDataAPIUsageAndAccess(r *http.Request, acct state.Account, app state.App, data *dashboard.DataAPIData) {
	if s.dataAPIUsage == nil {
		data.UsageMessage = "Usage is not available on this host."
	} else if usage, err := dataapiusage.View(r.Context(), s.dataAPIUsage, app.ID, app.Slug, time.Now(), 7*24*time.Hour, "day"); err != nil {
		data.UsageMessage = "Usage is temporarily unavailable."
	} else {
		data.Usage = dashboard.NewDataAPIUsageView(usage)
	}
	secrets, err := s.store.ListAppSecretsInScope(r.Context(), acct.ID, app.ID, api.DefaultEnvScope)
	if err != nil {
		return
	}
	keys := make(map[string]bool, len(secrets))
	for _, secret := range secrets {
		keys[secret.Key] = true
	}
	data.Access, data.ServerKeys = dashboard.DataAPIAccessSettings(app.Slug, keys)
}

func (s *server) currentDataAPITask(r *http.Request, app state.App, task state.AppTask) bool {
	if task.Status != state.AppTaskSucceeded || task.ExitCode == nil || *task.ExitCode != 0 || task.OutputTruncated || len(task.StdoutTail) > api.AppTaskDefaultMaxOutputBytes {
		return false
	}
	deployment, err := s.store.LiveDeployment(r.Context(), app.ID)
	return err == nil && deployment.ID == task.DeploymentID && normalizedDataAPIScope(deployment.Scope) == normalizedDataAPIScope(task.DeploymentScope)
}

func (s *server) runDashboardDataAPI(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardDataAPIApp(w, r)
	if !ok || !s.verifyDashboardConfigCSRF(w, r, dashboardDataAPIAction, dashboardDataAPICookie, acct.ID) {
		return
	}
	operation := r.PathValue("operation")
	if dataAPITaskCommand(operation) == nil {
		http.NotFound(w, r)
		return
	}
	if !s.requireAppTaskAPI(w) {
		return
	}
	resolved, problem := api.DataAPIContractTaskKind(operation).Request().Resolve()
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	task, problem := s.admitAppTask(r, acct, app, resolved)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+app.Slug+"/data-api?task="+task.ID, http.StatusSeeOther)
}

var dashboardDataAPIFingerprint = regexp.MustCompile(`(?m)^// Schema fingerprint: ([a-f0-9]{64})$`)

func dashboardDataAPITypeFingerprint(content string) string {
	if !strings.HasPrefix(content, "// Generated by gregale data-api types. Do not edit.\n") {
		return ""
	}
	matches := dashboardDataAPIFingerprint.FindAllStringSubmatch(content, -1)
	if len(matches) != 1 {
		return ""
	}
	return matches[0][1]
}

func (s *server) downloadDashboardDataAPITypes(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardDataAPIApp(w, r)
	if !ok {
		return
	}
	task, err := s.store.AppTaskByID(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if err != nil || dataAPITaskKind(task) != "types" || !s.currentDataAPITask(r, app, task) || dashboardDataAPITypeFingerprint(task.StdoutTail) == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="database.types.ts"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(task.StdoutTail))
}

func (s *server) refreshDashboardDataAPI(w http.ResponseWriter, r *http.Request) {
	acct, app, ok := s.dashboardDataAPIApp(w, r)
	if !ok || !s.verifyDashboardConfigCSRF(w, r, dashboardDataAPIAction, dashboardDataAPICookie, acct.ID) {
		return
	}
	// Reuse the existing runtime refresh admission, audit and durable handoff.
	request := r.Clone(r.Context())
	u := *r.URL
	u.RawQuery = "fresh=true"
	request.URL = &u
	resp := s.forwardDashboardJSON(request, acct, http.MethodPost, "/v1/apps/"+app.Slug+"/restart", "", "", nil, func(w http.ResponseWriter, forwarded *http.Request, acct state.Account) {
		forwarded.URL.RawQuery = "fresh=true"
		s.restartApp(w, forwarded, acct)
	})
	if !dashboardMutationSucceeded(w, resp) {
		return
	}
	var accepted api.AppRestartResponse
	if json.Unmarshal(resp.Body.Bytes(), &accepted) != nil || !dashboardRetryDeploymentIDRe.MatchString(accepted.WakeID) {
		api.WriteProblem(w, api.ErrInternal("could not read accepted refresh status"))
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+app.Slug+"/restarts/"+accepted.WakeID, http.StatusSeeOther)
}

func normalizedDataAPIScope(scope string) string {
	if scope == "" {
		return "default"
	}
	return scope
}
