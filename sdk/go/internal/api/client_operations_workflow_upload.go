// adr: 608
package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// ReuseWorkflowOperationUpload looks up a verified private receipt without bytes.
func (c *Client) ReuseWorkflowOperationUpload(ctx context.Context, id string, proof OperationWorkflowRuntimeProof, req OperationArtifactUploadRequest) (OperationWorkflowArtifactResponse, error) {
	var out OperationWorkflowArtifactResponse
	err := c.doOperationWithHeaders(ctx, http.MethodPost, "/v1/runtime/workflow-operations/"+url.PathEscape(id)+"/artifact-upload-receipts", req, &out, proof.headers())
	return out, err
}

// UploadWorkflowOperationArtifact streams bytes once; the declaration's stable ID,
// size and checksum bind the private receipt. It never retries business work.
func (c *Client) UploadWorkflowOperationArtifact(ctx context.Context, id string, proof OperationWorkflowRuntimeProof, declaration OperationArtifactUploadRequest, body io.Reader) (OperationWorkflowArtifactResponse, error) {
	var out OperationWorkflowArtifactResponse
	query := url.Values{"report_id": {declaration.ReportID}, "name": {declaration.Name}, "size_bytes": {strconv.FormatInt(declaration.SizeBytes, 10)}, "sha256": {declaration.SHA256}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runtime/workflow-operations/"+url.PathEscape(id)+"/artifact-uploads?"+query.Encode(), body)
	if err != nil {
		return out, err
	}
	req.Header = proof.headers()
	req.Header.Set("Content-Type", "application/octet-stream")
	c.addAuthHeader(req)
	cli := *c.http
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	err = c.doReq(&cli, req, &out)
	return out, err
}
