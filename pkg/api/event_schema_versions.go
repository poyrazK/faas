package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
)

var eventSubscriptionVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func NormalizeEventSchemaVersions(versions []string) ([]string, error) {
	if len(versions) > EventSubscriptionSchemaVersionsMax {
		return nil, fmt.Errorf("schema_versions must contain at most %d versions", EventSubscriptionSchemaVersionsMax)
	}
	if len(versions) == 0 {
		return nil, nil
	}
	out := append([]string(nil), versions...)
	sort.Strings(out)
	for i, version := range out {
		if !eventSubscriptionVersionPattern.MatchString(version) {
			return nil, fmt.Errorf("schema_versions entries must contain 1..64 ASCII letters, digits, dots, underscores or hyphens, starting with a letter or digit")
		}
		if i > 0 && out[i-1] == version {
			return nil, fmt.Errorf("schema_versions must not contain duplicates")
		}
	}
	return out, nil
}

type EventSubscriptionSchemaVersionsRequest struct {
	SchemaVersions []string `json:"schema_versions"`
}
type EventSubscriptionSchemaVersionsResponse struct {
	SubscriptionID string   `json:"subscription_id"`
	SchemaVersions []string `json:"schema_versions"`
}

func eventSchemaVersionsPath(app, id string) string {
	return "/v1/apps/" + url.PathEscape(app) + "/event-subscriptions/" + url.PathEscape(id) + "/schema-versions"
}
func (c *Client) GetEventSubscriptionSchemaVersions(ctx context.Context, app, id string) (EventSubscriptionSchemaVersionsResponse, error) {
	var out EventSubscriptionSchemaVersionsResponse
	err := c.do(ctx, http.MethodGet, eventSchemaVersionsPath(app, id), nil, &out)
	return out, err
}
func (c *Client) SetEventSubscriptionSchemaVersions(ctx context.Context, app, id string, versions []string) (EventSubscriptionSchemaVersionsResponse, error) {
	var out EventSubscriptionSchemaVersionsResponse
	err := c.do(ctx, http.MethodPut, eventSchemaVersionsPath(app, id), EventSubscriptionSchemaVersionsRequest{SchemaVersions: append([]string{}, versions...)}, &out)
	return out, err
}
func (c *Client) ResetEventSubscriptionSchemaVersions(ctx context.Context, app, id string) (EventSubscriptionSchemaVersionsResponse, error) {
	var out EventSubscriptionSchemaVersionsResponse
	err := c.do(ctx, http.MethodDelete, eventSchemaVersionsPath(app, id), nil, &out)
	return out, err
}
