package runpipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/state"
)

type Submitter struct {
	AgentFileGrants map[string][]string
	AgentFiles      *runfiles.AgentFileSigner
	FileReader      state.ExecutionFileAdmissionReader
	Now             func() time.Time
	Callback        *state.ExecutionTaskCallback
	Jobs            state.ExecutionPipelineStore
	Receipts        state.ExecutionWorkflowJobStore
	Compiler        Compiler
	Enabled         bool
	Rerun           *api.ExecutionPipelineRerun
	RerunIntent     string
	Validate        func(api.CreateManagedExecutionWorkflowRequest) error
	Seal            func([]byte) ([]byte, string, error)
}

func (s Submitter) Submit(ctx context.Context, account string, principal *string, wide, controlWide bool, request api.CreateManagedExecutionWorkflowRequest) (state.ExecutionWorkflowJob, bool, error) {
	normalized, err := Normalize(request)
	if err != nil {
		return state.ExecutionWorkflowJob{}, false, api.ErrValidation(err.Error())
	}
	// Bounded shape validation precedes all catalog reads. Task output declarations
	// are validated a second time after opening the immutable definitions.
	shape := normalized
	shape.Steps = append([]api.CreateManagedExecutionWorkflowStep(nil), normalized.Steps...)
	for i := range shape.Steps {
		shape.Steps[i].ArtifactInputs = nil
	}
	if err := api.ValidateCreateManagedExecutionWorkflow(shape); err != nil {
		return state.ExecutionWorkflowJob{}, false, api.ErrValidation(err.Error())
	}
	raw, err := json.Marshal(normalized)
	if err != nil || len(raw) > api.ExecutionWorkflowManagedPlanMaxBytes {
		return state.ExecutionWorkflowJob{}, false, api.ErrValidation("pipeline plan exceeds encoded size limit")
	}
	if s.Callback != nil {
		// Agent turns persist arguments in JSONB. Object-key reordering after a
		// lost acknowledgement must preserve the logical invocation. UseNumber
		// retains large integer inputs without a float conversion.
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return state.ExecutionWorkflowJob{}, false, api.ErrValidation("invalid pipeline JSON")
		}
		raw, err = json.Marshal(value)
		if err != nil {
			return state.ExecutionWorkflowJob{}, false, api.ErrValidation("invalid pipeline JSON")
		}
		// The callback issue time may change on a replay; the original grant stays
		// attached. Provider and logical call identities are immutable intent.
		raw = append(raw, s.Callback.Key()...)
		raw = append(raw, []byte(s.Callback.ProviderCallID)...)
	}
	if len(s.AgentFileGrants) != 0 {
		if s.Callback == nil {
			return state.ExecutionWorkflowJob{}, false, api.ErrValidation("scoped file grants require an Agent callback")
		}
		grants, err := json.Marshal(s.AgentFileGrants)
		if err != nil {
			return state.ExecutionWorkflowJob{}, false, err
		}
		// Keep the existing digest for replay of already submitted JSON calls.
		raw = append([]byte("gregale scoped pipeline inputs v1\x00"), raw...)
		raw = append(raw, 0)
		raw = append(raw, grants...)
	}
	manifestDigest := sha256.Sum256(raw)
	if s.Rerun != nil {
		raw = append(raw, []byte(s.RerunIntent)...)
	}
	hash := sha256.Sum256(raw)
	submission := hex.EncodeToString(hash[:])
	if s.Jobs == nil || s.Receipts == nil {
		return state.ExecutionWorkflowJob{}, false, api.ErrCapacity("pipeline storage unavailable")
	}
	replay := func() (state.ExecutionWorkflowJob, bool, error) {
		old, err := s.Receipts.ExecutionWorkflowJobByKey(ctx, account, normalized.WorkflowID, principal)
		if err != nil {
			return old, false, err
		}
		p, err := s.Jobs.ExecutionPipelineByID(ctx, old.ID)
		if err == nil && p.Policy.SubmissionDigest == submission {
			return old, true, nil
		}
		return state.ExecutionWorkflowJob{}, false, api.NewProblem(409, api.CodeConflict, "Workflow already submitted", "this workflow id has a different plan")
	}
	if old, err := s.Receipts.ExecutionWorkflowJobByKey(ctx, account, normalized.WorkflowID, principal); err == nil && old.ID != "" {
		return replay()
	} else if err != nil && !errors.Is(err, state.ErrNotFound) {
		return old, false, err
	}
	if !s.Enabled {
		return state.ExecutionWorkflowJob{}, false, api.ErrCapacity("new Run pipelines are disabled")
	}
	compiler := s.Compiler
	if s.Callback != nil {
		compiler.EndpointID = s.Callback.EndpointID
	}
	plan, err := compiler.Compile(ctx, request, wide, controlWide)
	if err != nil {
		return state.ExecutionWorkflowJob{}, false, err
	}
	plan.Policy.ManifestDigest = hex.EncodeToString(manifestDigest[:])
	plan.Policy.ProgressVersion = "v1"
	plan.Policy.Rerun = s.Rerun
	plan.Policy.CompletionVersion = "v1"
	plan.Policy.Callback = s.Callback
	plan.Policy.SubmissionDigest = submission
	if err := s.pinAgentFiles(ctx, account, &plan); err != nil {
		return state.ExecutionWorkflowJob{}, false, err
	}
	if s.Validate != nil {
		if err := s.Validate(plan.Request); err != nil {
			return state.ExecutionWorkflowJob{}, false, err
		}
	}
	raw, err = json.Marshal(plan)
	if err != nil || len(raw) > api.ExecutionWorkflowManagedPlanMaxBytes {
		return state.ExecutionWorkflowJob{}, false, api.ErrValidation("compiled pipeline plan exceeds encoded size limit")
	}
	digest := sha256.Sum256(raw)
	if s.Seal == nil {
		return state.ExecutionWorkflowJob{}, false, api.ErrCapacity("pipeline sealing unavailable")
	}
	sealed, kid, err := s.Seal(raw)
	if err != nil {
		return state.ExecutionWorkflowJob{}, false, api.ErrCapacity("pipeline sealing unavailable")
	}
	row, err := s.Jobs.CreateExecutionPipeline(ctx, state.CreateExecutionWorkflowJobParams{AccountID: account, RunsPrincipalID: principal, WorkflowID: normalized.WorkflowID, PlanID: hex.EncodeToString(digest[:12]), StepCount: len(plan.Request.Steps), SealedPlan: sealed, PayloadKID: kid, CreatedAt: time.Now().UTC()}, plan.Policy)
	if errors.Is(err, state.ErrExecutionWorkflowQueueFull) {
		return row, false, api.ErrCapacity("the account managed workflow queue is full")
	}
	if errors.Is(err, state.ErrExecutionWorkflowJobExists) {
		return replay()
	}
	return row, false, err
}
