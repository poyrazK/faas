package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apisandbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) savedAPISandboxScenario(ctx context.Context, sourceID string) (*api.APISandboxScenario, error) {
	store, ok := s.store.(state.APISandboxScenarioStore)
	if !ok {
		return nil, errors.New("store does not support API sandbox scenarios")
	}
	scenario, err := store.GetAPISandboxScenario(ctx, sourceID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scenario, nil
}

func (s *server) getAPISandboxScenario(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	parent, sourceID, ok := s.apiSandboxParent(w, r, acct)
	if !ok {
		return
	}
	if _, ok := s.readAPISandboxSource(w, r, parent, sourceID); !ok {
		return
	}
	scenario, err := s.savedAPISandboxScenario(r.Context(), sourceID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the release scenario"))
		return
	}
	writeAPISandboxScenario(w, scenario)
}

func (s *server) putAPISandboxScenario(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	parent, sourceID, ok := s.apiSandboxParent(w, r, acct)
	if !ok || !s.allowAPISandboxProvisioning(w, r, acct, parent) {
		return
	}
	source, ok := s.readAPISandboxSource(w, r, parent, sourceID)
	if !ok || !supportedAPISandboxSource(w, parent, source) {
		return
	}
	store, ok := s.store.(state.APISandboxScenarioStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("sandbox scenarios are unavailable"))
		return
	}
	var input api.APISandboxScenarioSaveRequest
	if err := decodeJSONSized(r, &input, api.APISandboxScenarioMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("expected scenario JSON of up to 24 KiB with a name, seed, and requests"))
		return
	}
	if err := apisandbox.ValidateScenario(&input.APISandboxScenarioInput); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	var scenario api.APISandboxScenario
	var err error
	if input.SourceRequestID != "" {
		for _, request := range input.Requests {
			path, _, _ := strings.Cut(request.Path, "?")
			if strings.ContainsAny(path, "{}*") {
				api.WriteProblem(w, api.ErrValidation("replace route placeholders with synthetic values"))
				return
			}
		}
		if input.ExpectedRevision == nil {
			api.WriteProblem(w, api.ErrValidation("expected_revision is required with source_request_id; use an empty string for a release with no scenario"))
			return
		}
		revision := *input.ExpectedRevision
		if revision != "" {
			parsed, parseErr := uuid.Parse(revision)
			if parseErr != nil || parsed == uuid.Nil {
				api.WriteProblem(w, api.ErrValidation("expected_revision must be a UUID or an empty string"))
				return
			}
			revision = parsed.String()
		}
		row, problem := s.sandboxScenarioRequest(r.Context(), acct, parent, input.SourceRequestID)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if uuidFromPg(row.DeploymentID) != source.ID {
			s.notFound(w, "request telemetry not found for this API release")
			return
		}
		scenario, err = store.PutAPISandboxScenarioFromRequest(r.Context(), sourceID, input.APISandboxScenarioInput,
			state.APISandboxScenarioOrigin{RequestID: uuidFromPg(row.ID), ExpectedRevision: revision})
	} else {
		if input.ExpectedRevision != nil {
			api.WriteProblem(w, api.ErrValidation("expected_revision requires source_request_id"))
			return
		}
		scenario, err = store.PutAPISandboxScenario(r.Context(), sourceID, input.APISandboxScenarioInput)
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Scenario changed",
			"review the current release scenario and create another draft before saving"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not save the release scenario"))
		return
	}
	s.audit.Emit(r.Context(), "api_sandbox.scenario_saved", &acct.ID, map[string]any{
		"app_id": parent.ID, "source_deployment_id": sourceID, "scenario_revision": scenario.Revision,
	})
	writeAPISandboxScenario(w, &scenario)
}

func writeAPISandboxScenario(w http.ResponseWriter, scenario *api.APISandboxScenario) {
	response := api.APISandboxScenarioResponse{Scenario: scenario}
	if scenario != nil {
		response.SourceRequestID = scenario.SourceRequestID
	}
	writeJSON(w, http.StatusOK, response)
}
