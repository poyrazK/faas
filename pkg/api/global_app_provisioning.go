package api

// adr: 797
import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Credentials are request-only and excluded from both intent and receipts.
type GlobalAppProvisionTargetRequest struct {
	Region            string           `json:"region"`
	RegionalAccountID string           `json:"regional_account_id"`
	RegionalAPIKey    string           `json:"regional_api_key"`
	App               CreateAppRequest `json:"app"`
}

type PutGlobalAppProvisioningRequest struct {
	Targets []GlobalAppProvisionTargetRequest `json:"targets"`
}

type GlobalAppProvisionTargetResponse struct {
	Region      string                   `json:"region"`
	ProvisionID string                   `json:"provision_id"`
	Status      string                   `json:"status"`
	Diagnostic  string                   `json:"diagnostic,omitempty"`
	Target      *GlobalAppTargetResponse `json:"target,omitempty"`
}

type CellAppProvisionRequest struct {
	GlobalAppID string           `json:"global_app_id"`
	App         CreateAppRequest `json:"app"`
}

type CellAppProvisionResponse struct {
	ProvisionID string                 `json:"provision_id"`
	GlobalAppID string                 `json:"global_app_id"`
	Binding     CellAppBindingResponse `json:"binding"`
	Request     CreateAppRequest       `json:"request"`
	CreatedAt   time.Time              `json:"created_at"`
}

type CellAccountBindingResponse struct {
	CellID    string `json:"cell_id"`
	Region    string `json:"region"`
	AccountID string `json:"account_id"`
}

func (c *Client) PutGlobalAppProvisioning(ctx context.Context, slug string, req PutGlobalAppProvisioningRequest) ([]GlobalAppProvisionTargetResponse, error) {
	var out []GlobalAppProvisionTargetResponse
	client := *c.http
	if client.Timeout < GlobalImageReleaseRequestTimeout+GlobalImageReleaseRegionalTimeout {
		client.Timeout = GlobalImageReleaseRequestTimeout + GlobalImageReleaseRegionalTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	err := c.doWithClientAndIdempotencyKey(ctx, &client, http.MethodPut, "/v1/global-apps/"+url.PathEscape(slug)+"/provisioning", req, &out, "")
	return out, err
}

func (c *Client) GetGlobalAppProvisioning(ctx context.Context, slug string) ([]GlobalAppProvisionTargetResponse, error) {
	var out []GlobalAppProvisionTargetResponse
	err := c.do(ctx, http.MethodGet, "/v1/global-apps/"+url.PathEscape(slug)+"/provisioning", nil, &out)
	return out, err
}
