package apidserver

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/automationconsole"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const automationConsoleCookie = "gregale_automation_csrf"

type automationConsoleHost struct{ server *server }

func (h automationConsoleHost) Scope(w http.ResponseWriter, r *http.Request) (automationconsole.Scope, bool) {
	acct, app, ok := h.server.dashboardDataAPIApp(w, r)
	if !ok {
		return automationconsole.Scope{}, false
	}
	return automationconsole.Scope{AccountID: acct.ID, AppID: app.ID, Slug: app.Slug, Account: dashboardAccountView(acct, 0)}, true
}
func (h automationConsoleHost) IssueCSRF(w http.ResponseWriter, scope automationconsole.Scope) string {
	token, err := middleware.IssueForAuthenticatedNamed(h.server.sessions, "automation_console:"+scope.AppID, scope.AccountID, automationConsoleCookie)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: automationConsoleCookie, Value: token,
		Path: "/dashboard/apps/" + url.PathEscape(scope.Slug) + "/automations", HttpOnly: true,
		Secure: h.server.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return token
}
func (h automationConsoleHost) VerifyCSRF(w http.ResponseWriter, r *http.Request, scope automationconsole.Scope) bool {
	return h.server.verifyDashboardConfigCSRF(w, r, "automation_console:"+scope.AppID, automationConsoleCookie, scope.AccountID)
}

func (h automationConsoleHost) Request(r *http.Request, scope automationconsole.Scope, operation automationconsole.Operation) automationconsole.Response {
	acct, ok := AccountFrom(r.Context())
	if !ok || acct.ID != scope.AccountID {
		return automationconsole.Response{Status: http.StatusUnauthorized}
	}
	method, path, handler := h.endpoint(scope.Slug, operation)
	if handler == nil {
		return automationconsole.Response{Status: http.StatusNotFound}
	}
	request := r.Clone(r.Context())
	request.Header = r.Header.Clone()
	request.Header.Del("Idempotency-Key")
	// The dashboard actor comes from its cookie session, never a competing bearer.
	request.Header.Del("Authorization")
	if operation.Key != "" {
		request.Header.Set("Idempotency-Key", operation.Key)
	}
	setAutomationConsolePaths(request, scope.Slug, operation)
	next := accountHandler(handler)
	scopes := api.ScopesReadSurface
	switch operation.Kind {
	case "save", "publish", "check", "enabled", "run", "resume", "cancel", "bind-webhook", "unbind-webhook":
		scopes = api.ScopesDeployWriteSurface
		next = h.server.requireVerifiedEmail(next)
	}
	authenticated := h.server.auth(h.server.requireScope(scopes...)(next))
	response := h.server.forwardDashboardJSON(request, acct, method, path, "", "", operation.Body,
		func(w http.ResponseWriter, forwarded *http.Request, account state.Account) {
			if operation.Kind == "runs" {
				forwarded.URL.RawQuery = url.Values{"workflow_name": {operation.Name}, "limit": {"20"}}.Encode()
			} else if operation.Kind == "unbind-webhook" {
				forwarded.URL.RawQuery = url.Values{"expected_version": {strconv.FormatInt(operation.Version, 10)}}.Encode()
			}
			authenticated(w, forwarded)
		})
	return automationconsole.Response{Status: response.Code, Body: response.Body.Bytes()}
}

func setAutomationConsolePaths(r *http.Request, slug string, operation automationconsole.Operation) {
	r.SetPathValue("slug", slug)
	r.SetPathValue("name", operation.Name)
	r.SetPathValue("id", operation.RunID)
	r.SetPathValue("step", operation.Step)
	if operation.EndpointID != "" {
		r.SetPathValue("id", operation.EndpointID)
	}
	r.SetPathValue("event_id", operation.EventID)
}

func (h automationConsoleHost) endpoint(slug string, op automationconsole.Operation) (string, string, dashboardJSONHandler) {
	root := "/v1/apps/" + url.PathEscape(slug) + "/automations"
	named := root + "/" + url.PathEscape(op.Name)
	switch op.Kind {
	case "list":
		return http.MethodGet, root, h.server.listAutomations
	case "get":
		return http.MethodGet, named, h.server.getAutomation
	case "policy":
		return http.MethodGet, root + ":publish-policy", h.server.getAutomationPublishPolicy
	case "validate":
		return http.MethodPost, root + ":validate", h.server.validateAutomation
	case "simulate":
		return http.MethodPost, root + ":simulate", h.server.simulateAutomation
	case "save":
		return http.MethodPut, named, h.server.saveAutomationDraft
	case "check":
		return http.MethodPost, named + "/publish-check", h.server.checkAutomationPublication
	case "publish":
		return http.MethodPost, named + "/publish", dashboardJSONHandler(h.server.idempotent(h.server.publishAutomation))
	case "enabled":
		return http.MethodPut, named + "/enabled", h.server.setAutomationEnabled
	case "health":
		return http.MethodGet, named + "/health", h.server.getAutomationHealth
	case "runs":
		return http.MethodGet, "/v1/apps/" + url.PathEscape(slug) + "/workflows/runs", h.server.listWorkflowRuns
	case "schedule-preview":
		return http.MethodGet, "/v1/apps/" + url.PathEscape(slug) + "/workflows/schedules/" + url.PathEscape(op.Name) + "/preview", h.server.previewWorkflowSchedule
	case "run":
		return http.MethodPost, "/v1/apps/" + url.PathEscape(slug) + "/workflows/" + url.PathEscape(op.Name) + "/runs", h.server.createWorkflowRun
	default:
		if method, path, handler := h.webhookEndpoint(slug, op); handler != nil {
			return method, path, handler
		}
		return h.runEndpoint(op)
	}
}

func (h automationConsoleHost) webhookEndpoint(slug string, op automationconsole.Operation) (string, string, dashboardJSONHandler) {
	root := "/v1/apps/" + url.PathEscape(slug) + "/inbound-webhooks"
	named := root + "/" + url.PathEscape(op.EndpointID)
	switch op.Kind {
	case "endpoints":
		return http.MethodGet, root, h.server.listInboundWebhookEndpoints
	case "endpoint":
		return http.MethodGet, named, h.server.getInboundWebhookEndpoint
	case "webhook-binding":
		return http.MethodGet, named + "/automation-binding", h.server.getWebhookAutomationBinding
	case "bind-webhook":
		return http.MethodPut, named + "/automation-binding", dashboardJSONHandler(h.server.idempotent(h.server.putWebhookAutomationBinding))
	case "unbind-webhook":
		return http.MethodDelete, named + "/automation-binding", h.server.deleteWebhookAutomationBinding
	case "webhook-receipt":
		return http.MethodGet, named + "/automation-receipts/" + url.PathEscape(op.EventID), h.server.getWebhookAutomationReceipt
	default:
		return "", "", nil
	}
}

func (h automationConsoleHost) runEndpoint(op automationconsole.Operation) (string, string, dashboardJSONHandler) {
	root := "/v1/workflows/runs/" + url.PathEscape(op.RunID)
	switch op.Kind {
	case "get-run":
		return http.MethodGet, root, h.server.getWorkflowRun
	case "steps":
		return http.MethodGet, root + "/steps", h.server.listWorkflowSteps
	case "attempts":
		return http.MethodGet, root + "/steps/" + url.PathEscape(op.Step) + "/attempts", h.server.listWorkflowStepAttempts
	case "diagnostics":
		return http.MethodGet, root + "/diagnostics", h.server.getWorkflowRunDiagnostics
	case "resumes":
		return http.MethodGet, root + "/resumes", h.server.listWorkflowResumes
	case "resume":
		return http.MethodPost, root + "/resume", dashboardJSONHandler(h.server.idempotent(h.server.resumeWorkflowRun))
	case "cancel":
		return http.MethodPost, root + "/cancel", dashboardJSONHandler(h.server.idempotent(h.server.cancelWorkflowRun))
	default:
		return "", "", nil
	}
}
