package api

// adr: 657
import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

// These types preserve the original ADR-644 capitalized binding wire spelling.
// Signed receipt storage is independent of native execution authority.
type RegionalRuntimeQuotaBinding struct {
	CellID            string `json:"CellID"`
	Region            string `json:"Region"`
	CellGeneration    int64  `json:"CellGeneration"`
	RegionalAccountID string `json:"RegionalAccountID"`
	RegionalOrgID     string `json:"RegionalOrgID"`
	RegionalAppID     string `json:"RegionalAppID"`
	RegionalAppSlug   string `json:"RegionalAppSlug"`
	APIBase           string `json:"APIBase"`
}

type RegionalRuntimeQuotaEnvelope struct {
	KeyID     string `json:"key_id"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type RegionalRuntimeQuotaDelivery struct {
	Binding   RegionalRuntimeQuotaBinding  `json:"binding"`
	Authority RegionalRuntimeQuotaEnvelope `json:"authority"`
	ConsentID string                       `json:"consent_id,omitempty"`
}

type RegionalRuntimeQuotaDeliveryReceipt struct {
	Grant      *RegionalRuntimeQuotaEnvelope `json:"grant"`
	Retirement *RegionalRuntimeQuotaEnvelope `json:"retirement"`
}

type DeliverGlobalRuntimeQuotaRequest struct {
	RegionalAPIKey string `json:"regional_api_key,omitempty"`
	ConsentID      string `json:"runtime_consent_id,omitempty"`
}

func (c *Client) DeliverGlobalRuntimeQuota(ctx context.Context, slug, allocation, region string, req DeliverGlobalRuntimeQuotaRequest) (RegionalRuntimeQuotaDeliveryReceipt, error) {
	var out RegionalRuntimeQuotaDeliveryReceipt
	path, err := globalRuntimeQuotaPath(slug, allocation)
	if err != nil {
		return out, err
	}
	if region == "" || !ValidComputeRegion(region) {
		return out, fmt.Errorf("a canonical selected region is required")
	}
	err = c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "POST", path+"/deliver/"+url.PathEscape(region), req, &out, "")
	return out, err
}

func regionalQuotaDeliveryPath(cell, app, allocation string) (string, error) {
	for _, id := range []string{cell, app, allocation} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return "", fmt.Errorf("canonical nonzero cell, app and allocation UUIDs are required")
		}
	}
	return "/v1/cells/" + cell + "/apps/" + app + "/runtime-quotas/" + allocation, nil
}

func (c *Client) AcceptCellRuntimeQuota(ctx context.Context, cell, app, allocation string, req RegionalRuntimeQuotaDelivery) (RegionalRuntimeQuotaDeliveryReceipt, error) {
	return c.deliverCellRuntimeQuota(ctx, cell, app, allocation, req, false)
}

func (c *Client) RetireCellRuntimeQuota(ctx context.Context, cell, app, allocation string, req RegionalRuntimeQuotaDelivery) (RegionalRuntimeQuotaDeliveryReceipt, error) {
	return c.deliverCellRuntimeQuota(ctx, cell, app, allocation, req, true)
}

func (c *Client) deliverCellRuntimeQuota(ctx context.Context, cell, app, allocation string, req RegionalRuntimeQuotaDelivery, retirement bool) (RegionalRuntimeQuotaDeliveryReceipt, error) {
	var out RegionalRuntimeQuotaDeliveryReceipt
	path, err := regionalQuotaDeliveryPath(cell, app, allocation)
	if err != nil {
		return out, err
	}
	if retirement {
		path += "/retirement"
	}
	err = c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "PUT", path, req, &out, allocation)
	return out, err
}
