package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ToolGrantIntent grants one existing agent connection access to one immutable
// publication/tool. AllowInvocation is a ceiling, not a dispatch permission.
type ToolGrantIntent struct {
	KeyID                  string    `json:"key_id"`
	Version                string    `json:"version"`
	Name                   string    `json:"name"`
	ExpiresAt              time.Time `json:"expires_at"`
	AllowInvocation        bool      `json:"allow_invocation"`
	CredentialSecretRef    string    `json:"credential_secret_ref,omitempty"`
	CredentialConnectionID string    `json:"credential_connection_id,omitempty"`
}

// ToolGrantCredential binds sealed source identity, never a credential value.
type ToolGrantCredential struct {
	ConnectionID   string   `json:"connection_id,omitempty"`
	SHA256         string   `json:"sha256"`
	SourceSHA256   string   `json:"source_sha256"`
	Scope          string   `json:"scope"`
	RequiredScopes []string `json:"required_scopes"`
}

type ToolGrantRecord struct {
	Credential *ToolGrantCredential `json:"credential,omitempty"`
	ID         string               `json:"id"`
	AccountID  string               `json:"account_id"`
	App        string               `json:"app"`
	AppID      string               `json:"app_id"`
	ProjectID  string               `json:"project_id,omitempty"`
	Intent     ToolGrantIntent      `json:"intent"`
	CreatedAt  time.Time            `json:"created_at"`
	RevokedAt  *time.Time           `json:"revoked_at,omitempty"`
}

type ToolGrantPage struct {
	Grants     []ToolGrantRecord `json:"grants"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// ToolRegistryTool contains a canonical catalog projected to exactly one tool.
// The projected digest and original publication digest are intentionally distinct.
type ToolRegistryTool struct {
	Grants                         []ToolGrantAccess `json:"grants,omitempty"`
	Version                        string            `json:"version"`
	AccountID                      string            `json:"account_id"`
	App                            string            `json:"app"`
	AppID                          string            `json:"app_id"`
	Name                           string            `json:"name"`
	DeploymentID                   string            `json:"deployment_id"`
	SourceSHA256                   string            `json:"source_sha256"`
	DeploymentSourceSHA256         string            `json:"deployment_source_sha256"`
	CatalogSHA256                  string            `json:"catalog_sha256"`
	CatalogContractSHA256          string            `json:"catalog_contract_sha256"`
	PublishedCatalogContractSHA256 string            `json:"published_catalog_contract_sha256"`
	Catalog                        []byte            `json:"catalog_base64"`
	AccessMode                     string            `json:"access_mode"`
	RequiredScopes                 []string          `json:"required_scopes"`
	EvidenceOrigin                 string            `json:"evidence_origin"`
	Availability                   string            `json:"availability"`
}

type ToolGrantAccess struct {
	CredentialBindingSHA256 string    `json:"credential_binding_sha256,omitempty"`
	GrantID                 string    `json:"grant_id"`
	ExpiresAt               time.Time `json:"expires_at"`
	AllowInvocation         bool      `json:"allow_invocation"`
}

func (c *Client) PutToolGrant(ctx context.Context, id string, in ToolGrantIntent) (ToolGrantRecord, error) {
	var out ToolGrantRecord
	err := c.do(ctx, http.MethodPut, "/v1/tool-grants/"+url.PathEscape(id), in, &out)
	return out, err
}

func (c *Client) GetToolGrant(ctx context.Context, id string) (ToolGrantRecord, error) {
	var out ToolGrantRecord
	err := c.do(ctx, http.MethodGet, "/v1/tool-grants/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) ListToolGrants(ctx context.Context, cursor string, limit int) (ToolGrantPage, error) {
	var out ToolGrantPage
	q := url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(limit)}}
	err := c.do(ctx, http.MethodGet, "/v1/tool-grants?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) RevokeToolGrant(ctx context.Context, id string) (ToolGrantRecord, error) {
	var out ToolGrantRecord
	err := c.do(ctx, http.MethodDelete, "/v1/tool-grants/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) GetRegistryTool(ctx context.Context, version, name string) (ToolRegistryTool, error) {
	var out ToolRegistryTool
	err := c.do(ctx, http.MethodGet, "/v1/tool-registry/"+url.PathEscape(version)+"/tools/"+url.PathEscape(name), nil, &out)
	return out, err
}
