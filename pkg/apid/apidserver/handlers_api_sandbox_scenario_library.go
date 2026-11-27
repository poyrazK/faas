package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apisandbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// selectedAPISandboxScenario accepts the current scenario saved on this
// release or a current revision from this app's library. Run reservations
// keep their own copy of the definition.
func (s *server) selectedAPISandboxScenario(ctx context.Context, appID, releaseID, revision string) (*api.APISandboxScenario, error) {
	local, err := s.savedAPISandboxScenario(ctx, releaseID)
	if err != nil || (local != nil && local.Revision == revision) {
		return local, err
	}
	library, ok := s.store.(state.APISandboxScenarioLibraryStore)
	if !ok || !canonicalSandboxComparisonID(revision) {
		return nil, nil
	}
	scenario, err := library.GetAPISandboxLibraryScenarioByRevision(ctx, appID, revision)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	return &scenario, err
}

func (s *server) sandboxScenarioLibraryApp(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.APISandboxScenarioLibraryStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	if app.PreviewOfSlug != "" || !apiSandboxHTTPSourceApp(app) {
		s.notFound(w, "no such API sandbox scenario library")
		return app, nil, false
	}
	store, ok := s.store.(state.APISandboxScenarioLibraryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sandbox scenario library is unavailable"))
	}
	return app, store, ok
}

func (s *server) listAPISandboxLibraryScenarios(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.sandboxScenarioLibraryApp(w, r, acct)
	if !ok {
		return
	}
	scenarios, err := store.ListAPISandboxLibraryScenarios(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the scenario library"))
		return
	}
	writeJSON(w, http.StatusOK, api.APISandboxScenarioLibraryResponse{Scenarios: scenarios})
}

func (s *server) getAPISandboxLibraryScenario(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.sandboxScenarioLibraryApp(w, r, acct)
	if !ok {
		return
	}
	id := apiSandboxPathID(r, "scenario")
	if id == "" {
		s.notFound(w, "no such library scenario")
		return
	}
	scenario, err := store.GetAPISandboxLibraryScenario(r.Context(), app.ID, id)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such library scenario")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the library scenario"))
		return
	}
	writeJSON(w, http.StatusOK, scenario)
}

func (s *server) putAPISandboxLibraryScenario(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.sandboxScenarioLibraryApp(w, r, acct)
	if !ok || !s.allowAPISandboxProvisioning(w, r, acct, app) {
		return
	}
	id := apiSandboxPathID(r, "scenario")
	var input api.APISandboxScenarioSaveRequest
	if id == "" || decodeJSONSized(r, &input, api.APISandboxScenarioMaxBytes) != nil ||
		input.ExpectedRevision == nil || (*input.ExpectedRevision != "" && !canonicalSandboxComparisonID(*input.ExpectedRevision)) {
		api.WriteProblem(w, api.ErrValidation("supply a scenario UUID and expected_revision, using an empty string for a new scenario"))
		return
	}
	if err := apisandbox.ValidateScenario(&input.APISandboxScenarioInput); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	for _, request := range input.Requests {
		path, _, _ := strings.Cut(request.Path, "?")
		if strings.ContainsAny(path, "{}*") {
			api.WriteProblem(w, api.ErrValidation("replace route placeholders with synthetic values"))
			return
		}
	}
	origin := state.APISandboxScenarioOrigin{ExpectedRevision: *input.ExpectedRevision}
	if input.SourceRequestID != "" {
		origin.RequestID, ok = s.sandboxLibraryRequestOrigin(w, r, acct, app, input.SourceRequestID, origin.ExpectedRevision)
		if !ok {
			return
		}
	}
	saved, err := store.PutAPISandboxLibraryScenario(r.Context(), app.ID, id, input.APISandboxScenarioInput, origin)
	if err != nil {
		writeSandboxLibrarySaveProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "api_sandbox.library_scenario_saved", &acct.ID, map[string]any{
		"app_id": app.ID, "scenario_id": saved.ID, "scenario_revision": saved.Scenario.Revision,
	})
	writeJSON(w, http.StatusOK, saved)
}

func (s *server) sandboxLibraryRequestOrigin(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, requestID, revision string) (string, bool) {
	if revision != "" {
		api.WriteProblem(w, api.ErrValidation("use source_request_id when creating a scenario"))
		return "", false
	}
	row, problem := s.sandboxScenarioRequest(r.Context(), acct, app, requestID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return "", false
	}
	source, ok := s.readAPISandboxSource(w, r, app, uuidFromPg(row.DeploymentID))
	if !ok || !supportedAPISandboxSource(w, app, source) {
		return "", false
	}
	return uuidFromPg(row.ID), true
}

func writeSandboxLibrarySaveProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrAPISandboxScenarioNameTaken):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Scenario name in use", "choose another name for this app's scenario"))
	case errors.Is(err, state.ErrAPISandboxScenarioLibraryFull):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Scenario library is full",
			fmt.Sprintf("this app can save up to %d library scenarios", api.APISandboxScenarioLibraryMaxItems)))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Library scenario changed", "reload the library scenario before making changes"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not update the scenario library"))
	}
}

func (s *server) deleteAPISandboxLibraryScenario(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.sandboxScenarioLibraryApp(w, r, acct)
	if !ok || !s.allowAPISandboxProvisioning(w, r, acct, app) {
		return
	}
	id, revision := apiSandboxPathID(r, "scenario"), r.URL.Query().Get("expected_revision")
	if id == "" || !canonicalSandboxComparisonID(revision) {
		api.WriteProblem(w, api.ErrValidation("supply a scenario UUID and the current expected_revision"))
		return
	}
	if err := store.DeleteAPISandboxLibraryScenario(r.Context(), app.ID, id, revision); err != nil {
		writeSandboxLibrarySaveProblem(w, err)
		return
	}
	s.audit.Emit(r.Context(), "api_sandbox.library_scenario_deleted", &acct.ID, map[string]any{
		"app_id": app.ID, "scenario_id": id, "scenario_revision": revision,
	})
	w.WriteHeader(http.StatusNoContent)
}
