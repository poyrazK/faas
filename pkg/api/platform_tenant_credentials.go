package api

import (
	"context"
	"encoding/hex"
	"net/url"
	"strconv"
	"time"
)

const (
	MaxPlatformTenantCredentialScopes = 3
	MaxPlatformTenantKeysPerConsumer  = 100
)

type SetPlatformTenantCredentialPolicyRequest struct {
	AllowedScopes      []string `json:"allowed_scopes"`
	MaxKeysPerConsumer *int     `json:"max_keys_per_consumer"`
}

type PlatformTenantCredentialPolicyResponse struct {
	TenantID           string     `json:"tenant_id"`
	Enabled            bool       `json:"enabled"`
	AllowedScopes      []string   `json:"allowed_scopes"`
	MaxKeysPerConsumer int        `json:"max_keys_per_consumer"`
	UpdatedAt          *time.Time `json:"updated_at,omitempty"`
}

// PlatformTenantSelfConsumerResponse omits app IDs and account-owned details;
// the tenant only needs its stable consumer ID to manage credentials.
type PlatformTenantSelfConsumerResponse struct {
	ConsumerID  string `json:"consumer_id"`
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
	Status      string `json:"status"`
}

type PlatformTenantSelfConsumersResponse struct {
	Consumers []PlatformTenantSelfConsumerResponse `json:"consumers"`
}

// CreatePlatformTenantSelfConsumerRequest creates an identity on a surface
// already linked to the authenticated tenant. App and tenant IDs are derived
// server-side and cannot be selected by the caller.
type CreatePlatformTenantSelfConsumerRequest struct {
	SurfaceID   string `json:"surface_id"`
	ExternalRef string `json:"external_ref"`
	Name        string `json:"name"`
}

// PlatformTenantCredentialIntent contains only public key metadata and the
// SHA-256 digest of a client-generated key. Never send the plaintext key.
type PlatformTenantCredentialIntent struct {
	ConsumerID string     `json:"consumer_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Hash       string     `json:"hash"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type ApplyPlatformTenantCredentialsRequest struct {
	DryRun       bool                             `json:"dry_run,omitempty"`
	Keys         []PlatformTenantCredentialIntent `json:"keys,omitempty"`
	RevokeKeyIDs []string                         `json:"revoke_key_ids,omitempty"`
}

type PlatformTenantCredentialResult struct {
	PlatformTenantCredentialMetadata
	Action string `json:"action"`
}

// PlatformTenantCredentialMetadata cannot carry plaintext or a hash by type.
type PlatformTenantCredentialMetadata struct {
	ID         string     `json:"id,omitempty"`
	ConsumerID string     `json:"consumer_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type ApplyPlatformTenantCredentialsResponse struct {
	TenantID string                           `json:"tenant_id"`
	DryRun   bool                             `json:"dry_run"`
	Keys     []PlatformTenantCredentialResult `json:"keys"`
}

type PlatformTenantCredentialsResponse struct {
	Keys []PlatformTenantCredentialMetadata `json:"keys"`
}

// PreparePlatformTenantCredential generates a credential locally. Save the
// returned plaintext in a secure store before submitting the intent; the
// server cannot recover it, including after a lost response.
func PreparePlatformTenantCredential(consumerID, name string, scopes []string, expiresAt *time.Time) (PlatformTenantCredentialIntent, string, error) {
	plaintext, prefix, hash, err := GenerateConsumerKey()
	if err != nil {
		return PlatformTenantCredentialIntent{}, "", err
	}
	return PlatformTenantCredentialIntent{ConsumerID: consumerID, Name: name, Prefix: prefix,
		Hash: hex.EncodeToString(hash), Scopes: append([]string(nil), scopes...), ExpiresAt: expiresAt}, plaintext, nil
}

func (c *Client) ApplyPlatformTenantCredentials(ctx context.Context, tenantID string, req ApplyPlatformTenantCredentialsRequest) (ApplyPlatformTenantCredentialsResponse, error) {
	var out ApplyPlatformTenantCredentialsResponse
	err := c.do(ctx, "POST", "/v1/account/platform-tenants/"+url.PathEscape(tenantID)+"/credentials/apply", req, &out)
	return out, err
}

func (c *Client) ListPlatformTenantCredentials(ctx context.Context, tenantID string, limit, offset int) (PlatformTenantCredentialsResponse, error) {
	var out PlatformTenantCredentialsResponse
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/credentials"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) GetPlatformTenantCredentialPolicy(ctx context.Context, tenantID string) (PlatformTenantCredentialPolicyResponse, error) {
	var out PlatformTenantCredentialPolicyResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/credential-policy"
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) SetPlatformTenantCredentialPolicy(ctx context.Context, tenantID string, req SetPlatformTenantCredentialPolicyRequest) (PlatformTenantCredentialPolicyResponse, error) {
	var out PlatformTenantCredentialPolicyResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/credential-policy"
	return out, c.do(ctx, "PUT", path, req, &out)
}

func (c *Client) ListPlatformTenantSelfConsumers(ctx context.Context) (PlatformTenantSelfConsumersResponse, error) {
	var out PlatformTenantSelfConsumersResponse
	return out, c.do(ctx, "GET", "/v1/platform-tenant-self/consumers", nil, &out)
}

func (c *Client) CreatePlatformTenantSelfConsumer(ctx context.Context, req CreatePlatformTenantSelfConsumerRequest) (PlatformTenantSelfConsumerResponse, error) {
	var out PlatformTenantSelfConsumerResponse
	return out, c.do(ctx, "POST", "/v1/platform-tenant-self/consumers", req, &out)
}

func (c *Client) ListPlatformTenantSelfCredentials(ctx context.Context, limit, offset int) (PlatformTenantCredentialsResponse, error) {
	var out PlatformTenantCredentialsResponse
	path := "/v1/platform-tenant-self/credentials"
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) ApplyPlatformTenantSelfCredentials(ctx context.Context, req ApplyPlatformTenantCredentialsRequest) (ApplyPlatformTenantCredentialsResponse, error) {
	var out ApplyPlatformTenantCredentialsResponse
	return out, c.do(ctx, "POST", "/v1/platform-tenant-self/credentials/apply", req, &out)
}
