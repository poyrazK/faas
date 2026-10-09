// adr: 664
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
)

// Job runtime clients are tokenless. This proof is injected by schedd and
// cannot be replaced by account credentials or an application workload JWT.
type OperationJobRuntimeProof struct {
	RunID, InstanceID   string
	Generation, Attempt int
	Capability          string `json:"-"`
}

func (OperationJobRuntimeProof) String() string         { return "job operation proof (private)" }
func (p OperationJobRuntimeProof) GoString() string     { return p.String() }
func (p OperationJobRuntimeProof) LogValue() slog.Value { return slog.StringValue(p.String()) }
func (p OperationJobRuntimeProof) headers() http.Header {
	return http.Header{OperationJobRunHeader: {p.RunID}, OperationJobInstanceHeader: {p.InstanceID}, OperationGenerationHeader: {strconv.Itoa(p.Generation)}, OperationAttemptHeader: {strconv.Itoa(p.Attempt)}, OperationJobCapabilityHeader: {p.Capability}}
}
func (c *Client) GetJobOperationExecutionControl(ctx context.Context, id string, proof OperationJobRuntimeProof) (OperationJobControlResponse, error) {
	var out OperationJobControlResponse
	if err := c.validateJobOperationClient(); err != nil {
		return out, err
	}
	err := c.doOperationWithHeaders(ctx, http.MethodGet, "/v1/runtime/job-operations/"+url.PathEscape(id)+"/control", nil, &out, proof.headers())
	return out, err
}
func (c *Client) ReportJobOperationProgress(ctx context.Context, id string, proof OperationJobRuntimeProof, req OperationJobReportRequest) (OperationResponse, error) {
	return c.reportJobOperation(ctx, id, proof, "progress", req)
}
func (c *Client) PrepareJobOperationResult(ctx context.Context, id string, proof OperationJobRuntimeProof, req OperationJobReportRequest) (OperationResponse, error) {
	return c.reportJobOperation(ctx, id, proof, "result", req)
}
func (c *Client) reportJobOperation(ctx context.Context, id string, proof OperationJobRuntimeProof, kind string, req OperationJobReportRequest) (OperationResponse, error) {
	var out OperationResponse
	if err := c.validateJobOperationClient(); err != nil {
		return out, err
	}
	err := c.doOperationWithHeaders(ctx, http.MethodPost, "/v1/runtime/job-operations/"+url.PathEscape(id)+"/"+kind, req, &out, proof.headers())
	return out, err
}

func (c *Client) validateJobOperationClient() error {
	if c.Token() != "" {
		return fmt.Errorf("job runtime requires a tokenless client")
	}
	u, err := url.Parse(c.baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1"))) {
		return fmt.Errorf("job runtime requires HTTPS or loopback HTTP")
	}
	return nil
}

// ReuseJobOperationArtifact checks a committed private copy without source I/O.
func (c *Client) ReuseJobOperationArtifact(ctx context.Context, id string, proof OperationJobRuntimeProof, req OperationArtifactRequest) (OperationJobArtifactResponse, error) {
	return c.jobOperationArtifact(ctx, id, proof, "artifact-receipts", req)
}

// PrepareJobOperationArtifact verifies and retains a file, pending host confirmation.
func (c *Client) PrepareJobOperationArtifact(ctx context.Context, id string, proof OperationJobRuntimeProof, req OperationArtifactRequest) (OperationJobArtifactResponse, error) {
	return c.jobOperationArtifact(ctx, id, proof, "artifacts", req)
}
func (c *Client) jobOperationArtifact(ctx context.Context, id string, proof OperationJobRuntimeProof, action string, req OperationArtifactRequest) (OperationJobArtifactResponse, error) {
	var out OperationJobArtifactResponse
	if err := c.validateJobOperationClient(); err != nil {
		return out, err
	}
	err := c.doOperationWithHeaders(ctx, http.MethodPost, "/v1/runtime/job-operations/"+url.PathEscape(id)+"/"+action, req, &out, proof.headers())
	return out, err
}
