package apidserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *server) getDebugSandboxScenarioDraft(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	parent, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if parent.PreviewOfSlug != "" {
		s.notFound(w, "no such API release")
		return
	}
	if !s.allowAPISandboxProvisioning(w, r, acct, parent) {
		return
	}
	row, problem := s.sandboxScenarioRequest(r.Context(), acct, parent, r.PathValue("req_id"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	source, ok := s.readAPISandboxSource(w, r, parent, uuidFromPg(row.DeploymentID))
	if !ok || !supportedAPISandboxSource(w, parent, source) {
		return
	}
	current, err := s.savedAPISandboxScenario(r.Context(), source.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the release scenario"))
		return
	}
	draft := api.APISandboxScenarioDraft{
		DeploymentID: source.ID, SourceRequestID: uuidFromPg(row.ID), Method: row.Method,
		RouteTemplate: s.sandboxScenarioRouteTemplate(r.Context(), parent, source.ID, row.Method, row.Route),
	}
	if current != nil {
		draft.ExpectedRevision = current.Revision
	}
	writeJSON(w, http.StatusOK, draft)
}

func (s *server) sandboxScenarioRequest(ctx context.Context, acct state.Account, parent state.App, requestID string) (sqlc.GetRequestTelemetryByAppAndIdentifierRow, *api.Problem) {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		return sqlc.GetRequestTelemetryByAppAndIdentifierRow{}, api.ErrPlanFeatureGated("debugger", acct.Plan)
	}
	identifier, err := normalizeDebugRequestIdentifier(requestID)
	if err != nil {
		return sqlc.GetRequestTelemetryByAppAndIdentifierRow{}, api.ErrValidation(err.Error())
	}
	now := time.Now().UTC()
	retention := time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
	row, err := s.getDebugRequestTelemetry(ctx, acct.ID, parent.ID, identifier, now.Add(-retention), now.Add(debugRequestJournalSkew), now)
	if errors.Is(err, pgx.ErrNoRows) {
		return row, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "retained request telemetry not found")
	}
	if err != nil {
		return row, api.ErrCapacity("could not read the request telemetry")
	}
	if !row.ID.Valid || !row.DeploymentID.Valid || row.Count != 1 {
		return row, api.NewProblem(http.StatusConflict, api.CodeConflict, "Request has no individual release evidence",
			"open an individual retained request with its serving release")
	}
	switch row.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return row, nil
	default:
		return row, api.NewProblem(http.StatusConflict, api.CodeConflict, "Request method is unavailable for scenarios",
			"API sandbox scenarios support GET, HEAD, POST, PUT, PATCH and DELETE")
	}
}

// A gateway route label can also be inferred from a concrete request path.
// Only copy a method and path pair confirmed by stored route declarations.
func (s *server) sandboxScenarioRouteTemplate(ctx context.Context, app state.App, deploymentID, method, label string) string {
	path, ok := strings.CutPrefix(label, method+" ")
	if !ok || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
		return ""
	}
	for _, route := range app.DeclaredRoutes {
		if route.Path == path && (slices.Contains(route.Methods, method) ||
			(method == http.MethodHead && slices.Contains(route.Methods, http.MethodGet))) {
			return route.Path
		}
	}
	doc, _, err := s.store.GetDeploymentOpenAPIDoc(ctx, deploymentID, app.AccountID)
	if err == nil && sandboxScenarioOpenAPIPath(doc, path, method) {
		return path
	}
	doc, _, err = s.store.GetAppOpenAPIDoc(ctx, app.ID, app.AccountID)
	if err == nil && sandboxScenarioOpenAPIPath(doc, path, method) {
		return path
	}
	return ""
}

func sandboxScenarioOpenAPIPath(doc []byte, path, method string) bool {
	spec, err := openapidiff.LoadBytes(doc)
	if err != nil {
		return false
	}
	item := spec.Paths[path]
	return item != nil && (item.Methods[strings.ToLower(method)] != nil ||
		(method == http.MethodHead && item.Methods["get"] != nil))
}
