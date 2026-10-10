package sched

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnvironmentQualificationHTTPProbeResult contains only the HTTP status and
// whether the response stream completed. Response bytes and headers are never
// retained; health endpoints can accidentally return application data.
type EnvironmentQualificationHTTPProbeResult struct {
	StatusCode int
	Completed  bool
}

// EnvironmentQualificationHTTPProbeVMM routes one fixed, policy-derived GET
// to a qualification target on its owning compute node. It is separate from
// ordinary serving callbacks so a probe cannot borrow a gateway route.
type EnvironmentQualificationHTTPProbeVMM interface {
	ProbeEnvironmentQualificationHTTP(context.Context, state.EnvironmentQualificationExecution,
		state.EnvironmentWorkloadQualificationRequest, state.Instance) (EnvironmentQualificationHTTPProbeResult, error)
}

type EnvironmentQualificationQueueProbeResult struct {
	StatusCode int
	Completed  bool
	Failed     bool
}

func qualificationQueueSmokeMessageID(request state.EnvironmentWorkloadQualificationRequest, bindingID string) string {
	identity := fmt.Sprintf("%s\x00%d\x00%s", request.ID, request.Attempt, bindingID)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String()
}

// EnvironmentQualificationQueueProbeVMM delivers one reviewed
// synthetic queue message to the exact private candidate instance. The
// adapter must not write invocations, trigger records, or customer queue rows.
type EnvironmentQualificationQueueProbeVMM interface {
	ProbeEnvironmentQualificationQueue(context.Context, state.EnvironmentQualificationExecution,
		state.EnvironmentWorkloadQualificationRequest, state.Instance, string) (EnvironmentQualificationQueueProbeResult, error)
}

// ProbeEnvironmentQualificationSourceGraph runs the reviewed HTTP health
// policy against every live source member before capture. It is a gate on the
// caller's source visitor only: the source result is not retained as
// qualification evidence and does not create a smoke receipt.
func (e *Engine) ProbeEnvironmentQualificationSourceGraph(ctx context.Context, instances map[string]state.Instance) error {
	_, err := e.probeEnvironmentQualificationGraph(ctx, instances, false)
	return err
}

// ProbeEnvironmentQualificationGraph executes the frozen HTTP health policy
// for every restored member in the current graph. The result is sanitized and
// attempt-bound; it creates no receipt until the dispatcher has also retired
// the complete target cohort.
func (e *Engine) ProbeEnvironmentQualificationGraph(ctx context.Context, instances map[string]state.Instance) ([]state.EnvironmentQualificationSmokeEvidence, error) {
	return e.probeEnvironmentQualificationGraph(ctx, instances, true)
}

func (e *Engine) probeEnvironmentQualificationGraph(ctx context.Context, instances map[string]state.Instance, restored bool) ([]state.EnvironmentQualificationSmokeEvidence, error) {
	requests, ok := ctx.Value(qualificationGraphRequestsContextKey{}).([]state.EnvironmentWorkloadQualificationRequest)
	graphID, graphOK := ctx.Value(qualificationGraphContextKey{}).(string)
	nodeID, nodeOK := ctx.Value(qualificationGraphDispatchNodeContextKey{}).(string)
	qualifier, qualifierOK := e.store.(state.EnvironmentGitOpsQualificationStore)
	executions, executionOK := e.store.(state.EnvironmentQualificationExecutionStore)
	probe, probeOK := e.vmm.(EnvironmentQualificationHTTPProbeVMM)
	queueProbe, queueProbeOK := e.vmm.(EnvironmentQualificationQueueProbeVMM)
	if !ok || !graphOK || graphID == "" || !nodeOK || nodeID == "" || !qualifierOK || !executionOK ||
		len(requests) == 0 || len(instances) != len(requests) {
		return nil, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	ordered := slices.Clone(requests)
	slices.SortFunc(ordered, func(a, b state.EnvironmentWorkloadQualificationRequest) int {
		return strings.Compare(a.Resource, b.Resource)
	})
	seenResources := make(map[string]bool, len(ordered))
	seenInstances := make(map[string]bool, len(ordered))
	for _, request := range ordered {
		if request.GraphID != graphID || seenResources[request.Resource] {
			return nil, state.ErrConflict
		}
		seenResources[request.Resource] = true
		instance, exists := instances[request.Resource]
		if !exists || instance.ID == "" || seenInstances[instance.ID] || instance.NodeID != nodeID ||
			instance.State != string(state.StateRunning) || instance.WakeID == "" || instance.AppID != request.AppID ||
			instance.DeploymentID != request.DeploymentID || instance.HostIP == "" || instance.Netns == "" {
			return nil, fmt.Errorf("restored qualification member %s has no distinct running runtime: %w", request.Resource, state.ErrConflict)
		}
		seenInstances[instance.ID] = true
	}

	evidence := make([]state.EnvironmentQualificationSmokeEvidence, 0, len(ordered))
	for _, request := range ordered {
		validateOwner := e.validateQualificationOwner
		if restored {
			validateOwner = e.validateQualificationRestoreOwner
		}
		if err := validateOwner(ctx, qualifier, request); err != nil {
			return nil, err
		}
		instance := instances[request.Resource]
		app, err := e.store.AppByID(ctx, request.AppID)
		if err != nil {
			return nil, err
		}
		if app.AppProtocol != "" && app.AppProtocol != api.AppProtocolHTTP1 {
			return nil, state.ErrEnvironmentWorkloadPreparationUnavailable
		}
		policy, policySHA256, err := state.EnvironmentQualificationSmokePolicyFor(request)
		if err != nil {
			return nil, err
		}
		status, err := executions.EnvironmentQualificationExecution(ctx, instance.ID)
		if err != nil {
			return nil, err
		}
		phase := "source"
		executionMatches := qualificationSmokeSourceExecutionMatches(request, instance, status)
		if restored {
			phase = "restored"
			executionMatches = qualificationSmokeExecutionMatches(request, instance, status)
		}
		if !executionMatches {
			return nil, fmt.Errorf("%s qualification execution changed for %s: %w", phase, request.Resource, state.ErrConflict)
		}
		var resultSHA256 string
		if len(policy.QueueMessages) != 0 {
			queueInstanceMode := string(state.InstanceModeWorker)
			if request.ExecutionMode == api.ExecutionModeRequest && request.FrozenInputs.AppType == state.AppTypeFunction {
				queueInstanceMode = string(state.InstanceModeNormal)
			}
			if !queueProbeOK || instance.Mode != queueInstanceMode {
				return nil, state.ErrEnvironmentWorkloadPreparationUnavailable
			}
			results := make([]EnvironmentQualificationQueueProbeResult, 0, len(policy.QueueMessages))
			for _, message := range policy.QueueMessages {
				queueResult, err := queueProbe.ProbeEnvironmentQualificationQueue(ctx, status.Execution, request, instance, message.BindingName)
				if err != nil {
					return nil, fmt.Errorf("%s qualification queue probe for %s/%s: %w", phase, request.Resource, message.BindingName, err)
				}
				if !queueResult.Completed || queueResult.StatusCode < 200 || queueResult.StatusCode > 299 || queueResult.Failed {
					return nil, fmt.Errorf("%s qualification queue probe for %s/%s was not acknowledged: %w", phase, request.Resource, message.BindingName, state.ErrConflict)
				}
				results = append(results, queueResult)
			}
			resultSHA256, err = qualificationQueueSmokeResultSHA256(policySHA256, policy.QueueMessages, results)
			if err != nil {
				return nil, err
			}
		} else {
			if !probeOK {
				return nil, state.ErrEnvironmentWorkloadPreparationUnavailable
			}
			result, err := probe.ProbeEnvironmentQualificationHTTP(ctx, status.Execution, request, instance)
			if err != nil {
				return nil, fmt.Errorf("%s qualification HTTP probe for %s: %w", phase, request.Resource, err)
			}
			if !result.Completed || result.StatusCode < policy.StatusMin || result.StatusCode > policy.StatusMax {
				return nil, fmt.Errorf("%s qualification HTTP probe for %s returned no complete 2xx response: %w", phase, request.Resource, state.ErrConflict)
			}
			resultSHA256, err = qualificationSmokeResultSHA256(policySHA256, result)
			if err != nil {
				return nil, err
			}
		}
		report := state.EnvironmentQualificationSmokeEvidence{Resource: request.Resource, InstanceID: instance.ID,
			PolicyID: policy.ID, PolicySHA256: policySHA256, ResultSHA256: resultSHA256, Passed: true}
		if err := report.ValidateFor(request, instance.ID); err != nil {
			return nil, err
		}
		if err := validateOwner(ctx, qualifier, request); err != nil {
			return nil, err
		}
		if !restored {
			continue
		}
		evidence = append(evidence, report)
	}
	return evidence, nil
}

func qualificationSmokeExecutionMatches(request state.EnvironmentWorkloadQualificationRequest, instance state.Instance,
	status state.EnvironmentQualificationExecutionStatus) bool {
	return instance.ID != request.ReservedInstanceID && status.CaptureInstanceID == request.ReservedInstanceID &&
		status.Execution.CaptureInstanceID == request.ReservedInstanceID && qualificationSmokeExecutionIdentityMatches(request, instance, status)
}

func qualificationSmokeSourceExecutionMatches(request state.EnvironmentWorkloadQualificationRequest, instance state.Instance,
	status state.EnvironmentQualificationExecutionStatus) bool {
	return instance.ID == request.ReservedInstanceID && status.CaptureInstanceID == "" && status.Execution.CaptureInstanceID == "" &&
		qualificationSmokeExecutionIdentityMatches(request, instance, status)
}

func qualificationSmokeExecutionIdentityMatches(request state.EnvironmentWorkloadQualificationRequest, instance state.Instance,
	status state.EnvironmentQualificationExecutionStatus) bool {
	frame := status.Execution
	return status.DispatchStarted && status.RetiredAt == nil && status.Retirement == nil &&
		frame.InstanceID == instance.ID && frame.RequestID == request.ID &&
		frame.GraphID == request.GraphID && frame.AppID == request.AppID && frame.DeploymentID == request.DeploymentID &&
		frame.NodeID == instance.NodeID && frame.WakeID == instance.WakeID && frame.SourceID == request.FrozenInputs.SourceID &&
		frame.EnvironmentID == request.FrozenInputs.EnvironmentID && frame.RevisionID == request.FrozenInputs.RevisionID &&
		frame.Resource == request.Resource && frame.Scope == request.FrozenInputs.Scope && frame.PlanHash == request.FrozenInputs.PlanHash &&
		frame.Generation == request.FrozenInputs.Generation && frame.IntentVersion == request.FrozenInputs.IntentVersion &&
		frame.Attempt == request.Attempt && frame.RAMMB == instance.RAMMB && frame.Artifact == request.Artifact &&
		instance.AppID == frame.AppID && instance.DeploymentID == frame.DeploymentID && instance.NodeID == frame.NodeID && instance.WakeID == frame.WakeID
}

func qualificationSmokeResultSHA256(policySHA256 string, result EnvironmentQualificationHTTPProbeResult) (string, error) {
	if !lowerQualificationDigest(policySHA256) || !result.Completed || result.StatusCode < 100 || result.StatusCode > 599 {
		return "", state.ErrInvalidArgument
	}
	body, err := json.Marshal(struct {
		Version        int    `json:"version"`
		PolicySHA256   string `json:"policy_sha256"`
		StatusCode     int    `json:"status_code"`
		StreamComplete bool   `json:"stream_complete"`
	}{Version: 1, PolicySHA256: policySHA256, StatusCode: result.StatusCode, StreamComplete: result.Completed})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func qualificationQueueSmokeResultSHA256(policySHA256 string, messages []state.EnvironmentQualificationQueueSmokeMessage,
	results []EnvironmentQualificationQueueProbeResult) (string, error) {
	if !lowerQualificationDigest(policySHA256) || len(messages) == 0 || len(messages) != len(results) {
		return "", state.ErrInvalidArgument
	}
	type resultEntry struct {
		BindingName string `json:"binding_name"`
		StatusCode  int    `json:"status_code"`
		Completed   bool   `json:"completed"`
		Failed      bool   `json:"failed"`
	}
	entries := make([]resultEntry, 0, len(results))
	for index, result := range results {
		if messages[index].BindingName == "" || !result.Completed || result.StatusCode < 100 || result.StatusCode > 599 {
			return "", state.ErrInvalidArgument
		}
		entries = append(entries, resultEntry{BindingName: messages[index].BindingName, StatusCode: result.StatusCode,
			Completed: result.Completed, Failed: result.Failed})
	}
	body, err := json.Marshal(struct {
		Version      int           `json:"version"`
		PolicySHA256 string        `json:"policy_sha256"`
		Results      []resultEntry `json:"results"`
	}{Version: 1, PolicySHA256: policySHA256, Results: entries})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func lowerQualificationDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
