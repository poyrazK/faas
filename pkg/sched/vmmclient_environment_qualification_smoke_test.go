package sched

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
)

type qualificationSmokeHTTPStream struct {
	grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse]
	sent      []*vmmdpb.ForwardHTTPStreamRequest
	responses []*vmmdpb.ForwardHTTPStreamResponse
	index     int
	closed    bool
}

func (s *qualificationSmokeHTTPStream) Send(request *vmmdpb.ForwardHTTPStreamRequest) error {
	s.sent = append(s.sent, request)
	return nil
}

func (s *qualificationSmokeHTTPStream) CloseSend() error {
	s.closed = true
	return nil
}

func (s *qualificationSmokeHTTPStream) Recv() (*vmmdpb.ForwardHTTPStreamResponse, error) {
	if s.index >= len(s.responses) {
		return nil, io.EOF
	}
	response := s.responses[s.index]
	s.index++
	return response, nil
}

type qualificationSmokeHTTPClient struct {
	vmmdpb.VmmdClient
	stream *qualificationSmokeHTTPStream
}

func (c *qualificationSmokeHTTPClient) ForwardHTTPStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	return c.stream, nil
}

func qualificationSmokeHTTPProbeFixture() (state.EnvironmentWorkloadQualificationRequest, state.EnvironmentQualificationExecution, state.Instance) {
	request := state.EnvironmentWorkloadQualificationRequest{
		ID: "request", GraphID: "graph", DeploymentID: "deployment", AppID: "app", Resource: "workload/api",
		ExecutionMode: api.ExecutionModeRequest, Attempt: 1, ReservedInstanceID: "capture",
		Artifact: state.EnvironmentWorkloadArtifact{RootfsKey: "rootfs"},
		FrozenInputs: state.EnvironmentWorkloadRuntime{
			SourceID: "source", EnvironmentID: "environment", RevisionID: "revision", Resource: "workload/api",
			PlanHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Generation: 1, IntentVersion: 1,
			AppID: "app", Scope: "production", Runtime: map[string]json.RawMessage{"healthz": []byte(`"/ready"`), "port": []byte("8087")},
		},
	}
	instance := state.Instance{ID: "restored", WakeID: "wake", NodeID: "node", AppID: request.AppID,
		DeploymentID: request.DeploymentID, State: string(state.StateRunning), RAMMB: 512}
	frame := state.EnvironmentQualificationExecution{
		InstanceID: instance.ID, CaptureInstanceID: request.ReservedInstanceID, RequestID: request.ID, GraphID: request.GraphID,
		AppID: request.AppID, DeploymentID: request.DeploymentID, NodeID: instance.NodeID, WakeID: instance.WakeID,
		SourceID: request.FrozenInputs.SourceID, EnvironmentID: request.FrozenInputs.EnvironmentID,
		RevisionID: request.FrozenInputs.RevisionID, Resource: request.Resource, Scope: request.FrozenInputs.Scope,
		PlanHash: request.FrozenInputs.PlanHash, Generation: request.FrozenInputs.Generation,
		IntentVersion: request.FrozenInputs.IntentVersion, Attempt: request.Attempt, RAMMB: instance.RAMMB,
		Artifact: request.Artifact,
	}
	return request, frame, instance
}

func TestVMMClientEnvironmentQualificationSmokeProbeUsesFrozenHTTPContractAndDiscardsBody(t *testing.T) {
	request, frame, instance := qualificationSmokeHTTPProbeFixture()
	stream := &qualificationSmokeHTTPStream{responses: []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("must not enter smoke evidence")}},
	}}
	client := &VMMClient{cli: &qualificationSmokeHTTPClient{stream: stream}}
	result, err := client.ProbeEnvironmentQualificationHTTP(t.Context(), frame, request, instance)
	if err != nil || result != (EnvironmentQualificationHTTPProbeResult{StatusCode: 204, Completed: true}) {
		t.Fatalf("qualification probe result = %+v, %v", result, err)
	}
	if !stream.closed || len(stream.sent) != 1 {
		t.Fatalf("qualification request stream was not one closed, bodyless GET: closed=%t sent=%d", stream.closed, len(stream.sent))
	}
	init := stream.sent[0].GetInit()
	if init == nil || init.GetInstance() != instance.ID || init.GetMethod() != "GET" || init.GetRequestUri() != "/ready" ||
		init.GetPort() != 8087 || init.GetAppProtocol() != api.AppProtocolHTTP1 || !init.GetContentLengthKnown() || init.GetContentLength() != 0 {
		t.Fatalf("qualification probe did not use the frozen HTTP/1 zero-body contract: %+v", init)
	}
}

func TestVMMClientEnvironmentQualificationSmokeProbeRejectsIncompleteAndNon2xx(t *testing.T) {
	for name, responses := range map[string][]*vmmdpb.ForwardHTTPStreamResponse{
		"empty":            nil,
		"body_before_init": {{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("body")}}},
		"redirect":         {{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 302}}}},
		"duplicate_init": {
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
		},
		"vmmd_error": {{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 502, Error: "guest unavailable"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			request, frame, instance := qualificationSmokeHTTPProbeFixture()
			client := &VMMClient{cli: &qualificationSmokeHTTPClient{stream: &qualificationSmokeHTTPStream{responses: responses}}}
			if result, err := client.ProbeEnvironmentQualificationHTTP(t.Context(), frame, request, instance); err == nil || result.Completed {
				t.Fatalf("invalid qualification response was accepted: %+v, %v", result, err)
			}
		})
	}
}

func TestVMMClientEnvironmentQualificationSmokeProbeBoundsDiscardedResponse(t *testing.T) {
	tests := map[string][]*vmmdpb.ForwardHTTPStreamResponse{
		"body_bytes": {
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: make([]byte, qualificationSmokeResponseMaxBytes+1)}},
		},
	}
	tooManyChunks := []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
	}
	for range qualificationSmokeResponseMaxBodyChunks + 1 {
		tooManyChunks = append(tooManyChunks, &vmmdpb.ForwardHTTPStreamResponse{
			Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte{}},
		})
	}
	tests["body_chunks"] = tooManyChunks
	for name, responses := range tests {
		t.Run(name, func(t *testing.T) {
			request, frame, instance := qualificationSmokeHTTPProbeFixture()
			client := &VMMClient{cli: &qualificationSmokeHTTPClient{stream: &qualificationSmokeHTTPStream{responses: responses}}}
			if result, err := client.ProbeEnvironmentQualificationHTTP(t.Context(), frame, request, instance); err == nil || result.Completed {
				t.Fatalf("oversized qualification response was accepted: %+v, %v", result, err)
			}
		})
	}
}

func qualificationWorkerQueueProbeFixture() (state.EnvironmentWorkloadQualificationRequest, state.EnvironmentQualificationExecution, state.Instance) {
	enabled := true
	request := state.EnvironmentWorkloadQualificationRequest{
		ID: "33333333-3333-4333-8333-333333333333", GraphID: "graph", DeploymentID: "deployment", AppID: "app-worker",
		Resource: "workload/worker", ExecutionMode: api.ExecutionModeWorker, Attempt: 2, ReservedInstanceID: "capture",
		Artifact: state.EnvironmentWorkloadArtifact{RootfsKey: "rootfs"},
		FrozenInputs: state.EnvironmentWorkloadRuntime{SourceID: "source", EnvironmentID: "environment", RevisionID: "revision",
			Resource: "workload/worker", PlanHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Generation: 1,
			IntentVersion: 1, AppID: "app-worker", Scope: "production", WorkloadClass: state.WorkloadClassWorker,
			Runtime: map[string]json.RawMessage{}, Baseline: state.AppManifest{Port: 8087},
			QueueBindings: map[string]state.EnvironmentScopedQueueBinding{"orders": {
				BindingID: "11111111-1111-4111-8111-111111111111", TriggerID: "22222222-2222-4222-8222-222222222222",
				Contract: api.EnvironmentQueueBinding{QueueName: "orders", Mode: "push", WorkloadClass: "worker", Enabled: &enabled},
			}},
			QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"idempotency_key":"qualification-1"}`)}},
		},
	}
	instance := state.Instance{ID: "restored", WakeID: "wake", NodeID: "node", AppID: request.AppID,
		DeploymentID: request.DeploymentID, State: string(state.StateRunning), Mode: string(state.InstanceModeWorker), RAMMB: 512}
	frame := state.EnvironmentQualificationExecution{InstanceID: instance.ID, CaptureInstanceID: request.ReservedInstanceID,
		RequestID: request.ID, GraphID: request.GraphID, AppID: request.AppID, DeploymentID: request.DeploymentID,
		NodeID: instance.NodeID, WakeID: instance.WakeID, SourceID: request.FrozenInputs.SourceID,
		EnvironmentID: request.FrozenInputs.EnvironmentID, RevisionID: request.FrozenInputs.RevisionID, Resource: request.Resource,
		Scope: request.FrozenInputs.Scope, PlanHash: request.FrozenInputs.PlanHash, Generation: request.FrozenInputs.Generation,
		IntentVersion: request.FrozenInputs.IntentVersion, Attempt: request.Attempt, RAMMB: instance.RAMMB, Artifact: request.Artifact}
	return request, frame, instance
}

func TestVMMClientEnvironmentQualificationQueueUsesReviewedTriggerAndAcknowledgement(t *testing.T) {
	request, frame, instance := qualificationWorkerQueueProbeFixture()
	messageID := qualificationQueueSmokeMessageID(request, request.FrozenInputs.QueueBindings["orders"].BindingID)
	stream := &qualificationSmokeHTTPStream{responses: []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
	}}
	client := &VMMClient{cli: &qualificationSmokeHTTPClient{stream: stream}}
	result, err := client.ProbeEnvironmentQualificationQueue(t.Context(), frame, request, instance, "orders")
	if err != nil || result != (EnvironmentQualificationQueueProbeResult{StatusCode: 204, Completed: true}) {
		t.Fatalf("qualification queue result = %+v, %v", result, err)
	}
	if !stream.closed || len(stream.sent) != 2 {
		t.Fatalf("qualification queue request stream = closed:%t sent:%d, want closed with init and message", stream.closed, len(stream.sent))
	}
	init := stream.sent[0].GetInit()
	if init == nil || init.GetInstance() != instance.ID || init.GetMethod() != "POST" ||
		init.GetRequestUri() != "/_triggers/esm/22222222-2222-4222-8222-222222222222" || init.GetPort() != 8087 ||
		init.GetAppProtocol() != api.AppProtocolHTTP1 || !init.GetContentLengthKnown() || init.GetContentLength() != int64(len(`{"idempotency_key":"qualification-1"}`)) {
		t.Fatalf("qualification queue request did not use the frozen trigger contract: %+v", init)
	}
	var invocationID, invocationSource, contentType string
	for _, header := range init.GetHeaders() {
		switch strings.ToLower(header.GetName()) {
		case strings.ToLower(api.InvocationIDHeader):
			invocationID = header.GetValue()
		case strings.ToLower(api.InvocationSourceHeader):
			invocationSource = header.GetValue()
		case "content-type":
			contentType = header.GetValue()
		}
	}
	if invocationID != messageID || invocationSource != "esm" || contentType != "application/json" {
		t.Fatalf("qualification message headers id=%q source=%q content-type=%q", invocationID, invocationSource, contentType)
	}
	if got := string(stream.sent[1].GetBodyChunk()); got != `{"idempotency_key":"qualification-1"}` {
		t.Fatalf("qualification queue body = %q", got)
	}
}

func TestVMMClientEnvironmentQualificationPullQueueUsesGenericInvocationAcknowledgement(t *testing.T) {
	request, frame, instance := qualificationWorkerQueueProbeFixture()
	binding := request.FrozenInputs.QueueBindings["orders"]
	binding.TriggerID = ""
	binding.Contract.Mode = "pull"
	request.FrozenInputs.QueueBindings["orders"] = binding
	stream := &qualificationSmokeHTTPStream{responses: []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte(`{"ignored":"private response"}`)}},
	}}
	client := &VMMClient{cli: &qualificationSmokeHTTPClient{stream: stream}}
	result, err := client.ProbeEnvironmentQualificationQueue(t.Context(), frame, request, instance, "orders")
	if err != nil || result != (EnvironmentQualificationQueueProbeResult{StatusCode: 204, Completed: true}) {
		t.Fatalf("pull qualification result = %+v, %v", result, err)
	}
	if !stream.closed || len(stream.sent) != 2 {
		t.Fatalf("pull qualification stream = closed:%t sent:%d, want closed with init and message", stream.closed, len(stream.sent))
	}
	init := stream.sent[0].GetInit()
	if init == nil || init.GetInstance() != instance.ID || init.GetMethod() != http.MethodPost || init.GetRequestUri() != "/" ||
		init.GetPort() != 8087 || !init.GetContentLengthKnown() || init.GetContentLength() != int64(len(`{"idempotency_key":"qualification-1"}`)) {
		t.Fatalf("pull qualification request did not use the generic invocation contract: %+v", init)
	}
	var invocationID, invocationSource string
	for _, header := range init.GetHeaders() {
		switch strings.ToLower(header.GetName()) {
		case strings.ToLower(api.InvocationIDHeader):
			invocationID = header.GetValue()
		case strings.ToLower(api.InvocationSourceHeader):
			invocationSource = header.GetValue()
		}
	}
	if invocationID != qualificationQueueSmokeMessageID(request, binding.BindingID) || invocationSource != string(state.InvocationQueue) {
		t.Fatalf("pull invocation headers id=%q source=%q", invocationID, invocationSource)
	}
}

func TestVMMClientEnvironmentQualificationQueueAcceptsHTTPFunctionInstance(t *testing.T) {
	request, frame, instance := qualificationWorkerQueueProbeFixture()
	request.ExecutionMode = api.ExecutionModeRequest
	request.Resource, request.AppID = "workload/function", "app-function"
	request.FrozenInputs.Resource, request.FrozenInputs.AppID = request.Resource, request.AppID
	request.FrozenInputs.AppType, request.FrozenInputs.RuntimeBase = state.AppTypeFunction, "node22"
	request.FrozenInputs.WorkloadClass = state.WorkloadClassHTTP
	binding := request.FrozenInputs.QueueBindings["orders"]
	binding.Contract.WorkloadClass = "http"
	request.FrozenInputs.QueueBindings["orders"] = binding
	instance.AppID, instance.Mode = request.AppID, string(state.InstanceModeNormal)
	frame.Resource, frame.AppID = request.Resource, request.AppID
	stream := &qualificationSmokeHTTPStream{responses: []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
	}}
	client := &VMMClient{cli: &qualificationSmokeHTTPClient{stream: stream}}
	result, err := client.ProbeEnvironmentQualificationQueue(t.Context(), frame, request, instance, "orders")
	if err != nil || !result.Completed || result.StatusCode != 204 {
		t.Fatalf("HTTP function queue result = %+v, %v", result, err)
	}
}

func TestVMMClientEnvironmentQualificationWorkerQueueBoundsAcknowledgementBody(t *testing.T) {
	tests := map[string][]*vmmdpb.ForwardHTTPStreamResponse{
		"body_bytes": {
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
			{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: make([]byte, qualificationSmokeResponseMaxBytes+1)}},
		},
	}
	tooManyChunks := []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{Status: 204}}},
	}
	for range qualificationSmokeResponseMaxBodyChunks + 1 {
		tooManyChunks = append(tooManyChunks, &vmmdpb.ForwardHTTPStreamResponse{
			Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte{}},
		})
	}
	tests["body_chunks"] = tooManyChunks
	for name, responses := range tests {
		t.Run(name, func(t *testing.T) {
			request, frame, instance := qualificationWorkerQueueProbeFixture()
			stream := &qualificationSmokeHTTPStream{responses: responses}
			client := &VMMClient{cli: &qualificationSmokeHTTPClient{stream: stream}}
			result, err := client.ProbeEnvironmentQualificationQueue(t.Context(), frame, request, instance, "orders")
			if err == nil || result.Completed {
				t.Fatalf("oversized worker acknowledgement was accepted: %+v, %v", result, err)
			}
		})
	}
}

func TestQualificationWorkerQueueAcknowledgementMatchesGatewaySemantics(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		want bool
		bad  bool
	}{
		{name: "empty", body: nil},
		{name: "scalar success", body: []byte(`"ok"`)},
		{name: "missing member", body: []byte(`{"ok":true}`)},
		{name: "empty failures", body: []byte(`{"batchItemFailures":[]}`)},
		{name: "other item", body: []byte(`{"batchItemFailures":[{"itemIdentifier":"other"}]}`)},
		{name: "matching failure", body: []byte(`{"batchItemFailures":[{"itemIdentifier":"test-item"}]}`), want: true},
		{name: "malformed envelope", body: []byte(`{"batchItemFailures":null}`), bad: true},
		{name: "invalid json", body: []byte(`{"batchItemFailures":`), bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failed, err := qualificationWorkerQueueItemFailed(tc.body, "test-item")
			if failed != tc.want || (err != nil) != tc.bad {
				t.Fatalf("queue acknowledgement failed=%t err=%v, want failed=%t bad=%t", failed, err, tc.want, tc.bad)
			}
		})
	}
}
