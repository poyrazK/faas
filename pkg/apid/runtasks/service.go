// Package runtasks implements the account-owned named Run task catalog.
package runtasks

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/executionadmission"
	"github.com/onebox-faas/faas/pkg/apid/runcontrols"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/runtask"
	"github.com/onebox-faas/faas/pkg/state"
)

type Access struct {
	ControlWide bool
	Account     state.Account
	Principal   *string
	Wide        bool
}
type Service struct {
	Store      state.ExecutionTaskStore
	Admissions state.ExecutionAdmissionStore
	Enabled    bool
	HMACKey    []byte
	AgentFiles *runfiles.AgentFileSigner
	Validate   func(context.Context, Access, api.PublishExecutionTaskRequest) error
	Seal       func(context.Context, state.ExecutionTaskVersion, api.PublishExecutionTaskRequest) ([]byte, string, error)
	Open       func(context.Context, state.ExecutionTaskVersion) (api.PublishExecutionTaskRequest, error)
	Prepare    func(context.Context, state.ExecutionAdmissionReader, Access, api.CreateExecutionRequest) (state.CreateExecutionParams, error)
}

func Response(v state.ExecutionTaskVersion) api.ExecutionTaskVersionResponse {
	m := v.Metadata
	m.ID = v.ID
	m.Name = v.Name
	m.Version = v.Version
	m.Digest = v.Digest
	m.Status = v.Status
	m.CreatedAt = v.CreatedAt.UTC().Format(time.RFC3339Nano)
	return m
}
func (s Service) Publish(ctx context.Context, a Access, request api.PublishExecutionTaskRequest) (api.ExecutionTaskVersionResponse, bool, error) {
	if !a.Wide {
		return api.ExecutionTaskVersionResponse{}, false, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account authority required", "publishing Run tasks requires account-wide write authority")
	}
	if !s.Enabled || s.Store == nil || s.Seal == nil {
		return api.ExecutionTaskVersionResponse{}, false, api.ErrCapacity("new Run task publication is disabled")
	}
	normalized, meta, err := runtask.Normalize(request, a.Account.Plan)
	if err != nil {
		return meta, false, err
	}
	if s.Validate != nil {
		if err := s.Validate(ctx, a, normalized); err != nil {
			return meta, false, err
		}
	}
	v := state.ExecutionTaskVersion{ID: uuid.NewString(), AccountID: a.Account.ID, Name: meta.Name, Version: meta.Version, Digest: meta.Digest, EnvironmentID: meta.EnvironmentID, Runtime: meta.Runtime, Metadata: meta}
	v.Metadata.ID = v.ID
	v.SealedDefinition, v.PayloadKID, err = s.Seal(ctx, v, normalized)
	if err != nil {
		return meta, false, api.ErrCapacity("the task definition could not be sealed")
	}
	stored, replayed, err := s.Store.PublishExecutionTaskVersion(ctx, v)
	if err != nil {
		return meta, false, err
	}
	return Response(stored), replayed, nil
}
func (s Service) Get(ctx context.Context, account, name string, version int) (api.ExecutionTaskVersionResponse, error) {
	if !runtask.ValidRef(name, version) {
		return api.ExecutionTaskVersionResponse{}, api.ErrValidation("invalid task name or version")
	}
	if s.Store == nil {
		return api.ExecutionTaskVersionResponse{}, api.ErrCapacity("Run task storage is unavailable")
	}
	v, err := s.Store.ExecutionTaskVersionByName(ctx, account, name, version)
	return Response(v), err
}
func (s Service) List(ctx context.Context, account, cursor string) (api.ExecutionTaskListResponse, error) {
	out := api.ExecutionTaskListResponse{Tasks: []api.ExecutionTaskVersionResponse{}}
	name, version := "", 0
	if cursor != "" {
		var ok bool
		name, cursor, ok = strings.Cut(cursor, "@v")
		var err error
		version, err = strconv.Atoi(cursor)
		if !ok || err != nil || !runtask.ValidRef(name, version) {
			return out, api.ErrValidation("invalid task catalog cursor")
		}
	}
	if s.Store == nil {
		return out, api.ErrCapacity("Run task storage is unavailable")
	}
	rows, err := s.Store.ListExecutionTaskVersions(ctx, account, name, version, api.ExecutionTaskListMax+1)
	if err != nil {
		return out, err
	}
	more := len(rows) > api.ExecutionTaskListMax
	if more {
		rows = rows[:api.ExecutionTaskListMax]
	}
	for _, v := range rows {
		out.Tasks = append(out.Tasks, Response(v))
	}
	if more {
		last := rows[len(rows)-1]
		out.NextCursor = last.Name + "@v" + strconv.Itoa(last.Version)
	}
	return out, nil
}
func (s Service) Retire(ctx context.Context, a Access, name string, version int) error {
	if !a.Wide {
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account authority required", "retiring Run task versions requires account-wide write authority")
	}
	if !runtask.ValidRef(name, version) {
		return api.ErrValidation("invalid task name or version")
	}
	if s.Store == nil {
		return api.ErrCapacity("Run task storage is unavailable")
	}
	return s.Store.RetireExecutionTaskVersion(ctx, a.Account.ID, name, version)
}
func (s Service) Invoke(ctx context.Context, a Access, request api.InvokeExecutionTaskRequest, key string, callback *state.ExecutionTaskCallback) (state.Execution, bool, error) {
	if !runtask.ValidRef(request.Name, request.Version) || !executionadmission.ValidKey(key) {
		return state.Execution{}, false, api.ErrValidation("a valid task name, version and Idempotency-Key are required")
	}
	if len(request.Input) == 0 {
		request.Input = json.RawMessage("null")
	}
	fingerprint, err := executionadmission.TaskFingerprint(request, s.HMACKey)
	if callback != nil {
		fingerprint, err = executionadmission.TaskToolFingerprint(request, callback.ProviderCallID, s.HMACKey)
	}
	if err != nil {
		return state.Execution{}, false, api.ErrCapacity("task admission signing key is unavailable")
	}
	if s.Admissions == nil || s.Store == nil {
		return state.Execution{}, false, api.ErrCapacity("atomic Run task admission is unavailable")
	}
	scope := "POST /v1/run-tasks/invoke\n"
	if callback != nil {
		scope += "agent-tool:" + callback.EndpointID + "\n" + callback.ThreadID + "\n"
	}
	if !a.Wide {
		if a.Principal == nil {
			return state.Execution{}, false, api.ErrValidation("Run key family is unavailable")
		}
		scope += "runs-principal:" + *a.Principal + "\n"
	}
	identity := state.ExecutionAdmissionIdentity{AccountID: a.Account.ID, ScopeHash: sha256.Sum256([]byte(scope)), KeyHash: sha256.Sum256([]byte(key)), Fingerprint: fingerprint, LegacyKey: scope + key, TaskCallback: callback}
	row, replayed, err := s.Admissions.AdmitExecution(ctx, identity, func(ctx context.Context, reader state.ExecutionAdmissionReader) (state.CreateExecutionParams, error) {
		if !s.Enabled || s.Open == nil || s.Prepare == nil {
			return state.CreateExecutionParams{}, api.ErrCapacity("new Run task invocations are disabled")
		}
		versions, ok := reader.(state.ExecutionTaskVersionReader)
		if !ok {
			return state.CreateExecutionParams{}, api.ErrCapacity("transactional task definition reads are unavailable")
		}
		version, err := versions.ExecutionTaskVersionByName(ctx, a.Account.ID, request.Name, request.Version)
		if err != nil {
			return state.CreateExecutionParams{}, err
		}
		if version.Status != "active" {
			return state.CreateExecutionParams{}, state.ErrExecutionTaskUnavailable
		}
		task, err := s.Open(ctx, version)
		if err != nil {
			return state.CreateExecutionParams{}, api.ErrCapacity("task definition is unavailable on this host")
		}
		if task.Name != request.Name || task.Version != request.Version {
			return state.CreateExecutionParams{}, api.ErrCapacity("task definition identity does not match its catalog version")
		}
		if callback != nil && !slices.Contains(task.AgentEndpointIDs, callback.EndpointID) {
			return state.CreateExecutionParams{}, state.ErrExecutionTaskUnavailable
		}
		definition := task.Definition
		definition.CustomerRef = request.CustomerRef
		definition.Input = request.Input
		definition.InputFiles = request.InputFiles
		if callback != nil && len(request.InputFiles) > 0 {
			definition.InputFiles, err = s.agentInputs(ctx, reader, a.Account.ID, request, callback)
			if err != nil {
				return state.CreateExecutionParams{}, err
			}
		}
		definition.ArtifactInputs = request.ArtifactInputs
		definition.WorkflowID = request.WorkflowID
		definition.StepLabel = request.StepLabel
		params, err := s.Prepare(ctx, reader, a, definition)
		if err != nil {
			return params, err
		}
		params.Task = &state.ExecutionTaskAdmission{VersionID: version.ID, RequestDigest: fingerprint, Callback: callback}
		return params, nil
	})
	if err != nil {
		return row, replayed, err
	}
	if row.Task == nil {
		inv, err := s.Store.ExecutionTaskInvocationByID(ctx, a.Account.ID, row.ID)
		if err != nil {
			return state.Execution{}, false, err
		}
		row.Task = &inv.Ref
	}
	return row, replayed, nil
}

// Only the callback path interprets these handles. Resolve before common Run
// preparation, which seals only the staged file metadata and bytes for the guest.
func (s Service) agentInputs(ctx context.Context, reader state.ExecutionAdmissionReader, accountID string, request api.InvokeExecutionTaskRequest, callback *state.ExecutionTaskCallback) ([]api.ExecutionFileInput, error) {
	if len(request.InputFiles) > api.AgentExecutionFileGrantMaxCount {
		return nil, api.ErrValidation("too many Agent file grants")
	}
	files, ok := reader.(state.ExecutionFileAdmissionReader)
	if !ok {
		return nil, api.ErrCapacity("Agent file metadata is unavailable")
	}
	scope := runfiles.AgentFileScope{AccountID: accountID, EndpointID: callback.EndpointID, ThreadID: callback.ThreadID, ToolCallID: callback.ToolCallID,
		TaskName: request.Name, Version: request.Version}
	inputs := make([]api.ExecutionFileInput, 0, len(request.InputFiles))
	now := time.Now()
	for _, in := range request.InputFiles {
		if in.FileID != "" || in.Path != "" || in.GrantToken == "" {
			return nil, api.ErrValidation("Agent file inputs require scoped grants")
		}
		input, err := s.AgentFiles.Resolve(ctx, files, scope, in.GrantToken, now)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func Problem(err error) *api.Problem {
	var budget *state.RunBudgetError
	if errors.As(err, &budget) || errors.Is(err, state.ErrRunAttributionDenied) {
		return runcontrols.Problem(err)
	}
	var problem *api.Problem
	if errors.As(err, &problem) {
		return problem
	}
	var quota *state.ExecutionTaskQuotaError
	if errors.As(err, &quota) {
		return api.NewProblem(http.StatusTooManyRequests, "execution_task_quota", "Run task quota exceeded", quota.Error()).WithDocs("https://gregale.dev/docs/run-tasks").WithLimit(quota.Maximum, quota.Observed)
	}
	var runQuota *state.ExecutionQuotaError
	if errors.As(err, &runQuota) {
		return api.NewProblem(http.StatusTooManyRequests, api.CodePlanLimitConcur, "Run concurrency limit reached", "retry after existing Runs finish").WithLimit(int64(runQuota.Limit), int64(runQuota.Observed))
	}
	switch {
	case errors.Is(err, state.ErrExecutionsNotAllowed):
		return api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Runs unavailable", "this account plan does not allow Run execution")
	case errors.Is(err, state.ErrExecutionInvalid):
		return api.ErrValidation("invalid task invocation or callback identity")
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrExecutionTaskUnavailable):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Run task unavailable", "no accessible active task version or invocation")
	case errors.Is(err, state.ErrExecutionTaskConflict):
		return api.NewProblem(http.StatusConflict, "execution_task_version_conflict", "Immutable task version conflict", "publish changed code or permissions under a new version")
	case errors.Is(err, state.ErrExecutionAdmissionConflict):
		return api.NewProblem(http.StatusUnprocessableEntity, "idempotency_key_reused", "Idempotency key reused", "the key or agent tool-call identity is already bound to a different task invocation")
	case errors.Is(err, state.ErrExecutionAdmissionKeyChanged):
		return api.ErrCapacity("the task admission fingerprint key is unavailable")
	default:
		return api.ErrCapacity("Run task operation is unavailable")
	}
}
