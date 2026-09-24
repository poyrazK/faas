package api

import (
	"context"
	"encoding/hex"
	"net/url"
	"strconv"
	"time"
)

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
