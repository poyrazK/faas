// adr: 20261022170348
package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/behaviorcompare"
	"github.com/onebox-faas/faas/pkg/executionprofiles"
	"github.com/onebox-faas/faas/pkg/state"
)

// rehearsalExecutionMessage is the compared content of a captured Run start.
// Source and input are sealed at rest in production, so the capture keeps
// only their digests. It also holds what a read of the start returns.
type rehearsalExecutionMessage struct {
	WorkflowID     string                      `json:"workflow_id,omitempty"`
	StepLabel      string                      `json:"step_label,omitempty"`
	Profile        api.ExecutionProfile        `json:"profile"`
	Runtime        api.ExecutionRuntime        `json:"runtime"`
	Entrypoint     string                      `json:"entrypoint,omitempty"`
	SourceSHA256   string                      `json:"source_sha256"`
	FileCount      int                         `json:"file_count,omitempty"`
	OutputFiles    []string                    `json:"output_files,omitempty"`
	InputSHA256    string                      `json:"input_sha256"`
	Limits         api.ResolvedExecutionLimits `json:"limits"`
	Network        api.ExecutionNetworkPolicy  `json:"network"`
	IntegrationIDs []string                    `json:"integration_ids,omitempty"`
}

func executionNotFound() *api.Problem {
	return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such execution")
}

// rehearsalExecutionKey keeps a production Idempotency-Key of any length: one
// over the capture bound is stored as its digest.
func rehearsalExecutionKey(r *http.Request) string {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > state.WorkflowRunIdempotencyKeyMaxBytes {
		sum := sha256.Sum256([]byte(key))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	return key
}

// serveRehearsalExecutionStart captures POST /v1/executions after the public
// API's caller checks. Artifact inputs read production Runs and redeem
// one-time grants, so a contained run cannot use them.
func serveRehearsalExecutionStart(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
	plan := call.policy.Plan
	if !plan.ExecutionsAllowed() {
		api.WriteProblem(w, api.ErrExecutionsNotAllowed(plan))
		return
	}
	body, problem := readRehearsalPlatformBody(w, r, api.ExecutionSealedPayloadMaxBytes, call.now)
	if problem != nil {
		if problem.Status == http.StatusRequestEntityTooLarge {
			problem = api.ErrRequestBodyTooLarge(api.ExecutionSealedPayloadMaxBytes, api.ExecutionSealedPayloadMaxBytes+1)
		} else {
			problem = api.ErrValidation("invalid execution request body")
		}
		api.WriteProblem(w, problem)
		return
	}
	var request api.CreateExecutionRequest
	if decodeRehearsalStrictJSON(body, &request) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid execution request body"))
		return
	}
	message, problem := rehearsalExecutionCapture(request, plan)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindExecution, Name: string(message.Runtime), IdempotencyKey: rehearsalExecutionKey(r)}
	encoded, _ := json.Marshal(message)
	capture.Message, capture.Oversized = rehearsalPlatformMessage(map[string]json.RawMessage{"execution": encoded}, len(encoded))
	result, problem := captureRehearsalPlatformCall(r, call, capture, executionNotFound(), nil)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	// A replay answers with the first start under the key, whatever this
	// body, as apid replays its first response.
	record, problem := readCapturedExecution(r, call, result.ID)
	if problem != nil && (result.Replayed || problem.Status != http.StatusNotFound) {
		api.WriteProblem(w, problem)
		return
	}
	if problem != nil {
		// Past the capture bound a start is kept only as the incomplete marker.
		record = state.ScenarioCapturedPlatformRecord{Kind: capture.Kind, Name: capture.Name, Message: capture.Message, CreatedAt: call.now}
	}
	if result.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		record.CancelledAt = nil
	}
	writeRehearsalCaptured(w, http.StatusAccepted, capturedExecutionResponse(result.ID, record))
}

// rehearsalExecutionCapture validates a Run start as createExecution does and
// returns its compared content.
func rehearsalExecutionCapture(request api.CreateExecutionRequest, plan api.Plan) (rehearsalExecutionMessage, *api.Problem) {
	if err := api.ValidateExecutionWorkflowMetadata(request.WorkflowID, request.StepLabel); err != nil {
		return rehearsalExecutionMessage{}, api.NewProblem(http.StatusUnprocessableEntity, api.CodeExecutionPayloadInvalid, "Invalid workflow metadata", err.Error())
	}
	if len(request.ArtifactInputs) > 0 {
		return rehearsalExecutionMessage{}, api.NewProblem(http.StatusUnprocessableEntity, api.CodeExecutionPayloadInvalid, "Artifact inputs unavailable",
			"a contained rehearsal cannot stage artifacts from production Runs or redeem artifact grants").WithDocs("https://gregale.dev/docs/scenario-tests#contain-serving-vm-outbound-traffic")
	}
	resolved, problem := request.Resolve(plan)
	if problem != nil {
		return rehearsalExecutionMessage{}, problem
	}
	integrationIDs, err := api.NormalizeExecutionIntegrationIDs(request.IntegrationIDs)
	if err != nil {
		return rehearsalExecutionMessage{}, api.ErrValidation("integration_ids must contain at most 25 distinct UUIDs")
	}
	message := rehearsalExecutionMessage{WorkflowID: request.WorkflowID, StepLabel: request.StepLabel, Profile: resolved.Profile.Normalized(), Runtime: resolved.Runtime,
		Entrypoint: resolved.Entrypoint, SourceSHA256: executionSourceDigest(resolved), FileCount: len(resolved.Files), OutputFiles: resolved.OutputFiles,
		Limits: resolved.Limits, Network: resolved.Network, IntegrationIDs: integrationIDs}
	input := []byte(resolved.Input)
	if canonical, err := behaviorcompare.CanonicalJSON(input); err == nil {
		input = canonical
	}
	sum := sha256.Sum256(input)
	message.InputSHA256 = hex.EncodeToString(sum[:])
	return message, nil
}

// executionSourceDigest names a Run's code: the source text, or a files
// bundle as its path-ordered file digests.
func executionSourceDigest(resolved api.ResolvedExecutionRequest) string {
	if len(resolved.Files) == 0 {
		sum := sha256.Sum256([]byte(resolved.Source))
		return hex.EncodeToString(sum[:])
	}
	files := append([]api.ExecutionFile(nil), resolved.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	bundle := sha256.New()
	for _, file := range files {
		content := sha256.Sum256(file.Content)
		bundle.Write([]byte(file.Path + "\x00" + hex.EncodeToString(content[:]) + "\n"))
	}
	return hex.EncodeToString(bundle.Sum(nil))
}

// executionReadRoute serves GET /v1/executions/{id} for the run's own
// captured start: queued, or cancelled once the run cancelled it.
func executionReadRoute(id string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		if !call.policy.Plan.ExecutionsAllowed() {
			api.WriteProblem(w, api.ErrExecutionsNotAllowed(call.policy.Plan))
			return
		}
		record, problem := readCapturedExecution(r, call, id)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		writeRehearsalCaptured(w, http.StatusOK, capturedExecutionResponse(id, record))
	}
}

// executionCancelRoute captures DELETE /v1/executions/{id} for the run's own
// captured start. A queued Run cancels at once, as in production.
func executionCancelRoute(id string) rehearsalPlatformRoute {
	return func(w http.ResponseWriter, r *http.Request, call rehearsalPlatformCall) {
		if !call.policy.Plan.ExecutionsAllowed() {
			api.WriteProblem(w, api.ErrExecutionsNotAllowed(call.policy.Plan))
			return
		}
		capture := state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindExecutionCancel, Address: id, Name: "cancel", IdempotencyKey: rehearsalExecutionKey(r)}
		capture.Message, capture.Oversized = rehearsalPlatformMessage(map[string]json.RawMessage{}, 0)
		result, problem := captureRehearsalPlatformCall(r, call, capture, executionNotFound(), nil)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		if result.Replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		record, problem := readCapturedExecution(r, call, id)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		writeRehearsalCaptured(w, http.StatusAccepted, capturedExecutionResponse(id, record))
	}
}

func readCapturedExecution(r *http.Request, call rehearsalPlatformCall, id string) (state.ScenarioCapturedPlatformRecord, *api.Problem) {
	record, err := call.store.ScenarioCapturedPlatformCall(r.Context(), call.policy.AccountID, call.policy.RunID, id)
	switch {
	case errors.Is(err, state.ErrNotFound) || err == nil && record.Kind != api.ScenarioPlatformKindExecution:
		return record, executionNotFound()
	case err != nil:
		return record, api.ErrCapacity("rehearsal capture unavailable")
	}
	return record, nil
}

// capturedExecutionResponse projects a captured start into the public shape.
// It never runs, so it has no output, usage of zero once cancelled, and no
// runtime image.
func capturedExecutionResponse(id string, record state.ScenarioCapturedPlatformRecord) api.ExecutionResponse {
	var captured struct {
		Execution rehearsalExecutionMessage `json:"execution"`
	}
	_ = json.Unmarshal(record.Message, &captured)
	message := captured.Execution
	if message.Runtime == "" {
		message.Runtime = api.ExecutionRuntime(record.Name)
	}
	resp := api.ExecutionResponse{WorkflowID: message.WorkflowID, StepLabel: message.StepLabel, Profile: message.Profile.Normalized(),
		Packages: executionprofiles.Packages(message.Profile), ID: id, Status: api.ExecutionStatusQueued, Runtime: message.Runtime, Limits: message.Limits,
		CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339Nano)}
	if record.CancelledAt != nil {
		finished := record.CancelledAt.UTC().Format(time.RFC3339Nano)
		resp.Status, resp.FinishedAt, resp.Usage = api.ExecutionStatusCancelled, &finished, &api.ExecutionUsage{}
	}
	return resp
}
