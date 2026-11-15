package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ToolPublicationAuth is the source-reviewed OAuth contract. It contains no
// credential or credential reference. Test authority is approved separately.
type ToolPublicationAuth struct {
	Issuer   string   `json:"issuer"`
	JWKSURL  string   `json:"jwks_url"`
	Resource string   `json:"resource"`
	Scopes   []string `json:"scopes"`
}

type ToolPublicationVerificationRequest struct {
	DeploymentID  string `json:"deployment_id"`
	CatalogSHA256 string `json:"catalog_sha256"`
	FixturesJSON  string `json:"fixtures_json"`
}

// The request digest binds the native deployment/source, publication review and
// publishing key. The owner approves this digest, never a mutable app alias.
type ToolPublicationVerification struct {
	RequestSHA256          string                              `json:"request_sha256"`
	PublicationID          string                              `json:"publication_id"`
	KeyID                  string                              `json:"key_id"`
	AppID                  string                              `json:"app_id"`
	DeploymentID           string                              `json:"deployment_id"`
	DeploymentSourceSHA256 string                              `json:"deployment_source_sha256"`
	CatalogSHA256          string                              `json:"catalog_sha256"`
	Status                 string                              `json:"status"`
	CreatedAt              time.Time                           `json:"created_at"`
	Deadline               time.Time                           `json:"deadline"`
	Outcome                *ToolPublicationVerificationOutcome `json:"outcome,omitempty"`
}

type ToolPublicationVerificationOutcome struct {
	OK               bool                   `json:"ok"`
	ProtocolVerified bool                   `json:"protocol_verified"`
	Catalog          []byte                 `json:"catalog_base64,omitempty"`
	Checks           []ToolPublicationCheck `json:"checks"`
	Code             string                 `json:"code,omitempty"`
}

// Only an unscoped personal administrator may choose a test credential. This
// reference is never part of a publisher-facing record or worker response.
type ToolPublicationVerificationDecision struct {
	RequestSHA256       string `json:"request_sha256"`
	CredentialSecretRef string `json:"credential_secret_ref,omitempty"`
	Revoke              bool   `json:"revoke,omitempty"`
}

func (c *Client) VerifyToolPublication(ctx context.Context, id string, in ToolPublicationVerificationRequest) (ToolPublicationVerification, error) {
	var out ToolPublicationVerification
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/tool-publications/"+url.PathEscape(id)+"/verification", in, &out)
	return out, err
}

func (c *Client) DecideToolPublicationVerification(ctx context.Context, id string, in ToolPublicationVerificationDecision) (ToolPublicationVerification, error) {
	var out ToolPublicationVerification
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/tool-publications/"+url.PathEscape(id)+"/verification", in, &out)
	return out, err
}
