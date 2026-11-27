package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *PreviewClient) PrepareExecutionFile(ctx context.Context, r CreateExecutionFileRequest) (ExecutionFileResponse, error) {
	var out ExecutionFileResponse
	err := c.do(ctx, http.MethodPost, "/v1/execution-files", r, &out)
	return out, err
}
func (c *PreviewClient) GetExecutionFile(ctx context.Context, id string) (ExecutionFileResponse, error) {
	var out ExecutionFileResponse
	return out, c.do(ctx, http.MethodGet, "/v1/execution-files/"+url.PathEscape(id), nil, &out)
}
func (c *PreviewClient) DeleteExecutionFile(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/execution-files/"+url.PathEscape(id), nil, nil)
}

// UploadExecutionFile streams exactly size bytes. The prepared immutable ID is
// safe to retry; a ready object is never overwritten. No transport retry occurs.
func (c *PreviewClient) UploadExecutionFile(ctx context.Context, id string, body io.Reader, size int64, digest string) (ExecutionFileResponse, error) {
	var out ExecutionFileResponse
	if !validExecutionFileID(id) || size < 0 || size > ExecutionFileHardMaxBytes || !validExecutionFileDigest(digest) {
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
	req.Header.Set("X-Content-SHA256", digest)
	c.addAuthHeader(req)
	err = c.doReq(c.uploadHTTP(), req, &out)
	return out, err
}

// DownloadExecutionFile verifies both the authenticated metadata and received
// bytes. Callers should publish local files only after this method succeeds.
func (c *PreviewClient) DownloadExecutionFile(ctx context.Context, id string, dst io.Writer) (ExecutionFileResponse, error) {
	meta, err := c.GetExecutionFile(ctx, id)
	if err != nil {
		return meta, err
	}
	if !validExecutionFileID(id) || meta.ID != id || meta.SizeBytes < 0 || meta.SizeBytes > ExecutionFileHardMaxBytes || !validExecutionFileDigest(meta.SHA256) {
		return meta, fmt.Errorf("invalid Run file download metadata")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/execution-files/"+id+"/content", nil)
	if err != nil {
		return meta, err
	}
	c.addAuthHeader(req)
	response, err := c.uploadHTTP().Do(req)
	if err != nil {
		return meta, err
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if readErr != nil {
			return meta, readErr
		}
		var p Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			if p.Status == 0 {
				p.Status = response.StatusCode
			}
			return meta, &APIError{Problem: p}
		}
		return meta, fmt.Errorf("run file download failed (%d)", response.StatusCode)
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

// Bounds mirror pkg/api/limits.go; this module has no server dependency.
const ExecutionFileHardMaxBytes int64 = 64 << 20
const ExecutionFileStreamChunkBytes = 64 << 10

func validExecutionFileID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validExecutionFileDigest(digest string) bool {
	if !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	v, err := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return err == nil && len(v) == 32 && "sha256:"+hex.EncodeToString(v) == digest
}
func (c *PreviewClient) CreateExecutionWithIdempotencyKey(ctx context.Context, req CreateExecutionRequest, key string) (ExecutionResponse, error) {
	var out ExecutionResponse
	path := "/v1/executions"
	if req.UsesExtendedExecution() {
		path = "/v2/executions"
	}
	return out, c.doWithIdempotencyKey(ctx, "POST", path, req, &out, key)
}
