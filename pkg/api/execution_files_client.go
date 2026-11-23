package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/runfile"
)

func (c *Client) PrepareExecutionFile(ctx context.Context, r CreateExecutionFileRequest) (ExecutionFileResponse, error) {
	var out ExecutionFileResponse
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/execution-files", r, &out)
	return out, err
}
func (c *Client) GetExecutionFile(ctx context.Context, id string) (ExecutionFileResponse, error) {
	var out ExecutionFileResponse
	return out, c.do(ctx, http.MethodGet, "/v1/execution-files/"+url.PathEscape(id), nil, &out)
}
func (c *Client) DeleteExecutionFile(ctx context.Context, id string) error {
	return c.doWithoutIdempotencyKey(ctx, http.MethodDelete, "/v1/execution-files/"+url.PathEscape(id), nil, nil)
}

// UploadExecutionFile streams exactly size bytes. The prepared immutable ID is
// safe to retry; a ready object is never overwritten. No transport retry occurs.
func (c *Client) UploadExecutionFile(ctx context.Context, id string, body io.Reader, size int64, digest string) (ExecutionFileResponse, error) {
	var out ExecutionFileResponse
	if !runfile.ValidID(id) || size < 0 || size > ExecutionFileHardMaxBytes || !runfile.ValidDigest(digest) {
		return out, fmt.Errorf("invalid execution file metadata")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/v1/execution-files/"+id+"/content", io.LimitReader(body, size+1))
	if err != nil {
		return out, err
	}
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Content-Sha256", digest)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	err = c.doReq(c.uploadHTTP(), req, &out)
	return out, err
}

// DownloadExecutionFile verifies both the authenticated metadata and received
// bytes. Callers should publish local files only after this method succeeds.
func (c *Client) DownloadExecutionFile(ctx context.Context, id string, dst io.Writer) (ExecutionFileResponse, error) {
	meta, err := c.GetExecutionFile(ctx, id)
	if err != nil {
		return meta, err
	}
	if !runfile.ValidID(id) || meta.ID != id || meta.SizeBytes < 0 || meta.SizeBytes > ExecutionFileHardMaxBytes || !runfile.ValidDigest(meta.SHA256) {
		return meta, fmt.Errorf("invalid Run file download metadata")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/execution-files/"+id+"/content", nil)
	if err != nil {
		return meta, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.uploadHTTP().Do(req)
	if err != nil {
		return meta, err
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		data, readErr := readBoundedResponse(response, maxResponseBodyBytes)
		if readErr != nil {
			return meta, readErr
		}
		return meta, apiErrorFromResponse(response, data)
	}
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(dst, h), io.LimitReader(response.Body, meta.SizeBytes+1), make([]byte, ExecutionFileStreamChunkBytes))
	if err != nil {
		return meta, err
	}
	if n != meta.SizeBytes || "sha256:"+hex.EncodeToString(h.Sum(nil)) != meta.SHA256 {
		return meta, fmt.Errorf("run file download length or checksum mismatch")
	}
	return meta, nil
}

// CreateAgentExecutionFileGrant delegates a ready upload to one logical Agent
// tool call. Persist the returned grant with the pending dispatch before retrying.
func (c *Client) CreateAgentExecutionFileGrant(ctx context.Context, id string, r CreateAgentExecutionFileGrantRequest) (AgentExecutionFileGrant, error) {
	var out AgentExecutionFileGrant
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/execution-files/"+url.PathEscape(id)+"/agent-grants", r, &out)
	return out, err
}
