package api

// adr: 774
import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
)

type PutGlobalRuntimeConsentRequest struct {
	Region         string    `json:"region"`
	ExpiresAt      time.Time `json:"expires_at"`
	RegionalAPIKey string    `json:"regional_api_key"`
}
type RevokeGlobalRuntimeConsentRequest struct {
	RegionalAPIKey string `json:"regional_api_key"`
}
type GlobalRuntimeConsentBindingResponse struct {
	CellID            string `json:"cell_id"`
	Region            string `json:"region"`
	CellGeneration    int64  `json:"cell_generation"`
	RegionalAccountID string `json:"regional_account_id"`
	RegionalAppID     string `json:"regional_app_id"`
	RegionalAppSlug   string `json:"regional_app_slug"`
	APIBase           string `json:"api_base"`
}
type GlobalRuntimeConsentResponse struct {
	ID                string                              `json:"id"`
	IssuerID          string                              `json:"issuer_id"`
	GlobalAppID       string                              `json:"global_app_id"`
	OwnerID           string                              `json:"owner_id"`
	Slug              string                              `json:"global_app_slug"`
	Binding           GlobalRuntimeConsentBindingResponse `json:"binding"`
	ExpiresAt         time.Time                           `json:"expires_at"`
	CreatedAt         time.Time                           `json:"created_at"`
	Status            string                              `json:"status"`
	RegionalCreatedAt *time.Time                          `json:"regional_created_at,omitempty"`
	RegionalRevokedAt *time.Time                          `json:"regional_revoked_at,omitempty"`
}

func globalRuntimeConsentPath(slug, id string) (string, error) {
	u, err := uuid.Parse(id)
	if !ValidGlobalAppSlug(slug) || err != nil || u == uuid.Nil || u.String() != id {
		return "", fmt.Errorf("a canonical global slug and consent UUID are required")
	}
	return "/v1/global-apps/" + url.PathEscape(slug) + "/runtime-consents/" + id, nil
}
func (c *Client) PutGlobalRuntimeConsent(ctx context.Context, slug, id string, req PutGlobalRuntimeConsentRequest) (GlobalRuntimeConsentResponse, error) {
	var out GlobalRuntimeConsentResponse
	p, err := globalRuntimeConsentPath(slug, id)
	if err != nil {
		return out, err
	}
	err = c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "PUT", p, req, &out, "")
	return out, err
}
func (c *Client) GetGlobalRuntimeConsent(ctx context.Context, slug, id string) (GlobalRuntimeConsentResponse, error) {
	var out GlobalRuntimeConsentResponse
	p, err := globalRuntimeConsentPath(slug, id)
	if err != nil {
		return out, err
	}
	err = c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "GET", p, nil, &out, "")
	return out, err
}
func (c *Client) RevokeGlobalRuntimeConsent(ctx context.Context, slug, id string, req RevokeGlobalRuntimeConsentRequest) (GlobalRuntimeConsentResponse, error) {
	var out GlobalRuntimeConsentResponse
	p, err := globalRuntimeConsentPath(slug, id)
	if err != nil {
		return out, err
	}
	err = c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "DELETE", p, req, &out, "")
	return out, err
}
