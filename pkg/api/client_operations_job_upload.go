// adr: 649
package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// ReuseJobOperationUpload looks up a verified private receipt without bytes.
func (c *Client) ReuseJobOperationUpload(ctx context.Context, id string, proof OperationJobRuntimeProof, req OperationArtifactUploadRequest) (OperationJobArtifactResponse, error) {
	var out OperationJobArtifactResponse
	if err := c.validateJobOperationClient(); err != nil {
		return out, err
	}
	err := c.doWithHeaders(ctx, http.MethodPost, "/v1/runtime/job-operations/"+url.PathEscape(id)+"/artifact-upload-receipts", req, &out, proof.headers())
	return out, err
}

// UploadJobOperationArtifact streams bytes once; the declaration's stable ID,
// size and checksum bind the private receipt. It never retries business work.
func (c *Client) UploadJobOperationArtifact(ctx context.Context, id string, proof OperationJobRuntimeProof, declaration OperationArtifactUploadRequest, body io.Reader) (OperationJobArtifactResponse, error) {
	var out OperationJobArtifactResponse
	if err := c.validateJobOperationClient(); err != nil {
		return out, err
	}
	query := url.Values{"report_id": {declaration.ReportID}, "name": {declaration.Name}, "size_bytes": {strconv.FormatInt(declaration.SizeBytes, 10)}, "sha256": {declaration.SHA256}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runtime/job-operations/"+url.PathEscape(id)+"/artifact-uploads?"+query.Encode(), body)
	if err != nil {
		return out, err
	}
	req.Header = proof.headers()
	req.Header.Set("Content-Type", "application/octet-stream")
	cli := *c.http
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	err = c.doReq(&cli, req, &out)
	return out, err
}
