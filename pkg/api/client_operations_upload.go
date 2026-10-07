// adr: 607
package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// ReuseOperationUpload looks up a verified private receipt without bytes.
func (c *Client) ReuseOperationUpload(ctx context.Context, id string, proof OperationRuntimeProof, req OperationArtifactUploadRequest) (OperationArtifactUploadResponse, error) {
	var out OperationArtifactUploadResponse
	cli := *c.http
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	err := c.doWithClientAndHeadersAndIdempotencyKey(ctx, &cli, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/artifact-upload-receipts", req, &out, "", proof.headers())
	return out, err
}

// UploadOperationArtifact streams bytes once; the declaration's stable ID,
// size and checksum bind the private receipt. It never retries business work.
func (c *Client) UploadOperationArtifact(ctx context.Context, id string, proof OperationRuntimeProof, declaration OperationArtifactUploadRequest, body io.Reader) (OperationArtifactUploadResponse, error) {
	var out OperationArtifactUploadResponse
	query := url.Values{"report_id": {declaration.ReportID}, "name": {declaration.Name}, "size_bytes": {strconv.FormatInt(declaration.SizeBytes, 10)}, "sha256": {declaration.SHA256}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/runtime/operations/"+url.PathEscape(id)+"/artifact-uploads?"+query.Encode(), body)
	if err != nil {
		return out, err
	}
	req.Header = proof.headers()
	req.Header.Set("Content-Type", "application/octet-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	cli := *c.http
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	err = c.doReq(&cli, req, &out)
	return out, err
}
