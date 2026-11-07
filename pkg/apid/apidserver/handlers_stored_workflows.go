package apidserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/cronexpr"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listStoredWorkflows(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	response, err := s.storedWorkflowList(r.Context(), app, account)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
func (s *server) getStoredWorkflow(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	response, err := s.storedWorkflowList(r.Context(), app, account)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	for _, definition := range response.StoredWorkflows {
		if definition.Name == r.PathValue("name") {
			writeJSON(w, http.StatusOK, definition)
			return
		}
	}
	s.notFound(w, "no such stored workflow")
}

func (s *server) getStoredWorkflowHealth(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	name := r.PathValue("name")
	definitions, err := s.storedWorkflowList(r.Context(), app, account)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	if !storedWorkflowListed(definitions.StoredWorkflows, name) {
		s.notFound(w, "no such stored workflow")
		return
	}
	after, before, valid := storedWorkflowHealthTimeRange(r.URL.Query(), time.Now().UTC())
	if !valid {
		api.WriteProblem(w, api.ErrValidation("created_after and created_before must form a non-future window no longer than 30 days"))
		return
	}
	store, ok := s.store.(state.StoredWorkflowHealthStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("stored workflow health storage unavailable"))
		return
	}
	health, err := store.GetStoredWorkflowHealth(r.Context(), app.ID, name, after, before)
	if err != nil {
		s.log.Error("get stored workflow health failed", "app_id", app.ID, "workflow_name", name, "err", err)
		api.WriteProblem(w, api.ErrCapacity("failed to read stored workflow health"))
		return
	}
	writeJSON(w, http.StatusOK, storedWorkflowHealthResponse(app.Slug, name, after, before, health))
}

func storedWorkflowListed(definitions []api.StoredWorkflowResponse, name string) bool {
	for _, definition := range definitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func storedWorkflowHealthTimeRange(query map[string][]string, now time.Time) (time.Time, time.Time, bool) {
	after, err := parseWorkflowRunTimeFilter(query, "created_after")
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	before, err := parseWorkflowRunTimeFilter(query, "created_before")
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if before == nil {
		before = &now
	}
	if before.After(now) {
		return time.Time{}, time.Time{}, false
	}
	if after == nil {
		defaultAfter := before.Add(-api.StoredWorkflowHealthDefaultRange)
		after = &defaultAfter
	}
	if after.After(*before) || before.Sub(*after) > api.StoredWorkflowHealthMaxRange {
		return time.Time{}, time.Time{}, false
	}
	return *after, *before, true
}

func storedWorkflowHealthResponse(appSlug, name string, after, before time.Time, health state.StoredWorkflowHealth) api.StoredWorkflowHealthResponse {
	successes := health.StatusCounts[state.WorkflowRunStatusSucceeded]
	successRate := 0.0
	if health.CompletedRunCount > 0 {
		successRate = float64(successes) / float64(health.CompletedRunCount)
	}
	response := api.StoredWorkflowHealthResponse{
		AppSlug: appSlug, StoredWorkflowName: name, WindowStart: after, WindowEnd: before,
		RunCount: health.RunCount, CompletedRunCount: health.CompletedRunCount,
		ActiveRunCount: health.ActiveRunCount, QueuedRunCount: health.QueuedRunCount,
		Queue:       health.Queue,
		SuccessRate: successRate, StatusCounts: health.StatusCounts,
		P50DurationMS: health.P50DurationMS, P95DurationMS: health.P95DurationMS,
		FailedSteps: make([]api.StoredWorkflowHealthStepFailure, 0, len(health.FailedSteps)),
	}
	response.LastRun = storedWorkflowHealthRunResponse(health.LastRun)
	response.LastSuccess = storedWorkflowHealthRunResponse(health.LastSuccess)
	response.LastFailure = storedWorkflowHealthRunResponse(health.LastFailure)
	for _, failure := range health.FailedSteps {
		response.FailedSteps = append(response.FailedSteps, api.StoredWorkflowHealthStepFailure{
			StepName: failure.StepName, FailedRunCount: failure.FailedRunCount, LastFailedAt: failure.LastFailedAt,
		})
	}
	return response
}

func storedWorkflowHealthRunResponse(run *state.StoredWorkflowRunSummary) *api.StoredWorkflowHealthRun {
	if run == nil {
		return nil
	}
	return &api.StoredWorkflowHealthRun{ID: run.ID, Status: run.Status, CreatedAt: run.CreatedAt, FinishedAt: run.FinishedAt}
}

func (s *server) listStoredWorkflowRevisions(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	limit, valid := storedWorkflowRevisionQueryInt(w, r, "limit", 50, 1, 100)
	if !valid {
		return
	}
	offset, valid := storedWorkflowRevisionQueryInt(w, r, "offset", 0, 0, int(^uint32(0)>>1))
	if !valid {
		return
	}
	store, ok := s.store.(state.StoredWorkflowStore)
	if !ok {
		writeStoredWorkflowError(w, errors.New("stored workflow storage unavailable"))
		return
	}
	revisions, total, err := store.ListStoredWorkflowRevisions(r.Context(), app.ID, r.PathValue("name"), state.StoredWorkflowRevisionListOptions{Limit: limit, Offset: offset})
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	response := api.ListStoredWorkflowRevisionsResponse{Revisions: make([]api.StoredWorkflowRevisionResponse, 0, len(revisions)), Total: total, Limit: limit, Offset: offset}
	for _, revision := range revisions {
		item, err := storedWorkflowRevisionResponse(revision)
		if err != nil {
			writeStoredWorkflowError(w, err)
			return
		}
		response.Revisions = append(response.Revisions, item)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) getStoredWorkflowRevision(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	version, ok := storedWorkflowRevisionPathVersion(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.StoredWorkflowStore)
	if !ok {
		writeStoredWorkflowError(w, errors.New("stored workflow storage unavailable"))
		return
	}
	revision, err := store.GetStoredWorkflowRevision(r.Context(), app.ID, r.PathValue("name"), version)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	response, err := storedWorkflowRevisionResponse(revision)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) restoreStoredWorkflowRevision(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.RestoreStoredWorkflowRevisionRequest
	if !decodeStoredWorkflowBody(w, r, &body) {
		return
	}
	if body.ExpectedVersion < 0 {
		api.WriteProblem(w, api.ErrValidation("expected_version must be zero or positive"))
		return
	}
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	version, ok := storedWorkflowRevisionPathVersion(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.StoredWorkflowStore)
	if !ok {
		writeStoredWorkflowError(w, errors.New("stored workflow storage unavailable"))
		return
	}
	revision, err := store.GetStoredWorkflowRevision(r.Context(), app.ID, r.PathValue("name"), version)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	s.applyStoredWorkflowMutation(w, r, account, state.StoredWorkflowMutation{Action: "save", ExpectedVersion: body.ExpectedVersion, Draft: revision.Definition})
}

func storedWorkflowRevisionQueryInt(w http.ResponseWriter, r *http.Request, name string, fallback, minimum, maximum int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value < int64(minimum) || value > int64(maximum) {
		api.WriteProblem(w, api.ErrValidation(fmt.Sprintf("%s must be between %d and %d", name, minimum, maximum)))
		return 0, false
	}
	return int(value), true
}

func storedWorkflowRevisionPathVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil || version <= 0 {
		api.WriteProblem(w, api.ErrValidation("version must be a positive integer"))
		return 0, false
	}
	return version, true
}

func storedWorkflowRevisionResponse(revision state.StoredWorkflowRevision) (api.StoredWorkflowRevisionResponse, error) {
	var definition api.WorkflowSpec
	if err := json.Unmarshal(revision.Definition, &definition); err != nil {
		return api.StoredWorkflowRevisionResponse{}, err
	}
	canonical, err := json.Marshal(definition)
	if err != nil {
		return api.StoredWorkflowRevisionResponse{}, err
	}
	var evidence *api.StoredWorkflowCheckEvidence
	if len(revision.CheckEvidence) > 0 {
		if err := json.Unmarshal(revision.CheckEvidence, &evidence); err != nil {
			return api.StoredWorkflowRevisionResponse{}, err
		}
	}
	hash := sha256.Sum256(canonical)
	return api.StoredWorkflowRevisionResponse{
		CheckEvidence: evidence,
		Version:       revision.Version, Definition: definition, DefinitionHash: hex.EncodeToString(hash[:]),
		RecordedAt: revision.RecordedAt.UTC(), LegacySnapshot: revision.LegacySnapshot,
		PublishedByAccountID: revision.PublishedByAccountID, PublishedByAPIKeyID: revision.PublishedByAPIKeyID,
	}, nil
}
func (s *server) storedWorkflowList(ctx context.Context, app state.App, account state.Account) (api.ListStoredWorkflowsResponse, error) {
	response := api.ListStoredWorkflowsResponse{AppSlug: app.Slug, RuntimeEnabled: s.workflowRuntimeEnabled, UnavailableReason: workflowScheduleUnavailableReason(s.workflowRuntimeEnabled, app, account), MaxDefinitions: account.Plan.WorkflowMaxPerApp(), StoredWorkflows: []api.StoredWorkflowResponse{}}
	if response.UnavailableReason == "" && !s.capabilityEnabledFor("workflows", account, app) {
		response.UnavailableReason = "workflows_not_released"
	}
	store, ok := s.store.(state.StoredWorkflowStore)
	if !ok {
		return response, errors.New("stored workflow storage unavailable")
	}
	records, err := store.ListStoredWorkflows(ctx, app.ID)
	if err != nil {
		return response, err
	}
	dep, err := s.store.LiveDeploymentForScope(ctx, app.ID, "default")
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return response, err
	}
	if errors.Is(err, state.ErrNotFound) && response.UnavailableReason == "" {
		response.UnavailableReason = "deployment_unavailable"
	}
	response.StoredWorkflows, err = storedWorkflowResponses(dep.Workflows, records)
	if err == nil {
		if policies, ok := s.store.(state.StoredWorkflowFailurePolicyStore); ok {
			names, readErr := policies.ListStoredWorkflowFailurePauses(ctx, app.ID)
			if readErr != nil {
				return response, readErr
			}
			paused := map[string]bool{}
			for _, name := range names {
				paused[name] = true
			}
			for i := range response.StoredWorkflows {
				response.StoredWorkflows[i].FailurePaused = paused[response.StoredWorkflows[i].Name]
			}
		}
	}
	return response, err
}
func storedWorkflowResponses(manifest json.RawMessage, records []state.StoredWorkflow) ([]api.StoredWorkflowResponse, error) {
	var definitions []api.WorkflowSpec
	if len(manifest) > 0 {
		if err := json.Unmarshal(manifest, &definitions); err != nil {
			return nil, err
		}
	}
	byName := make(map[string]api.StoredWorkflowResponse)
	for _, definition := range definitions {
		byName[definition.Name] = api.StoredWorkflowResponse{Name: definition.Name, Source: "manifest", Draft: definition, Published: &definition, Enabled: definition.Trigger == nil || definition.Trigger.Enabled == nil || *definition.Trigger.Enabled}
	}
	for _, record := range records {
		item := byName[record.Name]
		item.Name = record.Name
		item.Version = record.Version
		item.UpdatedAt = &record.UpdatedAt
		if err := storedWorkflowSavedTests(record, &item); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(record.Draft, &item.Draft); err != nil {
			return nil, err
		}
		if len(record.Published) > 0 {
			var published api.WorkflowSpec
			if err := json.Unmarshal(record.Published, &published); err != nil {
				return nil, err
			}
			if published.Trigger != nil && published.Trigger.Type != "manual" {
				enabled := record.Enabled
				published.Trigger.Enabled = &enabled
			}
			item.Published = &published
			item.PublishedVersion = record.PublishedVersion
			item.Enabled = record.Enabled
			item.Source = "dashboard"
		} else if item.Source == "" {
			item.Source = "draft"
			item.Enabled = record.Enabled
		}
		byName[record.Name] = item
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]api.StoredWorkflowResponse, 0, len(names))
	for _, name := range names {
		result = append(result, byName[name])
	}
	return result, nil
}
func (s *server) saveStoredWorkflowDraft(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.SaveStoredWorkflowDraftRequest
	if !decodeStoredWorkflowBody(w, r, &body) {
		return
	}
	if api.WorkflowUsesAgentSteps(body.Definition) && !s.agentStepsAllowed(account) {
		writeStoredWorkflowError(w, fmt.Errorf("%w: %w", state.ErrStoredWorkflowInvalid, api.ErrWorkflowInvalidStepTarget))
		return
	}
	raw, err := json.Marshal(body.Definition)
	if err != nil {
		writeStoredWorkflowError(w, state.ErrStoredWorkflowInvalid)
		return
	}
	s.applyStoredWorkflowMutation(w, r, account, state.StoredWorkflowMutation{Action: "save", ExpectedVersion: body.ExpectedVersion, Draft: raw})
}
func (s *server) publishStoredWorkflow(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.PublishStoredWorkflowRequest
	if !decodeStoredWorkflowBody(w, r, &body) {
		return
	}
	s.applyStoredWorkflowMutation(w, r, account, state.StoredWorkflowMutation{Action: "publish", ExpectedVersion: body.ExpectedVersion, TakeOverManifest: body.TakeOverManifest, CheckEvidence: body.CheckEvidence, CheckReceipt: body.CheckReceipt})
}
func (s *server) setStoredWorkflowEnabled(w http.ResponseWriter, r *http.Request, account state.Account) {
	var body api.SetStoredWorkflowEnabledRequest
	if !decodeStoredWorkflowBody(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		api.WriteProblem(w, api.ErrValidation("enabled is required and must be a boolean"))
		return
	}
	s.applyStoredWorkflowMutation(w, r, account, state.StoredWorkflowMutation{Action: "enable", ExpectedVersion: body.ExpectedVersion, Enabled: *body.Enabled})
}
func (s *server) deleteStoredWorkflow(w http.ResponseWriter, r *http.Request, account state.Account) {
	version, err := strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
	if err != nil || version <= 0 {
		api.WriteProblem(w, api.ErrValidation("expected_version must be a positive integer"))
		return
	}
	restore := false
	if value := r.URL.Query().Get("restore_manifest"); value != "" {
		restore, err = strconv.ParseBool(value)
	}
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("restore_manifest must be a boolean"))
		return
	}
	s.applyStoredWorkflowMutation(w, r, account, state.StoredWorkflowMutation{Action: "delete", ExpectedVersion: version, RestoreManifest: restore})
}
func (s *server) applyStoredWorkflowMutation(w http.ResponseWriter, r *http.Request, account state.Account, mutation state.StoredWorkflowMutation) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.StoredWorkflowStore)
	if !ok {
		writeStoredWorkflowError(w, errors.New("stored workflow storage unavailable"))
		return
	}
	if mutation.Action == "publish" {
		mutation.ActorAccountID = account.ID
		if _, key, authenticated := authmw.AccountFromContext(r); authenticated && key != nil {
			mutation.ActorAPIKeyID = key.ID
		}
	}
	record, err := store.MutateStoredWorkflow(r.Context(), app.ID, r.PathValue("name"), mutation)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	s.auditStoredWorkflowMutation(r, account, app.ID, r.PathValue("name"), mutation, record)
	if mutation.Action == "delete" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Return this mutation's version, even if another writer changes it immediately.
	dep, err := s.store.LiveDeploymentForScope(r.Context(), app.ID, "default")
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		writeStoredWorkflowError(w, err)
		return
	}
	responses, err := storedWorkflowResponses(dep.Workflows, []state.StoredWorkflow{record})
	if err == nil {
		if policies, ok := s.store.(state.StoredWorkflowFailurePolicyStore); ok {
			names, readErr := policies.ListStoredWorkflowFailurePauses(r.Context(), app.ID)
			if readErr != nil {
				writeStoredWorkflowError(w, readErr)
				return
			}
			for i := range responses {
				for _, name := range names {
					if responses[i].Name == name {
						responses[i].FailurePaused = true
					}
				}
			}
		}
	}
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	for _, response := range responses {
		if response.Name == record.Name {
			writeJSON(w, http.StatusOK, response)
			return
		}
	}
}
func (s *server) validateStoredWorkflow(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.ValidateStoredWorkflowRequest
	if !decodeStoredWorkflowBody(w, r, &body) {
		return
	}
	response := api.ValidateStoredWorkflowResponse{Issues: []string{}, StepOrder: []string{}}
	order, err := s.checkStoredWorkflowDefinition(r.Context(), app.ID, body.Definition, account.Plan)
	if err == nil && api.WorkflowUsesAgentSteps(body.Definition) && !s.agentStepsAllowed(account) {
		order, err = nil, fmt.Errorf("%w: %w", state.ErrStoredWorkflowInvalid, api.ErrWorkflowInvalidStepTarget)
	}
	if err != nil && !errors.Is(err, state.ErrStoredWorkflowInvalid) {
		writeStoredWorkflowError(w, err)
		return
	}
	if err != nil {
		response.Issues = append(response.Issues, err.Error())
	} else {
		response.Valid = true
		response.StepOrder = order
	}
	if response.Valid && body.Definition.Trigger != nil && body.Definition.Trigger.Type == "schedule" {
		trigger := body.Definition.Trigger
		schedule, err := cronexpr.Parse(trigger.Schedule, trigger.Timezone)
		if err == nil {
			response.NextFireAt = schedule.Next(time.Now().UTC()).UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, response)
}
func (s *server) checkStoredWorkflowDefinition(ctx context.Context, appID string, spec api.WorkflowSpec, plan api.Plan) ([]string, error) {
	order, err := api.ValidateWorkflowDAG(spec, plan)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", state.ErrStoredWorkflowInvalid, err)
	}
	if store, ok := s.store.(state.WorkflowOutboundStore); ok {
		err = store.ValidateWorkflowOutboundBindings(ctx, appID, spec)
	}
	return order, err
}
func decodeStoredWorkflowBody(w http.ResponseWriter, r *http.Request, body any) bool {
	if err := decodeJSONSized(r, body, api.StoredWorkflowDefinitionMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.StoredWorkflowDefinitionMaxBytes, api.StoredWorkflowDefinitionMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation(err.Error()))
		}
		return false
	}
	return true
}
func writeStoredWorkflowError(w http.ResponseWriter, err error) {
	if problem := api.AsProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var quota *state.StoredWorkflowQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrStoredWorkflowDefinitionsQuota(quota.Plan, quota.Limit, quota.Observed))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such stored workflow"))
	case errors.Is(err, state.ErrStoredWorkflowPublishCheckRequired):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "stored_workflow_publish_check_required", "Publishing checks required", err.Error()))
	case errors.Is(err, state.ErrStoredWorkflowVersionConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeStoredWorkflowVersionConflict, "Stored workflow changed", err.Error()))
	case errors.Is(err, state.ErrStoredWorkflowRevisionNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such stored workflow revision"))
	case errors.Is(err, state.ErrStoredWorkflowOwnershipConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeStoredWorkflowOwnershipConflict, "Confirm YAML ownership change", err.Error()))
	case errors.Is(err, state.ErrStoredWorkflowInvalid):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeStoredWorkflowInvalid, "Invalid stored workflow", err.Error()))
	case errors.Is(err, state.ErrWorkflowEventTargetUnavailable):
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
	default:
		api.WriteProblem(w, api.ErrCapacity("failed to read or update stored workflow"))
	}
}

// auditStoredWorkflowMutation records a change to what a stored workflow will run on
// its own: publishing or enabling it starts unattended calls into the app.
// Draft saves (including revision restores), publishes, enable/disable and
// deletes wrote no audit event (production-us hunt #9).
func (s *server) auditStoredWorkflowMutation(r *http.Request, account state.Account, appID, name string, mutation state.StoredWorkflowMutation, record state.StoredWorkflow) {
	kind := "app.stored_workflow_" + mutation.Action + "d"
	switch mutation.Action {
	case "tests":
		kind = "app.stored_workflow_tests_saved"
	case "save":
		kind = "app.stored_workflow_saved"
	case "publish":
		kind = "app.stored_workflow_published"
	case "enable":
		kind = "app.stored_workflow_disabled"
		if mutation.Enabled {
			kind = "app.stored_workflow_enabled"
		}
	case "delete":
		kind = "app.stored_workflow_deleted"
	}
	s.audit.Emit(r.Context(), kind, &account.ID, map[string]any{
		"app_id": appID, "stored_workflow": name, "version": record.Version, "published_version": record.PublishedVersion,
	})
}
