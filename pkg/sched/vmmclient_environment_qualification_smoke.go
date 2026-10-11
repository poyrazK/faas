package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

var _ EnvironmentQualificationHTTPProbeVMM = (*VMMClient)(nil)
var _ EnvironmentQualificationHTTPProbeVMM = (*VMMRouter)(nil)
var _ EnvironmentQualificationQueueProbeVMM = (*VMMClient)(nil)
var _ EnvironmentQualificationQueueProbeVMM = (*VMMRouter)(nil)

const (
	qualificationSmokeResponseMaxBytes      = 1 << 20
	qualificationSmokeResponseMaxBodyChunks = 256
)

// ProbeEnvironmentQualificationHTTP sends only the frozen, policy-derived GET
// to the exact restored target. It drains response frames without retaining
// headers or body content and refuses incomplete or non-2xx responses.
func (c *VMMClient) ProbeEnvironmentQualificationHTTP(ctx context.Context, frame state.EnvironmentQualificationExecution,
	request state.EnvironmentWorkloadQualificationRequest, instance state.Instance) (EnvironmentQualificationHTTPProbeResult, error) {
	var result EnvironmentQualificationHTTPProbeResult
	if c == nil || c.cli == nil || !qualificationSmokeProbeFrameMatches(frame, request, instance) {
		return result, state.ErrConflict
	}
	policy, _, err := state.EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		return result, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.TimeoutMillis)*time.Millisecond)
	defer cancel()
	probeCtx = wire.WithCorrelationOutgoing(probeCtx, wire.CorrelationFields{RequestID: frame.RequestID, WakeID: frame.WakeID,
		AppID: frame.AppID, DeploymentID: frame.DeploymentID, InstanceID: frame.InstanceID, NodeID: frame.NodeID})
	stream, err := c.cli.ForwardHTTPStream(probeCtx)
	if err != nil {
		return result, fmt.Errorf("open private qualification HTTP probe: %w", err)
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{
		Instance: frame.InstanceID, Method: policy.Method, RequestUri: policy.Path, Port: uint32(policy.Port),
		AppProtocol: api.AppProtocolHTTP1, ContentLengthKnown: true,
	}}}); err != nil {
		return result, fmt.Errorf("send private qualification HTTP probe: %w", err)
	}
	if err := stream.CloseSend(); err != nil {
		return result, fmt.Errorf("finish private qualification HTTP request: %w", err)
	}
	var responseBytes, bodyChunks int
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, fmt.Errorf("read private qualification HTTP response: %w", err)
		}
		switch frame := response.GetFrame().(type) {
		case *vmmdpb.ForwardHTTPStreamResponse_Init:
			if result.StatusCode != 0 || frame.Init == nil || frame.Init.Error != "" || frame.Init.Status < 100 || frame.Init.Status > 599 {
				return result, fmt.Errorf("private qualification HTTP response was malformed or unavailable: %w", state.ErrConflict)
			}
			result.StatusCode = int(frame.Init.Status)
		case *vmmdpb.ForwardHTTPStreamResponse_BodyChunk:
			if result.StatusCode == 0 {
				return result, fmt.Errorf("private qualification HTTP body preceded response headers: %w", state.ErrConflict)
			}
			bodyChunks++
			if bodyChunks > qualificationSmokeResponseMaxBodyChunks || len(frame.BodyChunk) > qualificationSmokeResponseMaxBytes-responseBytes {
				return result, fmt.Errorf("private qualification HTTP response exceeded smoke limits: %w", state.ErrConflict)
			}
			responseBytes += len(frame.BodyChunk)
			// Body bytes are deliberately discarded. The caller only needs
			// to know the response completed; app content must not enter proof.
			// Both byte and frame limits bound hostile or accidental streaming
			// responses even while the policy timeout is still active.
		default:
			return result, fmt.Errorf("private qualification HTTP response frame is unsupported: %w", state.ErrConflict)
		}
	}
	if result.StatusCode == 0 {
		return result, fmt.Errorf("private qualification HTTP response was empty: %w", state.ErrConflict)
	}
	if result.StatusCode < policy.StatusMin || result.StatusCode > policy.StatusMax {
		return result, fmt.Errorf("private qualification health endpoint returned HTTP %d: %w", result.StatusCode, state.ErrConflict)
	}
	result.Completed = true
	return result, nil
}

// ProbeEnvironmentQualificationQueue sends a frozen test message to the
// exact candidate instance using the same trigger route as normal push
// delivery. The response body is bounded, parsed only for this item ID, and
// never returned or retained.
func (c *VMMClient) ProbeEnvironmentQualificationQueue(ctx context.Context, frame state.EnvironmentQualificationExecution,
	request state.EnvironmentWorkloadQualificationRequest, instance state.Instance, bindingName string) (EnvironmentQualificationQueueProbeResult, error) {
	var result EnvironmentQualificationQueueProbeResult
	workerMode := request.ExecutionMode == api.ExecutionModeWorker && instance.Mode == string(state.InstanceModeWorker)
	functionMode := request.ExecutionMode == api.ExecutionModeRequest && request.FrozenInputs.AppType == state.AppTypeFunction &&
		request.FrozenInputs.WorkloadClass == state.WorkloadClassHTTP && instance.Mode == string(state.InstanceModeNormal)
	if c == nil || c.cli == nil || !workerMode && !functionMode || !qualificationSmokeProbeFrameMatches(frame, request, instance) {
		return result, state.ErrConflict
	}
	policy, _, err := state.EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		return result, err
	}
	var message *state.EnvironmentQualificationQueueSmokeMessage
	for index := range policy.QueueMessages {
		if policy.QueueMessages[index].BindingName == bindingName {
			message = &policy.QueueMessages[index]
			break
		}
	}
	if message == nil || message.Method != http.MethodPost || message.Path == "" ||
		message.Mode != "push" && message.Mode != "pull" || policy.Port < 1 || policy.Port > 65535 {
		return result, state.ErrConflict
	}
	if message.Mode == "push" && message.TriggerID == "" || message.Mode == "pull" && message.TriggerID != "" {
		return result, state.ErrConflict
	}
	expectedPath := "/"
	if message.Mode == "push" {
		expectedPath = "/_triggers/esm/" + message.TriggerID
	}
	if message.Path != expectedPath {
		return result, state.ErrConflict
	}
	messageID := qualificationQueueSmokeMessageID(request, message.BindingID)
	headers := make(http.Header)
	api.PlatformIdentity{RequestID: frame.RequestID, AppID: frame.AppID, DeploymentID: frame.DeploymentID,
		InstanceID: frame.InstanceID, NodeID: frame.NodeID}.ApplyGuestHeaders(headers)
	headers.Set(api.InvocationIDHeader, messageID)
	source := string(state.InvocationQueue)
	if message.Mode == "push" {
		// Trigger dispatch uses the guest-visible source "esm" even when
		// the backing broker is Gregale's in-platform queue.
		source = "esm"
	}
	headers.Set(api.InvocationSourceHeader, source)
	headers.Set("Content-Type", "application/json")
	protoHeaders := make([]*vmmdpb.Header, 0, len(headers))
	for name, values := range headers {
		for _, value := range values {
			protoHeaders = append(protoHeaders, &vmmdpb.Header{Name: name, Value: value})
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.TimeoutMillis)*time.Millisecond)
	defer cancel()
	probeCtx = wire.WithCorrelationOutgoing(probeCtx, wire.CorrelationFields{RequestID: frame.RequestID, WakeID: frame.WakeID,
		AppID: frame.AppID, DeploymentID: frame.DeploymentID, InstanceID: frame.InstanceID, NodeID: frame.NodeID})
	stream, err := c.cli.ForwardHTTPStream(probeCtx)
	if err != nil {
		return result, fmt.Errorf("open private qualification queue probe: %w", err)
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_Init{Init: &vmmdpb.ForwardHTTPRequestInit{
		Instance: frame.InstanceID, Method: message.Method, RequestUri: message.Path, Headers: protoHeaders, Port: uint32(policy.Port),
		AppProtocol: api.AppProtocolHTTP1, ContentLength: int64(len(message.Payload)), ContentLengthKnown: true,
	}}}); err != nil {
		return result, fmt.Errorf("send private qualification queue probe: %w", err)
	}
	if err := stream.Send(&vmmdpb.ForwardHTTPStreamRequest{Frame: &vmmdpb.ForwardHTTPStreamRequest_BodyChunk{BodyChunk: append([]byte(nil), message.Payload...)}}); err != nil {
		return result, fmt.Errorf("send private qualification queue message: %w", err)
	}
	if err := stream.CloseSend(); err != nil {
		return result, fmt.Errorf("finish private qualification queue probe: %w", err)
	}
	var responseBytes, bodyChunks int
	var body []byte
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, fmt.Errorf("read private qualification queue response: %w", err)
		}
		switch responseFrame := response.GetFrame().(type) {
		case *vmmdpb.ForwardHTTPStreamResponse_Init:
			if result.StatusCode != 0 || responseFrame.Init == nil || responseFrame.Init.Error != "" || responseFrame.Init.Status < 100 || responseFrame.Init.Status > 599 {
				return result, fmt.Errorf("private qualification queue response was malformed or unavailable: %w", state.ErrConflict)
			}
			result.StatusCode = int(responseFrame.Init.Status)
		case *vmmdpb.ForwardHTTPStreamResponse_BodyChunk:
			if result.StatusCode == 0 {
				return result, fmt.Errorf("private qualification queue body preceded response headers: %w", state.ErrConflict)
			}
			bodyChunks++
			if bodyChunks > qualificationSmokeResponseMaxBodyChunks || len(responseFrame.BodyChunk) > qualificationSmokeResponseMaxBytes-responseBytes {
				return result, fmt.Errorf("private qualification queue response exceeded smoke limits: %w", state.ErrConflict)
			}
			responseBytes += len(responseFrame.BodyChunk)
			if message.Mode == "push" {
				body = append(body, responseFrame.BodyChunk...)
			}
		default:
			return result, fmt.Errorf("private qualification queue response frame is unsupported: %w", state.ErrConflict)
		}
	}
	if result.StatusCode == 0 {
		return result, fmt.Errorf("private qualification queue response was empty: %w", state.ErrConflict)
	}
	if message.Mode == "pull" {
		result.Completed = true
		result.Failed = result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices
		return result, nil
	}
	failed, err := qualificationWorkerQueueItemFailed(body, messageID)
	if err != nil {
		return result, fmt.Errorf("private qualification queue acknowledgement was malformed: %w", errors.Join(err, state.ErrConflict))
	}
	result.Completed, result.Failed = true, failed
	return result, nil
}

func qualificationWorkerQueueItemFailed(body []byte, itemID string) (bool, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false, nil
	}
	var root json.RawMessage
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return false, err
	}
	if len(root) == 0 || root[0] != '{' {
		return false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(root, &fields); err != nil {
		return false, err
	}
	raw, exists := fields["batchItemFailures"]
	if !exists {
		return false, nil
	}
	if first := bytes.TrimSpace(raw); len(first) == 0 || first[0] != '[' {
		return false, fmt.Errorf("batchItemFailures must be an array")
	}
	var response struct {
		Failures []struct {
			ItemIdentifier string `json:"itemIdentifier"`
		} `json:"batchItemFailures"`
	}
	if err := json.Unmarshal(root, &response); err != nil {
		return false, err
	}
	for _, failure := range response.Failures {
		if failure.ItemIdentifier == itemID {
			return true, nil
		}
	}
	return false, nil
}

func qualificationSmokeProbeFrameMatches(frame state.EnvironmentQualificationExecution,
	request state.EnvironmentWorkloadQualificationRequest, instance state.Instance) bool {
	source := instance.ID == request.ReservedInstanceID && frame.CaptureInstanceID == ""
	restored := instance.ID != request.ReservedInstanceID && frame.CaptureInstanceID == request.ReservedInstanceID
	return (source || restored) && frame.InstanceID == instance.ID &&
		frame.RequestID == request.ID && frame.GraphID == request.GraphID && frame.AppID == request.AppID &&
		frame.DeploymentID == request.DeploymentID && frame.SourceID == request.FrozenInputs.SourceID &&
		frame.EnvironmentID == request.FrozenInputs.EnvironmentID && frame.RevisionID == request.FrozenInputs.RevisionID &&
		frame.Resource == request.Resource && frame.Scope == request.FrozenInputs.Scope && frame.PlanHash == request.FrozenInputs.PlanHash &&
		frame.Generation == request.FrozenInputs.Generation && frame.IntentVersion == request.FrozenInputs.IntentVersion &&
		frame.Attempt == request.Attempt && frame.Artifact == request.Artifact &&
		frame.NodeID == instance.NodeID && frame.WakeID == instance.WakeID && frame.RAMMB == instance.RAMMB &&
		instance.State == string(state.StateRunning) && instance.AppID == frame.AppID && instance.DeploymentID == frame.DeploymentID &&
		instance.NodeID != "" && instance.WakeID != ""
}

// The router resolves by the restored instance's pinned node, never the app's
// current placement. A node mismatch is rejected before dialing.
func (r *VMMRouter) ProbeEnvironmentQualificationHTTP(ctx context.Context, frame state.EnvironmentQualificationExecution,
	request state.EnvironmentWorkloadQualificationRequest, instance state.Instance) (EnvironmentQualificationHTTPProbeResult, error) {
	var result EnvironmentQualificationHTTPProbeResult
	if r == nil || frame.NodeID == "" || frame.NodeID != instance.NodeID {
		return result, state.ErrConflict
	}
	client, err := r.resolveFor(ctx, frame.NodeID)
	if err != nil {
		return result, err
	}
	probe, ok := client.(EnvironmentQualificationHTTPProbeVMM)
	if !ok {
		return result, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	return probe.ProbeEnvironmentQualificationHTTP(ctx, frame, request, instance)
}

func (r *VMMRouter) ProbeEnvironmentQualificationQueue(ctx context.Context, frame state.EnvironmentQualificationExecution,
	request state.EnvironmentWorkloadQualificationRequest, instance state.Instance, bindingName string) (EnvironmentQualificationQueueProbeResult, error) {
	var result EnvironmentQualificationQueueProbeResult
	if r == nil || frame.NodeID == "" || frame.NodeID != instance.NodeID {
		return result, state.ErrConflict
	}
	client, err := r.resolveFor(ctx, frame.NodeID)
	if err != nil {
		return result, err
	}
	probe, ok := client.(EnvironmentQualificationQueueProbeVMM)
	if !ok {
		return result, state.ErrEnvironmentWorkloadPreparationUnavailable
	}
	return probe.ProbeEnvironmentQualificationQueue(ctx, frame, request, instance, bindingName)
}
