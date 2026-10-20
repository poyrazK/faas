package operation

import (
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ListOperationDefinitions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.OperationStore(w)
	if !ok {
		return
	}
	dep, ok := s.operationDefinitionDeployment(w, r, acct)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	definitions, err := store.OperationDefinitionsForDeployment(r.Context(), acct.ID, dep.AppID, dep.ID)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	page := api.OperationDefinitionsResponse{Definitions: []api.OperationDefinitionSummary{}}
	for _, definition := range definitions {
		d, spec := definition.OperationDefinitionResponse, definition.Spec
		page.Definitions = append(page.Definitions, api.OperationDefinitionSummary{
			ID: d.ID, AppID: d.AppID, Scope: d.Scope, Revision: d.Revision, DeploymentID: d.DeploymentID, ReleaseID: d.ReleaseID,
			Name: spec.Name, Job: spec.Job, Workflow: spec.Workflow, TransactionReceipt: spec.TransactionReceipt,
			Method: spec.Method, Path: spec.Path, Owner: spec.Owner, ProgressStages: spec.ProgressStages,
			CompletionWebhookID: spec.CompletionWebhookID, Recovery: spec.Recovery, CreatedAt: d.CreatedAt,
			Milestones: operations.MilestoneNames(spec.Milestones), WorkflowSteps: spec.WorkflowSteps, Subject: spec.Subject,
			HTTPTransactionVersion: spec.HTTPTransactionVersion,
		})
	}
	sort.Slice(page.Definitions, func(i, j int) bool { return page.Definitions[i].Name < page.Definitions[j].Name })
	httpkit.WriteJSON(w, http.StatusOK, page)
}

func (s *Service) GetOperationDefinition(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.OperationStore(w)
	if !ok {
		return
	}
	dep, ok := s.operationDefinitionDeployment(w, r, acct)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	definition, err := store.OperationDefinitionForDeployment(r.Context(), acct.ID, dep.AppID, dep.ID, r.PathValue("name"))
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, definition.OperationDefinitionResponse)
}

func (s *Service) GetPlatformTenantSelfOperationIdentity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		WriteOperationError(w, state.ErrInvalidArgument)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.OperationTenantIdentity{AccountID: acct.ID, PlatformTenantID: tenant})
}
