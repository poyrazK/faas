package api

import "context"

// ListPrivateNetworkMembers returns the stable address reservations and
// allocatable capacity for one Gregale-owned private network.
func (c *Client) ListPrivateNetworkMembers(ctx context.Context, id string) (PrivateNetworkMembersResponse, error) {
	var out PrivateNetworkMembersResponse
	return out, c.do(ctx, "GET", "/v1/networks/"+id+"/members", nil, &out)
}
