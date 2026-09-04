package api

// upload.go contains the client side of the resumable source upload
// protocol. The server surface is introduced independently so older apid
// instances can still be used by the CLI; StartUpload reports
// ErrResumableUploadUnsupported for that compatibility case.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// ResumableUploadSession is the client view of a server-side upload session.
// ChunkSize is selected by apid and is the maximum body size accepted by each
// PATCH request.
type ResumableUploadSession struct {
	UploadID  string
	ChunkSize int64
	TotalSize int64
	ExpiresAt string
}

// ErrResumableUploadUnsupported means the API server predates the resumable
// upload routes. The CLI uses this as a safe signal to fall back to the
// existing single-request multipart endpoint.
var ErrResumableUploadUnsupported = errors.New("api: resumable uploads are not supported by this server")

type resumableUploadStartRequest struct {
	AppSlug   string  `json:"app_slug"`
	TotalSize int64   `json:"total_size"`
	Sha256Hex *string `json:"sha256_hex,omitempty"`
}

type resumableUploadStartResponse struct {
	UploadID  string `json:"upload_id"`
	ChunkSize int64  `json:"chunk_size"`
	TotalSize int64  `json:"total_size"`
	ExpiresAt string `json:"expires_at"`
}

// StartUpload opens a resumable upload session. sha256Hex is optional; pass
// an empty string when the caller wants to calculate the digest while sending
// chunks instead of reading the archive once before the upload begins.
func (c *Client) StartUpload(ctx context.Context, appSlug string, totalSize int64, sha256Hex string) (ResumableUploadSession, error) {
	var digest *string
	if sha256Hex != "" {
		digest = &sha256Hex
	}
	var out resumableUploadStartResponse
	_, _, err := c.doResumableUpload(ctx, http.MethodPost, "/v1/uploads", resumableUploadStartRequest{
		AppSlug:   appSlug,
		TotalSize: totalSize,
		Sha256Hex: digest,
	}, "application/json", nil, &out, true)
	if err != nil {
		return ResumableUploadSession{}, err
	}
	return ResumableUploadSession{
		UploadID:  out.UploadID,
		ChunkSize: out.ChunkSize,
		TotalSize: out.TotalSize,
		ExpiresAt: out.ExpiresAt,
	}, nil
}

// AppendUpload appends one chunk at the absolute offset supplied by the
// caller. The returned offset is the server's post-append offset.
func (c *Client) AppendUpload(ctx context.Context, uploadID string, offset int64, chunk []byte) (int64, error) {
	headers := make(http.Header)
	headers.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	respHeaders, _, err := c.doResumableUpload(ctx, http.MethodPatch,
		"/v1/uploads/"+url.PathEscape(uploadID), bytes.NewReader(chunk),
		"application/offset+octet-stream", headers, nil, false)
	if err != nil {
		return 0, err
	}
	raw := respHeaders.Get("Upload-Offset")
	if raw == "" {
		return 0, errors.New("api: resumable append response missing Upload-Offset")
	}
	next, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || next < 0 {
		return 0, fmt.Errorf("api: invalid Upload-Offset response %q", raw)
	}
	want := offset + int64(len(chunk))
	if next != want {
		return 0, fmt.Errorf("api: resumable append advanced to %d, want %d", next, want)
	}
	return next, nil
}

// CommitUpload finalizes a complete session and returns the queued deployment.
// A retry after the server has committed returns the typed
// CodeUploadSessionAlreadyCommitted problem; callers can use its deployment
// id to retrieve the original deployment without re-uploading.
func (c *Client) CommitUpload(ctx context.Context, uploadID string) (DeploymentResponse, error) {
	var out DeploymentResponse
	_, _, err := c.doResumableUpload(ctx, http.MethodPost,
		"/v1/uploads/"+url.PathEscape(uploadID)+"/commit", nil,
		"", nil, &out, false)
	return out, err
}

// CancelUpload cancels an open session and asks apid to remove its spool
// file. Cancellation is best-effort from the CLI's error paths.
func (c *Client) CancelUpload(ctx context.Context, uploadID string) error {
	_, _, err := c.doResumableUpload(ctx, http.MethodDelete,
		"/v1/uploads/"+url.PathEscape(uploadID), nil, "", nil, nil, false)
	return err
}

// doResumableUpload executes one request from the upload protocol and returns
// response headers plus the bounded response body. It deliberately does not
// use doReq: PATCH must retain Upload-Offset, and the response header is the
// protocol's acknowledgment.
func (c *Client) doResumableUpload(ctx context.Context, method, path string, body any, contentType string, headers http.Header, out any, start bool) (http.Header, []byte, error) {
	var reader io.Reader
	switch v := body.(type) {
	case nil:
	case io.Reader:
		reader = v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Idempotency-Key", newUUIDv4())
	}

	cli := c.uploadHTTP()
	if b, ok := reqbudget.FromContext(req.Context()); ok {
		newCtx, cancel, _ := b.WithCeiling(req.Context(), cli.Timeout)
		defer cancel()
		req = req.Clone(newCtx)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("could not reach the API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		if start && resp.StatusCode == http.StatusNotFound {
			return nil, nil, ErrResumableUploadUnsupported
		}
		var p Problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				p = *p.WithHeader("Retry-After", retryAfter)
			}
			return nil, nil, &APIError{Problem: p}
		}
		return nil, nil, fmt.Errorf("API error: %s", resp.Status)
	}

	responseHeaders := resp.Header.Clone()
	if c.cache != nil {
		c.cache.MaybeRefresh(req.URL.Path, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, nil, fmt.Errorf("decode response: %w", err)
		}
	}
	return responseHeaders, data, nil
}
