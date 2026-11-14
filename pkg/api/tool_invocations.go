package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ToolInvocationIntent contains execution consent and hashes, never arguments
// or bearer credentials. CredentialBindingSHA256 names a local variable binding or an opaque owner-granted credential binding.
type ToolInvocationIntent struct {
	Version                 string `json:"version"`
	Name                    string `json:"name"`
	ArgumentsSHA256         string `json:"arguments_sha256"`
	AccessMode              string `json:"access_mode"`
	CredentialBindingSHA256 string `json:"credential_binding_sha256,omitempty"`
	TimeoutSeconds          int    `json:"timeout_seconds"`
	AllowToolExecution      bool   `json:"allow_tool_execution"`
}

// ToolInvocationTarget is resolved once by apid from an owned publication.
type ToolInvocationTarget struct {
	App                    string `json:"app"`
	AppID                  string `json:"app_id"`
	DeploymentID           string `json:"deployment_id"`
	DeploymentSourceSHA256 string `json:"deployment_source_sha256"`
	CatalogSHA256          string `json:"catalog_sha256"`
	CatalogContractSHA256  string `json:"catalog_contract_sha256"`
	EndpointPath           string `json:"endpoint_path"`
}

type ToolInvocationOutcome struct {
	Status        string `json:"status"`
	Code          string `json:"code"`
	Result        []byte `json:"result_base64,omitempty"`
	ResultSHA256  string `json:"result_sha256,omitempty"`
	ResultBytes   int    `json:"result_bytes,omitempty"`
	ResultOmitted bool   `json:"result_omitted,omitempty"`
	Redacted      bool   `json:"redacted,omitempty"`
}

type ToolInvocationRecord struct {
	Delegation            *ToolInvocationDelegation `json:"delegation,omitempty"`
	ID                    string                    `json:"id"`
	AccountID             string                    `json:"account_id"`
	Intent                ToolInvocationIntent      `json:"intent"`
	Target                ToolInvocationTarget      `json:"target"`
	Status                string                    `json:"status"`
	CreatedAt             time.Time                 `json:"created_at"`
	Deadline              time.Time                 `json:"deadline"`
	DispatchedAt          *time.Time                `json:"dispatched_at,omitempty"`
	CancellationRequested bool                      `json:"cancellation_requested"`
	FinishedAt            *time.Time                `json:"finished_at,omitempty"`
	Outcome               *ToolInvocationOutcome    `json:"outcome,omitempty"`
}

// ToolInvocationDelegation is resolved by apid, never supplied by the caller.
// It fences receipts and dispatch to one exact grant and native connection key.
type ToolInvocationDelegation struct {
	GrantID   string `json:"grant_id"`
	KeyID     string `json:"key_id"`
	ProjectID string `json:"project_id,omitempty"`
}

type DelegatedToolInvocationIntent struct {
	GrantID string               `json:"grant_id"`
	Intent  ToolInvocationIntent `json:"intent"`
}

func (c *Client) BeginDelegatedToolInvocation(ctx context.Context, id string, in DelegatedToolInvocationIntent) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/delegated-tool-invocations/"+url.PathEscape(id), in, &out)
	return out, err
}

func (c *Client) GetDelegatedToolInvocation(ctx context.Context, id string) (ToolInvocationRecord, error) {
	var out ToolInvocationRecord
	err := c.do(ctx, http.MethodGet, "/v1/delegated-tool-invocations/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DispatchDelegatedToolInvocation(ctx context.Context, id string) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/delegated-tool-invocations/"+url.PathEscape(id)+"/dispatch", struct{}{}, &out)
	return out, err
}

func (c *Client) CancelDelegatedToolInvocation(ctx context.Context, id string) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	err := c.do(ctx, http.MethodPost, "/v1/delegated-tool-invocations/"+url.PathEscape(id)+"/cancel", struct{}{}, &out)
	return out, err
}

func (c *Client) FinishDelegatedToolInvocation(ctx context.Context, id string, in ToolInvocationOutcome) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	err := c.do(ctx, http.MethodPost, "/v1/delegated-tool-invocations/"+url.PathEscape(id)+"/outcome", in, &out)
	return out, err
}

type ToolInvocationReceipt struct {
	Created         bool                 `json:"created"`
	DispatchGranted bool                 `json:"dispatch_granted"`
	Record          ToolInvocationRecord `json:"record"`
}

func (c *Client) BeginToolInvocation(ctx context.Context, id string, in ToolInvocationIntent) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/tool-invocations/"+url.PathEscape(id), in, &out)
	return out, err
}

func (c *Client) GetToolInvocation(ctx context.Context, id string) (ToolInvocationRecord, error) {
	var out ToolInvocationRecord
	err := c.do(ctx, http.MethodGet, "/v1/tool-invocations/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) DispatchToolInvocation(ctx context.Context, id string) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	// A replayed HTTP receipt must never replay a dispatch grant.
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/tool-invocations/"+url.PathEscape(id)+"/dispatch", struct{}{}, &out)
	return out, err
}

func (c *Client) CancelToolInvocation(ctx context.Context, id string) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	err := c.do(ctx, http.MethodPost, "/v1/tool-invocations/"+url.PathEscape(id)+"/cancel", struct{}{}, &out)
	return out, err
}

func (c *Client) FinishToolInvocation(ctx context.Context, id string, in ToolInvocationOutcome) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	err := c.do(ctx, http.MethodPost, "/v1/tool-invocations/"+url.PathEscape(id)+"/outcome", in, &out)
	return out, err
}

// PrivateToolInvocationRequest carries transient arguments, never a bearer token.
type PrivateToolInvocationRequest struct {
	GrantID       string               `json:"grant_id"`
	Intent        ToolInvocationIntent `json:"intent"`
	ArgumentsJSON string               `json:"arguments_json"`
}

func (c *Client) ExecutePrivateToolInvocation(ctx context.Context, id string, in PrivateToolInvocationRequest) (ToolInvocationReceipt, error) {
	var out ToolInvocationReceipt
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/delegated-tool-invocations/"+url.PathEscape(id)+"/execute", in, &out)
	return out, err
}
