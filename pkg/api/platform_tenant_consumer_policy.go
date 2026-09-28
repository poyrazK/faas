package api

import (
	"context"
	"net/url"
	"time"
)

const MaxPlatformTenantSelfServiceConsumers = 100000

type SetPlatformTenantConsumerProvisioningPolicyRequest struct {
	Enabled      *bool `json:"enabled"`
	MaxConsumers *int  `json:"max_consumers"`
}

type PlatformTenantConsumerProvisioningPolicyResponse struct {
	TenantID     string     `json:"tenant_id"`
	Enabled      bool       `json:"enabled"`
	MaxConsumers int        `json:"max_consumers"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

func (c *Client) GetPlatformTenantConsumerProvisioningPolicy(ctx context.Context, tenantID string) (PlatformTenantConsumerProvisioningPolicyResponse, error) {
	var out PlatformTenantConsumerProvisioningPolicyResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/consumer-provisioning-policy"
	return out, c.do(ctx, "GET", path, nil, &out)
}

func (c *Client) SetPlatformTenantConsumerProvisioningPolicy(ctx context.Context, tenantID string, req SetPlatformTenantConsumerProvisioningPolicyRequest) (PlatformTenantConsumerProvisioningPolicyResponse, error) {
	var out PlatformTenantConsumerProvisioningPolicyResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/consumer-provisioning-policy"
	return out, c.do(ctx, "PUT", path, req, &out)
}
