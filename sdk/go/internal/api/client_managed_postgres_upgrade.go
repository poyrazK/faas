package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) UpgradeManagedPostgresDatabase(ctx context.Context, id string, request UpgradeManagedPostgresDatabaseRequest) (ManagedPostgresMajorUpgrade, error) {
	var out ManagedPostgresMajorUpgrade
	err := c.do(ctx, http.MethodPost, "/v1/postgres/databases/"+url.PathEscape(id)+"/upgrade", request, &out)
	return out, err
}

func (c *Client) GetManagedPostgresMajorUpgrade(ctx context.Context, databaseID, upgradeID string) (ManagedPostgresMajorUpgrade, error) {
	var out ManagedPostgresMajorUpgrade
	err := c.do(ctx, http.MethodGet, "/v1/postgres/databases/"+url.PathEscape(databaseID)+"/upgrades/"+url.PathEscape(upgradeID), nil, &out)
	return out, err
}
