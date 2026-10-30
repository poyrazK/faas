package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createFullProjectEnvironmentClone(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	req, problem := decodeCreateProjectEnvironmentRequest(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if req.FromEnvironment == "" || req.ShareResources {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid full clone", "from_environment is required and share_resources must be false"))
		return
	}
	// The same organization boundary as /environments: shared project access
	// never copies another owner's values, secrets or databases into a stage.
	req.Full = true
	if problem := projectaccess.ValidateEnvironmentCreation(r, req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if !s.fullProjectEnvironmentCloneAdmitted() {
		api.WriteProblem(w, s.fullProjectEnvironmentCloneProblem(r, acct, project))
		return
	}
	if req.Protected != nil && *req.Protected {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid full clone", "a full clone creates an unprotected stage; protect it once it is ready"))
		return
	}
	s.admitFullProjectEnvironmentClone(w, r, acct, project, req)
}

// Admission needs the clone machinery switched on fleet-wide and admission
// itself opened, so no gateway serves a stage before its clone publishes
// (ADR-20261024123122).
func (s *server) fullProjectEnvironmentCloneAdmitted() bool {
	return s.runtimeBool(runtimeConfigFullEnvironmentClones, false) && s.runtimeBool(runtimeConfigFullEnvironmentCloneAdmission, false)
}

// fullEnvironmentClonesOffProblem refuses turning the clone machinery off
// while admission is open or a clone is unfinished: with it off the gateway
// would serve an unpublished stage and schedd would stop recording the wakes
// and migrations a clone's retirement proofs read.
func (s *server) fullEnvironmentClonesOffProblem(ctx context.Context, key string, value json.RawMessage) *api.Problem {
	if key != runtimeConfigFullEnvironmentClones {
		return nil
	}
	enabled, err := runtimeconfig.Bool(value)
	if err != nil {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid configuration value", err.Error())
	}
	if enabled {
		return nil
	}
	if open, err := s.fullEnvironmentCloneAdmissionOpen(ctx); err != nil {
		return api.ErrCapacity("could not read the full environment clone admission switch")
	} else if open {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Full environment clone admission is open",
			"turn off "+runtimeConfigFullEnvironmentCloneAdmission+" first, then wait for unfinished clones to end")
	}
	store, ok := s.store.(state.ProjectEnvironmentCloneActivityStore)
	if !ok {
		return nil
	}
	unfinished, err := store.UnfinishedProjectEnvironmentClones(ctx)
	if err != nil {
		return api.ErrCapacity("could not count unfinished full environment clones")
	}
	if unfinished > 0 {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Full environment clones are unfinished",
			fmt.Sprintf("%d full environment clones are still running; turn this off once each is ready or compensated", unfinished))
	}
	return nil
}

// fullEnvironmentCloneAdmissionOpen reads the stored admission switch, not only
// this replica's cache, which can trail another replica's change. Admission
// share-locks that row, so once it is stored as off every clone it admitted
// has committed and the unfinished count that follows sees it.
func (s *server) fullEnvironmentCloneAdmissionOpen(ctx context.Context) (bool, error) {
	if s.runtimeBool(runtimeConfigFullEnvironmentCloneAdmission, false) {
		return true, nil
	}
	stored, err := s.store.GetRuntimeConfig(ctx, runtimeConfigFullEnvironmentCloneAdmission, state.RuntimeConfigScopeGlobal, "")
	if errors.Is(err, state.ErrRuntimeConfigNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	open, err := runtimeconfig.Bool(stored.DesiredValue)
	return err != nil || open, nil
}

// admitFullProjectEnvironmentClone captures the source and creates the
// operation in one transaction; the coordinator copies and publishes it. A
// source that uses a table without an isolated strategy, or holds managed data,
// is refused with each such table named.
func (s *server) admitFullProjectEnvironmentClone(w http.ResponseWriter, r *http.Request, acct state.Account, project state.Project, req api.CreateProjectEnvironmentRequest) {
	store, ok := s.store.(state.ProjectEnvironmentCloneConfigurationCaptureStore)
	if !ok {
		api.WriteProblem(w, fullProjectEnvironmentCloneUnavailable())
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		key = uuid.NewString()
	}
	op, err := store.CreateCapturedProjectEnvironmentCloneOperation(r.Context(), state.ProjectEnvironmentCloneCaptureRequest{AccountID: acct.ID, ProjectID: project.ID,
		SourceEnvironment: req.FromEnvironment, TargetEnvironment: req.Slug, IdempotencyKey: key, RequireCompleteInventory: true,
		AdmissionSwitches: []string{runtimeConfigFullEnvironmentClones, runtimeConfigFullEnvironmentCloneAdmission}})
	if err != nil {
		api.WriteProblem(w, fullProjectEnvironmentCloneAdmissionProblem(project.Slug, req.FromEnvironment, err))
		return
	}
	s.audit.Emit(r.Context(), "project.environment.clone_admitted", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug, "operation_id": op.ID,
		"source_environment": op.SourceEnvironment, "target_environment": op.TargetEnvironment,
	})
	// The target environment does not exist until the coordinator materializes
	// it; the response carries the durable operation to wait on.
	response := projectEnvironmentCloneOperationResponse(project.Slug, op)
	w.Header().Set("Location", "/v1/projects/"+project.Slug+"/environment-clones/"+op.ID)
	writeJSON(w, http.StatusAccepted, api.ProjectEnvironmentResponse{ProjectID: project.ID, Slug: op.TargetEnvironment, ClonedFrom: op.SourceEnvironment,
		CreatedAt: response.CreatedAt, UpdatedAt: response.UpdatedAt, CloneOperation: &response})
}

func fullProjectEnvironmentCloneAdmissionProblem(projectSlug, source string, err error) *api.Problem {
	var inventory *state.ProjectEnvironmentCloneInventoryError
	var coverage *state.ProjectEnvironmentCloneSchemaCoverageError
	switch {
	case errors.As(err, &inventory):
		problem := api.NewProblem(http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "Full environment clone unavailable",
			"the source uses resources a full clone cannot copy into an isolated stage yet")
		for _, blocker := range inventory.Blockers {
			problem.Errors = append(problem.Errors, api.FieldError{Field: "resource_coverage." + blocker.Table, Expected: "an isolated stage strategy", Got: blocker.Code})
		}
		return problem
	case errors.As(err, &coverage):
		problem := fullProjectEnvironmentCloneUnavailable()
		problem.Errors = nil
		for _, blocker := range coverage.Blockers {
			field := "resource_coverage." + blocker.Table
			if blocker.Column != "" {
				field += "." + blocker.Column
			}
			problem.Errors = append(problem.Errors, api.FieldError{Field: field, Expected: "registered schema", Got: blocker.Code})
		}
		return problem
	case errors.Is(err, state.ErrProjectEnvironmentCloneAdmissionClosed):
		return fullProjectEnvironmentCloneUnavailable()
	case errors.Is(err, state.ErrInvalidProjectEnvironmentCloneOperation):
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid full clone",
			"the Idempotency-Key must be 1 to 255 characters and the source and target environments must differ")
	case errors.Is(err, state.ErrProjectEnvironmentCloneSourceNotDeployed):
		return cloneSourceRefusal("source.workloads", "a live deployment for every workload", "not_deployed")
	case errors.Is(err, state.ErrReleaseConfigurationTransferUnsupported):
		return cloneSourceRefusal("source.release_configuration", "deployments without a release configuration snapshot", "release_pinned")
	case errors.Is(err, state.ErrProjectSecretCloneTransferUnsupported):
		return cloneSourceRefusal("source.project_secrets", "no shared environment secrets", "shared_secrets")
	case errors.Is(err, state.ErrProjectEnvironmentQueueCollectionUnavailable):
		return cloneSourceRefusal("source.queue_bindings", "environment-owned queue bindings", "legacy_stage_bindings")
	case errors.Is(err, state.ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable):
		return cloneSourceRefusal("source.work_policies", "work policies scoped to the source environment", "isolation_unavailable")
	case errors.Is(err, state.ErrNotFound):
		return projectEnvironmentNotFound(projectSlug, source)
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrInvalidArgument):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Full environment clone conflict",
			"the target environment already exists, the idempotency key names another clone, or the source changed while it was captured")
	default:
		return api.ErrCapacity("could not capture the source environment")
	}
}

// cloneSourceRefusal names what in the source keeps it from a full clone. These
// last until the customer changes the source, so they are never 503s.
func cloneSourceRefusal(field, expected, got string) *api.Problem {
	problem := api.NewProblem(http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "Full environment clone unavailable",
		"the source environment holds something a full clone cannot copy into an isolated stage yet")
	problem.Errors = []api.FieldError{{Field: field, Expected: expected, Got: got}}
	return problem
}

func (s *server) fullProjectEnvironmentCloneProblem(r *http.Request, acct state.Account, project state.Project) *api.Problem {
	problem := fullProjectEnvironmentCloneUnavailable()
	store, ok := s.store.(state.ProjectEnvironmentCloneSchemaCoverageStore)
	if !ok {
		return problem
	}
	coverage, err := store.ProjectEnvironmentCloneSchemaCoverage(r.Context(), acct.ID, project.ID)
	if err != nil {
		return api.ErrCapacity("could not verify clone configuration schema coverage")
	}
	hostingVisible, _ := s.adminAllows(acct)
	hostingVisible = hostingVisible && s.runtimeBool(runtimeConfigFrontendHosting, false)
	for _, blocker := range coverage.Blockers {
		if strings.HasPrefix(blocker.Table, "hosting_") && !hostingVisible {
			continue
		}
		field := "resource_coverage." + blocker.Table
		if blocker.Column != "" {
			field += "." + blocker.Column
		}
		problem.Errors = append(problem.Errors, api.FieldError{Field: field, Expected: "registered schema and complete isolated stage strategy", Got: blocker.Code})
	}
	return problem
}

// Admission stays closed until the worker can prove complete coverage and a
// coordinated data checkpoint. Never call the legacy live-copy path for full.
func fullProjectEnvironmentCloneUnavailable() *api.Problem {
	problem := api.NewProblem(http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "Full environment clone unavailable",
		"complete resource coverage and coordinated database/object-storage capture are not yet available")
	problem.Errors = []api.FieldError{
		{Field: "resource_coverage", Expected: "every effective resource has an isolated copy strategy", Got: "incomplete"},
		{Field: "data_checkpoint", Expected: "coordinated PostgreSQL and version-pinned object-storage capture", Got: "unavailable"},
		{Field: "managed_bindings", Expected: "verified fresh target resources and credentials", Got: "unavailable"},
		{Field: "application_policies", Expected: "isolated effective policies for every rule kind", Got: "shared"},
	}
	return problem
}

func (s *server) getProjectEnvironmentCloneOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.ProjectEnvironmentCloneOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("clone operation status is unavailable"))
		return
	}
	if _, err := uuid.Parse(r.PathValue("clone")); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Clone operation not found", "the project has no clone operation with this ID"))
		return
	}
	op, err := store.ProjectEnvironmentCloneOperationByID(r.Context(), acct.ID, project.ID, r.PathValue("clone"))
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Clone operation not found", "the project has no clone operation with this ID"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load clone operation status"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, projectEnvironmentCloneOperationResponse(project.Slug, op))
}

func projectEnvironmentCloneOperationResponse(projectSlug string, op state.ProjectEnvironmentCloneOperation) api.ProjectEnvironmentCloneOperationResponse {
	resources := make([]api.ProjectEnvironmentCloneResourceResponse, 0, len(op.Resources))
	for _, resource := range op.Resources {
		resources = append(resources, api.ProjectEnvironmentCloneResourceResponse{
			Kind: resource.Kind, Name: resource.Name, SourceID: resource.SourceID, SourceVersion: resource.SourceVersion,
			TargetID: resource.TargetID, CapturePoint: resource.CapturePoint, Status: resource.Status,
		})
	}
	return api.ProjectEnvironmentCloneOperationResponse{
		OperationID: op.ID, ProjectSlug: projectSlug, SourceEnvironment: op.SourceEnvironment, TargetEnvironment: op.TargetEnvironment,
		SourceRevisionHash: op.SourceRevisionHash, SourceReleaseSetID: op.SourceReleaseSetID, TargetReleaseSetID: op.TargetReleaseSetID,
		Status: op.Status, Revision: op.Revision, Resources: resources, ErrorCode: op.ErrorCode,
		CreatedAt: op.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: op.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}
