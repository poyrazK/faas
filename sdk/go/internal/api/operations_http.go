// adr: 521
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// These mirror existing platform bounds. The standalone SDK imports no daemon
// module; the monorepo contract test checks these values and wire definitions.
const (
	operationArtifactMaxBytes int64 = 256 << 20
	operationSubmissionKeyMax       = 128
	operationResponseMaxBytes       = 4 << 20 // existing SDK JSON response bound
	InvocationIDHeader              = "X-Faas-Invocation-Id"
)

func validOperationSubmissionKey(key string) bool {
	return key != "" && len(key) <= operationSubmissionKeyMax && !strings.ContainsAny(key, "\r\n\x00")
}

func (OperationRuntimeProof) String() string         { return "operation runtime proof (private)" }
func (p OperationRuntimeProof) GoString() string     { return p.String() }
func (p OperationRuntimeProof) LogValue() slog.Value { return slog.StringValue(p.String()) }

// OperationArtifactTruncatedError means the downloaded bytes did not match the
// declared length. Publish a temporary destination only after download succeeds.
type OperationArtifactTruncatedError struct {
	Expected int64
	Received int64
}

func (e *OperationArtifactTruncatedError) Error() string {
	return fmt.Sprintf("operation artifact length mismatch: expected %d bytes, received %d", e.Expected, e.Received)
}

func (c *Client) doOperation(ctx context.Context, method, path string, body, out any) error {
	return c.doOperationWithHeaders(ctx, method, path, body, out, nil)
}

func (c *Client) doOperationWithKey(ctx context.Context, method, path string, body, out any, key string) error {
	return c.doOperationWithHeaders(ctx, method, path, body, out, http.Header{"Idempotency-Key": {key}})
}

func (c *Client) doOperationWithHeaders(ctx context.Context, method, path string, body, out any, headers http.Header) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal operation request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	c.addAuthHeader(req) // Token rotation is read through the existing mutex.
	for name, values := range headers {
		req.Header[name] = append([]string(nil), values...)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && method != http.MethodHead && req.Header.Get("Idempotency-Key") == "" {
		req.Header.Set("Idempotency-Key", newUUIDv4())
	}
	cli := *c.http
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := cli.Do(req)
	if err != nil {
		return fmt.Errorf("operation request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := readOperationResponse(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return operationAPIError(resp, data)
	}
	if out == nil {
		return nil
	}
	if len(data) == 0 {
		return ErrNoBody
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode operation response: %w", err)
	}
	return nil
}

func readOperationResponse(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, operationResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read operation response: %w", err)
	}
	if len(data) > operationResponseMaxBytes {
		return nil, fmt.Errorf("operation response exceeds its byte bound")
	}
	if resp.ContentLength >= 0 && int64(len(data)) != resp.ContentLength {
		return nil, fmt.Errorf("operation response length mismatch")
	}
	return data, nil
}

func operationAPIError(resp *http.Response, data []byte) error {
	var problem Problem
	if json.Unmarshal(data, &problem) != nil || problem.Code == "" {
		problem = *NewProblem(resp.StatusCode, "operation_request_failed", "Operation request failed", http.StatusText(resp.StatusCode))
	}
	problem.Status = resp.StatusCode
	return &APIError{Problem: problem}
}

// StreamPlatformTenantSelfOperationEvents uses the current tenant-bound token.
// Parse snapshot/resync frames before applying operation events. auth_expired
// requires a refreshed credential; unavailable is a terminal access outcome.
func (c *Client) StreamPlatformTenantSelfOperationEvents(ctx context.Context, id string, after int64) (io.ReadCloser, error) {
	return c.streamOperation(ctx, operationSelfPath(id)+"/events", after)
}

func (c *Client) streamOperation(ctx context.Context, path string, after int64) (io.ReadCloser, error) {
	if after < 0 {
		return nil, fmt.Errorf("operation cursor must be non-negative")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	c.addAuthHeader(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	cli := *c.sseClient()
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := cli.Do(req)
	if err != nil {
		return nil, fmt.Errorf("operation stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		data, err := readOperationResponse(resp)
		if err != nil {
			return nil, err
		}
		return nil, operationAPIError(resp, data)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/event-stream" {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("operation stream has an invalid content type")
	}
	return resp.Body, nil
}
