package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type ManagedPostgresExtensionVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Default bool   `json:"default,omitempty"`
}

type ManagedPostgresExtensions struct {
	Installed              []ManagedPostgresExtensionVersion `json:"installed"`
	Available              []ManagedPostgresExtensionVersion `json:"available"`
	IncompatibleExtensions []ManagedPostgresExtensionVersion `json:"incompatible_extensions,omitempty"`
}

func (c *Client) ListManagedPostgresExtensions(ctx context.Context, organization, id string, targetMajor int) (ManagedPostgresExtensions, error) {
	path := managedPostgresExtensionsPath(organization, id)
	if targetMajor > 0 {
		path += "?target_major=" + strconv.Itoa(targetMajor)
	}
	var out ManagedPostgresExtensions
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) EnableManagedPostgresExtension(ctx context.Context, organization, id, name string) (ManagedPostgresExtensions, error) {
	var out ManagedPostgresExtensions
	err := c.do(ctx, http.MethodPut, managedPostgresExtensionsPath(organization, id)+"/"+url.PathEscape(name), nil, &out)
	return out, err
}

func managedPostgresExtensionsPath(organization, id string) string {
	prefix := "/v1/postgres"
	if organization != "" {
		prefix = orgPostgresPath(organization)
	}
	return prefix + "/databases/" + url.PathEscape(id) + "/extensions"
}
