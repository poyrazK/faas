package api

import "context"

// ListPrivateNetworkPeerings returns the account-scoped peering intents that
// involve the given Gregale-owned network.
func (c *Client) ListPrivateNetworkPeerings(ctx context.Context, id string) (PrivateNetworkPeeringListResponse, error) {
	var out PrivateNetworkPeeringListResponse
	return out, c.do(ctx, "GET", "/v1/networks/"+id+"/peerings", nil, &out)
}

// CreatePrivateNetworkPeering requests a symmetric, provider-neutral peering.
// The response is pending until the fabric reconciler marks the route domains
// ready; pending and error states remain fail-closed.
func (c *Client) CreatePrivateNetworkPeering(ctx context.Context, id string, req CreatePrivateNetworkPeeringRequest) (PrivateNetworkPeering, error) {
	var out PrivateNetworkPeering
	return out, c.do(ctx, "POST", "/v1/networks/"+id+"/peerings", req, &out)
}

// GetPrivateNetworkPeering reads one peering intent from either network side.
func (c *Client) GetPrivateNetworkPeering(ctx context.Context, id, peerID string) (PrivateNetworkPeering, error) {
	var out PrivateNetworkPeering
	return out, c.do(ctx, "GET", "/v1/networks/"+id+"/peerings/"+peerID, nil, &out)
}

// DeletePrivateNetworkPeering removes a peering intent and releases the
// deletion guard on both referenced network route domains.
func (c *Client) DeletePrivateNetworkPeering(ctx context.Context, id, peerID string) error {
	return c.do(ctx, "DELETE", "/v1/networks/"+id+"/peerings/"+peerID, nil, nil)
}
