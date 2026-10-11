package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) PutOperationDefinition(ctx context.Context, slug, deployment, name string, spec OperationDefinitionSpec) (OperationDefinitionResponse, error) {
	var out OperationDefinitionResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-definitions/" + url.PathEscape(name)
	err := c.doOperation(ctx, http.MethodPut, path, spec, &out)
	return out, err
}

func (c *Client) ListOperationDefinitions(ctx context.Context, slug, deployment string) (OperationDefinitionsResponse, error) {
	var out OperationDefinitionsResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-definitions"
	err := c.doOperation(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetOperationDefinition(ctx context.Context, slug, deployment, name string) (OperationDefinitionResponse, error) {
	var out OperationDefinitionResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-definitions/" + url.PathEscape(name)
	err := c.doOperation(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetOperationDoctor(ctx context.Context, slug, deployment, tenant, name string) (OperationDoctorResponse, error) {
	var out OperationDoctorResponse
	query := url.Values{"tenant_id": {tenant}}
	if name != "" {
		query.Set("name", name)
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-doctor?" + query.Encode()
	err := c.doOperation(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperationIdentity(ctx context.Context) (OperationTenantIdentity, error) {
	var out OperationTenantIdentity
	err := c.doOperation(ctx, http.MethodGet, "/v1/platform-tenant-self/customer-operations/identity", nil, &out)
	return out, err
}

func (c *Client) GetOperation(ctx context.Context, slug, id string) (OperationResponse, error) {
	var out OperationResponse
	err := c.doOperation(ctx, http.MethodGet, operationAppPath(slug, id), nil, &out)
	return out, err
}

func (c *Client) CancelOperation(ctx context.Context, slug, id string, req OperationCancellationRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.doOperation(ctx, http.MethodPost, operationAppPath(slug, id)+"/cancel", req, &out)
	return out, err
}

func (c *Client) StartPlatformTenantSelfOperation(ctx context.Context, req OperationStartRequest, key string) (OperationAcceptedResponse, error) {
	var out OperationAcceptedResponse
	if !validOperationSubmissionKey(key) {
		return out, fmt.Errorf("operation submission requires a stable idempotency key")
	}
	err := c.doOperationWithKey(ctx, http.MethodPost, "/v1/platform-tenant-self/customer-operations", req, &out, key)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperation(ctx context.Context, id string) (OperationResponse, error) {
	var out OperationResponse
	err := c.doOperation(ctx, http.MethodGet, operationSelfPath(id), nil, &out)
	return out, err
}

func (c *Client) ListPlatformTenantSelfOperations(ctx context.Context, opts OperationListOptions) (OperationListResponse, error) {
	var out OperationListResponse
	query := url.Values{"app_id": {opts.AppID}, "scope": {opts.Scope}}
	if opts.Name != "" {
		query.Set("name", opts.Name)
	}
	if opts.State != "" {
		query.Set("state", string(opts.State))
	}
	if opts.Limit != 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		query.Set("cursor", opts.Cursor)
	}
	err := c.doOperation(ctx, http.MethodGet, "/v1/platform-tenant-self/customer-operations?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperationEvents(ctx context.Context, id string, after int64) (OperationEventsResponse, error) {
	var out OperationEventsResponse
	err := c.doOperation(ctx, http.MethodGet, operationSelfPath(id)+"/events?after="+strconv.FormatInt(after, 10), nil, &out)
	return out, err
}

func (c *Client) CancelPlatformTenantSelfOperation(ctx context.Context, id string, req OperationCancellationRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.doOperation(ctx, http.MethodPost, operationSelfPath(id)+"/cancel", req, &out)
	return out, err
}

func operationAppPath(slug, id string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/operations/" + url.PathEscape(id)
}

func operationSelfPath(id string) string {
	return "/v1/platform-tenant-self/customer-operations/" + url.PathEscape(id)
}

func (c *Client) RecoverOperation(ctx context.Context, slug, id string, req OperationRecoveryRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.doOperation(ctx, http.MethodPost, operationAppPath(slug, id)+"/recover", req, &out)
	return out, err
}

func (c *Client) DownloadOperationArtifact(ctx context.Context, slug, id, artifact string, dst io.Writer) (int64, error) {
	return c.downloadOperationArtifact(ctx, operationAppPath(slug, id)+"/artifacts/"+url.PathEscape(artifact), dst)
}

func (c *Client) DownloadPlatformTenantSelfOperationArtifact(ctx context.Context, id, artifact string, dst io.Writer) (int64, error) {
	return c.downloadOperationArtifact(ctx, operationSelfPath(id)+"/artifacts/"+url.PathEscape(artifact), dst)
}

func (c *Client) downloadOperationArtifact(ctx context.Context, path string, dst io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
	c.addAuthHeader(req)
	// Artifact routes serve bytes directly. Do not send customer credentials
	// through provider redirects, even if a future server starts returning one.
	cli := *c.http
	cli.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := cli.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download operation artifact: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, err := readOperationResponse(resp)
		if err != nil {
			return 0, err
		}
		return 0, operationAPIError(resp, data)
	}
	limit := operationArtifactMaxBytes
	if resp.ContentLength < 0 || resp.ContentLength > limit {
		return 0, fmt.Errorf("operation artifact has an invalid content length")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(resp.Body, resp.ContentLength+1))
	if err != nil {
		return n, fmt.Errorf("download operation artifact: %w", err)
	}
	if n != resp.ContentLength {
		return n, &OperationArtifactTruncatedError{Expected: resp.ContentLength, Received: n}
	}
	if got := fmt.Sprintf("sha256:%x", hash.Sum(nil)); got != resp.Header.Get("X-Gregale-Artifact-Sha256") {
		return n, fmt.Errorf("operation artifact checksum mismatch")
	}
	return n, nil
}

// OperationRuntimeProof is ephemeral authority for the current HTTP attempt.
type OperationRuntimeProof struct {
	InvocationID string
	Attempt      int
	Capability   string `json:"-"`
}

func (p OperationRuntimeProof) headers() http.Header {
	return http.Header{InvocationIDHeader: {p.InvocationID}, OperationAttemptHeader: {strconv.Itoa(p.Attempt)}, OperationCapabilityHeader: {p.Capability}}
}
func (c *Client) ReportOperationProgress(ctx context.Context, id string, proof OperationRuntimeProof, req OperationReportRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.doOperationWithHeaders(ctx, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/progress", req, &out, proof.headers())
	return out, err
}
func (c *Client) AttachOperationArtifact(ctx context.Context, id string, proof OperationRuntimeProof, req OperationArtifactRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.doOperationWithHeaders(ctx, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/artifacts", req, &out, proof.headers())
	return out, err
}

func (c *Client) ListAccountOperations(ctx context.Context, slug string, opts OperationListOptions) (OperationListResponse, error) {
	var out OperationListResponse
	query := url.Values{"scope": {opts.Scope}}
	if opts.TenantID != "" {
		query.Set("tenant_id", opts.TenantID)
	}
	if opts.Name != "" {
		query.Set("name", opts.Name)
	}
	if opts.State != "" {
		query.Set("state", string(opts.State))
	}
	if opts.Limit != 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		query.Set("cursor", opts.Cursor)
	}
	err := c.doOperation(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/operations?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetAccountOperationEvents(ctx context.Context, slug, id string, after int64) (OperationEventsResponse, error) {
	var out OperationEventsResponse
	err := c.doOperation(ctx, http.MethodGet, operationAppPath(slug, id)+"/events?after="+strconv.FormatInt(after, 10), nil, &out)
	return out, err
}

func (c *Client) GetOperationExecutions(ctx context.Context, slug, id string, after, limit int) (OperationExecutionsResponse, error) {
	var out OperationExecutionsResponse
	query := url.Values{"after": {strconv.Itoa(after)}}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	err := c.doOperation(ctx, http.MethodGet, operationAppPath(slug, id)+"/executions?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) RetryOperationDelivery(ctx context.Context, slug, id string) (OperationResponse, error) {
	var out OperationResponse
	err := c.doOperation(ctx, http.MethodPost, operationAppPath(slug, id)+"/retry-delivery", nil, &out)
	return out, err
}

func (c *Client) GetOperationDelivery(ctx context.Context, slug, id string) (OperationDeliveryInspection, error) {
	var out OperationDeliveryInspection
	err := c.doOperation(ctx, http.MethodGet, operationAppPath(slug, id)+"/delivery", nil, &out)
	return out, err
}
func (c *Client) GetOperationDeliveryAttempts(ctx context.Context, slug, id string, limit int, cursor string) (OperationDeliveryAttemptsResponse, error) {
	var out OperationDeliveryAttemptsResponse
	query := url.Values{}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	path := operationAppPath(slug, id) + "/delivery-attempts"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	err := c.doOperation(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
func (c *Client) RetryOperationDeliveryWithReceipt(ctx context.Context, slug, id string, req OperationDeliveryRetryRequest) (OperationDeliveryRetryResponse, error) {
	var out OperationDeliveryRetryResponse
	err := c.doOperation(ctx, http.MethodPost, operationAppPath(slug, id)+"/delivery-retries", req, &out)
	return out, err
}
