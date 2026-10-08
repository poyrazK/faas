package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) PutOperationDefinition(ctx context.Context, slug, deployment, name string, spec OperationDefinitionSpec) (OperationDefinitionResponse, error) {
	var out OperationDefinitionResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-definitions/" + url.PathEscape(name)
	err := c.do(ctx, http.MethodPut, path, spec, &out)
	return out, err
}

func (c *Client) ListOperationDefinitions(ctx context.Context, slug, deployment string) (OperationDefinitionsResponse, error) {
	var out OperationDefinitionsResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-definitions"
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetOperationDefinition(ctx context.Context, slug, deployment, name string) (OperationDefinitionResponse, error) {
	var out OperationDefinitionResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-definitions/" + url.PathEscape(name)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetOperationDoctor(ctx context.Context, slug, deployment, tenant, name string) (OperationDoctorResponse, error) {
	var out OperationDoctorResponse
	query := url.Values{"tenant_id": {tenant}}
	if name != "" {
		query.Set("name", name)
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/deployments/" + url.PathEscape(deployment) + "/operation-doctor?" + query.Encode()
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperationIdentity(ctx context.Context) (OperationTenantIdentity, error) {
	var out OperationTenantIdentity
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/customer-operations/identity", nil, &out)
	return out, err
}

func (c *Client) GetOperation(ctx context.Context, slug, id string) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodGet, operationAppPath(slug, id), nil, &out)
	return out, err
}

func (c *Client) CancelOperation(ctx context.Context, slug, id string, req OperationCancellationRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/cancel", req, &out)
	return out, err
}

func (c *Client) StartPlatformTenantSelfOperation(ctx context.Context, req OperationStartRequest, key string) (OperationAcceptedResponse, error) {
	var out OperationAcceptedResponse
	if key == "" || len(key) > OperationIdempotencyKeyMaxBytes || strings.ContainsAny(key, "\r\n\x00") {
		return out, fmt.Errorf("operation submission requires a stable idempotency key")
	}
	err := c.doWithIdempotencyKey(ctx, http.MethodPost, "/v1/platform-tenant-self/customer-operations", req, &out, key)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperation(ctx context.Context, id string) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodGet, operationSelfPath(id), nil, &out)
	return out, err
}

func (c *Client) ListPlatformTenantSelfOperations(ctx context.Context, opts OperationListOptions) (OperationListResponse, error) {
	var out OperationListResponse
	query := url.Values{"app_id": {opts.AppID}, "scope": {opts.Scope}}
	if opts.SubjectType != "" {
		query.Set("subject_type", opts.SubjectType)
	}
	if opts.SubjectID != "" {
		query.Set("subject_id", opts.SubjectID)
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
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/customer-operations?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperationEvents(ctx context.Context, id string, after int64) (OperationEventsResponse, error) {
	var out OperationEventsResponse
	err := c.do(ctx, http.MethodGet, operationSelfPath(id)+"/events?after="+strconv.FormatInt(after, 10), nil, &out)
	return out, err
}

func (c *Client) CancelPlatformTenantSelfOperation(ctx context.Context, id string, req OperationCancellationRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodPost, operationSelfPath(id)+"/cancel", req, &out)
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
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/recover", req, &out)
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
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
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
		data, err := readBoundedResponse(resp, maxResponseBodyBytes)
		if err != nil {
			return 0, err
		}
		return 0, apiErrorFromResponse(resp, data)
	}
	limit := OperationArtifactSpoolMaxBytes
	if resp.ContentLength < 0 || resp.ContentLength > limit {
		return 0, fmt.Errorf("operation artifact has an invalid content length")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(resp.Body, resp.ContentLength+1))
	if err != nil {
		return n, fmt.Errorf("download operation artifact: %w", err)
	}
	if n != resp.ContentLength {
		return n, &ResponseTruncatedError{Expected: resp.ContentLength, Received: n}
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

func (OperationRuntimeProof) String() string         { return "operation runtime proof (private)" }
func (p OperationRuntimeProof) GoString() string     { return p.String() }
func (p OperationRuntimeProof) LogValue() slog.Value { return slog.StringValue(p.String()) }
func (p OperationRuntimeProof) headers() http.Header {
	return http.Header{InvocationIDHeader: {p.InvocationID}, OperationAttemptHeader: {strconv.Itoa(p.Attempt)}, OperationCapabilityHeader: {p.Capability}}
}
func (c *Client) ReportOperationProgress(ctx context.Context, id string, proof OperationRuntimeProof, req OperationReportRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.doWithHeaders(ctx, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/progress", req, &out, proof.headers())
	return out, err
}
func (c *Client) AttachOperationArtifact(ctx context.Context, id string, proof OperationRuntimeProof, req OperationArtifactRequest) (OperationResponse, error) {
	var out OperationResponse
	err := c.doWithHeaders(ctx, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/artifacts", req, &out, proof.headers())
	return out, err
}

func (c *Client) ListAccountOperations(ctx context.Context, slug string, opts OperationListOptions) (OperationListResponse, error) {
	var out OperationListResponse
	query := url.Values{"scope": {opts.Scope}}
	if opts.TenantID != "" {
		query.Set("tenant_id", opts.TenantID)
	}
	if opts.SubjectType != "" {
		query.Set("subject_type", opts.SubjectType)
	}
	if opts.SubjectID != "" {
		query.Set("subject_id", opts.SubjectID)
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
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/operations?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetAccountOperationEvents(ctx context.Context, slug, id string, after int64) (OperationEventsResponse, error) {
	var out OperationEventsResponse
	err := c.do(ctx, http.MethodGet, operationAppPath(slug, id)+"/events?after="+strconv.FormatInt(after, 10), nil, &out)
	return out, err
}

func (c *Client) GetOperationExecutions(ctx context.Context, slug, id string, after, limit int) (OperationExecutionsResponse, error) {
	var out OperationExecutionsResponse
	query := url.Values{"after": {strconv.Itoa(after)}}
	if limit != 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	err := c.do(ctx, http.MethodGet, operationAppPath(slug, id)+"/executions?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) RetryOperationDelivery(ctx context.Context, slug, id string) (OperationResponse, error) {
	var out OperationResponse
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/retry-delivery", nil, &out)
	return out, err
}

func (c *Client) GetOperationDelivery(ctx context.Context, slug, id string) (OperationDeliveryInspection, error) {
	var out OperationDeliveryInspection
	err := c.do(ctx, http.MethodGet, operationAppPath(slug, id)+"/delivery", nil, &out)
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
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
func (c *Client) RetryOperationDeliveryWithReceipt(ctx context.Context, slug, id string, req OperationDeliveryRetryRequest) (OperationDeliveryRetryResponse, error) {
	var out OperationDeliveryRetryResponse
	err := c.do(ctx, http.MethodPost, operationAppPath(slug, id)+"/delivery-retries", req, &out)
	return out, err
}

// ReportOperationMilestone publishes a fact already committed by the application.
func (c *Client) ReportOperationMilestone(ctx context.Context, id string, proof OperationRuntimeProof, req OperationMilestoneRequest) (OperationMilestone, error) {
	var out OperationMilestone
	err := c.doWithHeaders(ctx, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/milestones", req, &out, proof.headers())
	return out, err
}

func (c *Client) ValidateOperationMilestones(ctx context.Context, id string, proof OperationRuntimeProof, req OperationMilestoneValidationRequest) (OperationMilestoneValidationResponse, error) {
	var out OperationMilestoneValidationResponse
	err := c.doWithHeaders(ctx, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/milestones/validate", req, &out, proof.headers())
	return out, err
}

func (c *Client) ReportOperationWorkflowState(ctx context.Context, id string, proof OperationRuntimeProof, req OperationWorkflowStateReport) (OperationWorkflowStateReportResponse, error) {
	var out OperationWorkflowStateReportResponse
	err := c.doWithHeaders(ctx, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/workflow-states", req, &out, proof.headers())
	return out, err
}

func (c *Client) ValidateOperationWorkflowStates(ctx context.Context, id string, proof OperationRuntimeProof, req OperationWorkflowStateValidationRequest) (OperationWorkflowStateValidationResponse, error) {
	var out OperationWorkflowStateValidationResponse
	err := c.doWithHeaders(ctx, http.MethodPost, "/v1/runtime/operations/"+url.PathEscape(id)+"/workflow-states/validate", req, &out, proof.headers())
	return out, err
}

func operationMilestoneQuery(opts OperationMilestoneListOptions, reference, customer bool) string {
	query := url.Values{}
	if opts.Limit != 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		query.Set("cursor", opts.Cursor)
	}
	if opts.Workflow != "" || opts.WorkflowInstanceID != "" {
		query.Set("workflow", opts.Workflow)
		query.Set("workflow_instance_id", opts.WorkflowInstanceID)
	}
	if opts.WorkflowStateCursor != "" {
		query.Set("workflow_state_cursor", opts.WorkflowStateCursor)
	}
	if reference {
		if opts.WorkflowStaleOnly {
			query.Set("stale_only", "true")
		}
		query.Set("scope", opts.Scope)
		query.Set("subject_type", opts.SubjectType)
		query.Set("subject_id", opts.SubjectID)
		if customer {
			query.Set("app_id", opts.AppID)
		} else if opts.TenantID != "" {
			query.Set("tenant_id", opts.TenantID)
		}
	}
	return "?" + query.Encode()
}

func (c *Client) GetAccountOperationMilestones(ctx context.Context, slug, id string, opts OperationMilestoneListOptions) (OperationMilestonesResponse, error) {
	var out OperationMilestonesResponse
	err := c.do(ctx, http.MethodGet, operationAppPath(slug, id)+"/milestones"+operationMilestoneQuery(opts, false, false), nil, &out)
	return out, err
}

func (c *Client) GetPlatformTenantSelfOperationMilestones(ctx context.Context, id string, opts OperationMilestoneListOptions) (OperationMilestonesResponse, error) {
	var out OperationMilestonesResponse
	err := c.do(ctx, http.MethodGet, operationSelfPath(id)+"/milestones"+operationMilestoneQuery(opts, false, true), nil, &out)
	return out, err
}

func (c *Client) ListAccountBusinessMilestones(ctx context.Context, slug string, opts OperationMilestoneListOptions) (OperationMilestonesResponse, error) {
	var out OperationMilestonesResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/operation-milestones"+operationMilestoneQuery(opts, true, false), nil, &out)
	return out, err
}

func (c *Client) ListPlatformTenantSelfBusinessMilestones(ctx context.Context, opts OperationMilestoneListOptions) (OperationMilestonesResponse, error) {
	var out OperationMilestonesResponse
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/customer-operation-milestones"+operationMilestoneQuery(opts, true, true), nil, &out)
	return out, err
}

func operationWorkflowAttentionQuery(opts OperationWorkflowAttentionOptions, customer bool) string {
	query := url.Values{"scope": {opts.Scope}}
	for key, value := range map[string]string{"dependency_status": opts.DependencyStatus, "required_outcome_code": opts.RequiredOutcomeCode, "blocker_code": opts.BlockerCode, "workflow": opts.Workflow, "target_operation": opts.TargetOperation, "reason": opts.Reason, "cursor": opts.Cursor} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if customer {
		query.Set("app_id", opts.AppID)
	} else if opts.TenantID != "" {
		query.Set("tenant_id", opts.TenantID)
	}
	if opts.Limit != 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	return "?" + query.Encode()
}
func (c *Client) ListAccountWorkflowAttention(ctx context.Context, slug string, opts OperationWorkflowAttentionOptions) (OperationWorkflowAttentionResponse, error) {
	var out OperationWorkflowAttentionResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-attention"+operationWorkflowAttentionQuery(opts, false), nil, &out)
	return out, err
}
func (c *Client) ListPlatformTenantSelfWorkflowAttention(ctx context.Context, opts OperationWorkflowAttentionOptions) (OperationWorkflowAttentionResponse, error) {
	var out OperationWorkflowAttentionResponse
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-attention"+operationWorkflowAttentionQuery(opts, true), nil, &out)
	return out, err
}

func operationWorkflowAttentionSummaryQuery(opts OperationWorkflowAttentionSummaryOptions, customer bool) string {
	q := operationWorkflowAttentionQuery(opts.OperationWorkflowAttentionOptions, customer)
	if opts.GroupBy != "" {
		q += "&group_by=" + url.QueryEscape(opts.GroupBy)
	}
	return q
}
func (c *Client) SummarizeAccountWorkflowAttention(ctx context.Context, slug string, opts OperationWorkflowAttentionSummaryOptions) (OperationWorkflowAttentionSummary, error) {
	var out OperationWorkflowAttentionSummary
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-attention/summary"+operationWorkflowAttentionSummaryQuery(opts, false), nil, &out)
	return out, err
}
func (c *Client) SummarizePlatformTenantSelfWorkflowAttention(ctx context.Context, opts OperationWorkflowAttentionSummaryOptions) (OperationWorkflowAttentionSummary, error) {
	var out OperationWorkflowAttentionSummary
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-attention/summary"+operationWorkflowAttentionSummaryQuery(opts, true), nil, &out)
	return out, err
}

func operationWorkflowOutcomeQuery(opts OperationWorkflowOutcomeOptions, customer bool) string {
	query := url.Values{"scope": {opts.Scope}}
	for key, value := range map[string]string{"workflow": opts.Workflow, "code": opts.Code, "cursor": opts.Cursor} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if customer {
		query.Set("app_id", opts.AppID)
	} else if opts.TenantID != "" {
		query.Set("tenant_id", opts.TenantID)
	}
	if opts.Limit != 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	return "?" + query.Encode()
}
func (c *Client) ListAccountWorkflowOutcomes(ctx context.Context, slug string, opts OperationWorkflowOutcomeOptions) (OperationWorkflowOutcomesResponse, error) {
	query := operationWorkflowOutcomeQuery(opts, false)
	var out OperationWorkflowOutcomesResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-outcomes"+query, nil, &out)
	return out, err
}
func (c *Client) SummarizeAccountWorkflowOutcomes(ctx context.Context, slug string, opts OperationWorkflowOutcomeSummaryOptions) (OperationWorkflowOutcomeSummary, error) {
	query := operationWorkflowOutcomeQuery(opts.OperationWorkflowOutcomeOptions, false)
	if opts.GroupBy != "" {
		query += "&group_by=" + url.QueryEscape(opts.GroupBy)
	}
	var out OperationWorkflowOutcomeSummary
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-outcomes/summary"+query, nil, &out)
	return out, err
}
func (c *Client) ListPlatformTenantSelfWorkflowOutcomes(ctx context.Context, opts OperationWorkflowOutcomeOptions) (OperationWorkflowOutcomesResponse, error) {
	query := operationWorkflowOutcomeQuery(opts, true)
	var out OperationWorkflowOutcomesResponse
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-outcomes"+query, nil, &out)
	return out, err
}
func (c *Client) SummarizePlatformTenantSelfWorkflowOutcomes(ctx context.Context, opts OperationWorkflowOutcomeSummaryOptions) (OperationWorkflowOutcomeSummary, error) {
	query := operationWorkflowOutcomeQuery(opts.OperationWorkflowOutcomeOptions, true)
	if opts.GroupBy != "" {
		query += "&group_by=" + url.QueryEscape(opts.GroupBy)
	}
	var out OperationWorkflowOutcomeSummary
	err := c.do(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-outcomes/summary"+query, nil, &out)
	return out, err
}

// CheckAccountWorkflowReadiness observes retained requirements; tenant_id is mandatory.
func (c *Client) CheckAccountWorkflowReadiness(ctx context.Context, slug string, req OperationWorkflowReadinessRequest) (OperationWorkflowReadinessResponse, error) {
	var out OperationWorkflowReadinessResponse
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness", req, &out)
	return out, err
}

// CheckPlatformTenantSelfWorkflowReadiness uses the authenticated customer boundary.
func (c *Client) CheckPlatformTenantSelfWorkflowReadiness(ctx context.Context, req OperationWorkflowReadinessRequest) (OperationWorkflowReadinessResponse, error) {
	var out OperationWorkflowReadinessResponse
	err := c.do(ctx, http.MethodPost, "/v1/platform-tenant-self/workflow-readiness", req, &out)
	return out, err
}

func (c *Client) PreviewAccountWorkflowActions(ctx context.Context, slug string, req OperationWorkflowActionPreviewRequest) (OperationWorkflowActionPreviewResponse, error) {
	var out OperationWorkflowActionPreviewResponse
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/workflow-actions/preview", req, &out)
	return out, err
}

func (c *Client) PreviewPlatformTenantSelfWorkflowActions(ctx context.Context, req OperationWorkflowActionPreviewRequest) (OperationWorkflowActionPreviewResponse, error) {
	var out OperationWorkflowActionPreviewResponse
	err := c.do(ctx, http.MethodPost, "/v1/platform-tenant-self/workflow-actions/preview", req, &out)
	return out, err
}
