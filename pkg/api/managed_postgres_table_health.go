package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// ManagedPostgresTableHealth reports a bounded sample from a native primary.
type ManagedPostgresTableHealth struct {
	DatabaseID string `json:"database_id"`
	dbguest.TableHealth
}

func (c *Client) GetManagedPostgresTableHealth(ctx context.Context, id string, options dbguest.TableHealthOptions) (ManagedPostgresTableHealth, error) {
	return c.getManagedPostgresTableHealth(ctx, "/v1/postgres", id, options)
}

func (c *Client) GetOrgManagedPostgresTableHealth(ctx context.Context, organization, id string, options dbguest.TableHealthOptions) (ManagedPostgresTableHealth, error) {
	return c.getManagedPostgresTableHealth(ctx, orgPostgresPath(organization), id, options)
}

func (c *Client) getManagedPostgresTableHealth(ctx context.Context, prefix, id string, options dbguest.TableHealthOptions) (ManagedPostgresTableHealth, error) {
	var out ManagedPostgresTableHealth
	if err := options.Validate(); err != nil {
		return out, err
	}
	query := url.Values{"sort": {options.Sort}, "limit": {strconv.Itoa(options.Limit)}}
	err := c.do(ctx, http.MethodGet, prefix+"/databases/"+url.PathEscape(id)+"/tables?"+query.Encode(), nil, &out)
	return out, err
}
