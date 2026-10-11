package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
)

// A workload bearer and a trusted native attempt proof are both required.
type OperationWorkflowRuntimeProof struct {
	RunID, StepName     string
	Generation, Attempt int
	Capability          string `json:"-"`
}

func (OperationWorkflowRuntimeProof) String() string         { return "workflow operation proof (private)" }
func (p OperationWorkflowRuntimeProof) GoString() string     { return p.String() }
func (p OperationWorkflowRuntimeProof) LogValue() slog.Value { return slog.StringValue(p.String()) }
func (p OperationWorkflowRuntimeProof) headers() http.Header {
	return http.Header{OperationExecutionKindHeader: {"workflow"}, OperationWorkflowRunHeader: {p.RunID}, OperationWorkflowStepHeader: {p.StepName}, OperationGenerationHeader: {strconv.Itoa(p.Generation)}, OperationAttemptHeader: {strconv.Itoa(p.Attempt)}, OperationWorkflowCapabilityHeader: {p.Capability}}
}
func (c *Client) ReuseWorkflowOperationArtifact(ctx context.Context, id string, proof OperationWorkflowRuntimeProof, req OperationArtifactRequest) (OperationWorkflowArtifactResponse, error) {
	var out OperationWorkflowArtifactResponse
	err := c.doOperationWithHeaders(ctx, http.MethodPost, "/v1/runtime/workflow-operations/"+url.PathEscape(id)+"/artifact-receipts", req, &out, proof.headers())
	return out, err
}
func (c *Client) PrepareWorkflowOperationArtifact(ctx context.Context, id string, proof OperationWorkflowRuntimeProof, req OperationArtifactRequest) (OperationWorkflowArtifactResponse, error) {
	var out OperationWorkflowArtifactResponse
	err := c.doOperationWithHeaders(ctx, http.MethodPost, "/v1/runtime/workflow-operations/"+url.PathEscape(id)+"/artifacts", req, &out, proof.headers())
	return out, err
}

func (c *Client) GetWorkflowOperationExecutionControl(ctx context.Context, id string, proof OperationWorkflowRuntimeProof) (OperationWorkflowControlResponse, error) {
	var out OperationWorkflowControlResponse
	err := c.doOperationWithHeaders(ctx, http.MethodGet, "/v1/runtime/workflow-operations/"+url.PathEscape(id)+"/control", nil, &out, proof.headers())
	return out, err
}
