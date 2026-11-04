// adr: 20261103222927
package api

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// AppResourceInventoryBinding exposes linkage metadata, never connection or credential material.
type AppResourceInventoryBinding struct {
	ID              string `json:"id"`
	DatabaseID      string `json:"database_id"`
	AppID           string `json:"app_id"`
	Scope           string `json:"scope"`
	Access          string `json:"access"`
	State           string `json:"state"`
	EnvironmentKey  string `json:"environment_key"`
	RotationPending bool   `json:"rotation_pending"`
}

// AppResourceInventoryItem contains the reviewed metadata fields for its page's resource kind.
// Provider identifiers, access keys, credentials, secret references and raw errors are absent.
type AppResourceInventoryItem struct {
	ID              string                       `json:"id"`
	AppID           string                       `json:"app_id"`
	Scope           string                       `json:"scope"`
	Name            string                       `json:"name,omitempty"`
	Domain          string                       `json:"domain,omitempty"`
	Verified        *bool                        `json:"verified,omitempty"`
	CertStatus      string                       `json:"cert_status,omitempty"`
	Schedule        string                       `json:"schedule,omitempty"`
	Path            string                       `json:"path,omitempty"`
	Timezone        string                       `json:"timezone,omitempty"`
	Enabled         *bool                        `json:"enabled,omitempty"`
	Region          string                       `json:"region,omitempty"`
	State           string                       `json:"state,omitempty"`
	PostgresMajor   int                          `json:"postgres_major,omitempty"`
	BucketID        string                       `json:"bucket_id,omitempty"`
	Bucket          string                       `json:"bucket,omitempty"`
	Prefix          string                       `json:"prefix,omitempty"`
	RotationPending *bool                        `json:"rotation_pending,omitempty"`
	Binding         *AppResourceInventoryBinding `json:"binding,omitempty"`
}

// AppResourceInventoryResponse is one backend-filtered metadata page, not a runtime readiness claim.
type AppResourceInventoryResponse struct {
	SchemaVersion int                        `json:"schema_version"`
	AppID         string                     `json:"app_id"`
	Scope         string                     `json:"scope"`
	ProjectID     string                     `json:"project_id,omitempty"`
	EnvironmentID string                     `json:"environment_id,omitempty"`
	Kind          string                     `json:"kind"`
	Limit         int                        `json:"limit"`
	Offset        int                        `json:"offset"`
	Total         int64                      `json:"total"`
	Items         []AppResourceInventoryItem `json:"items"`
	HasMore       bool                       `json:"has_more"`
	NextOffset    *int                       `json:"next_offset,omitempty"`
	GeneratedAt   time.Time                  `json:"generated_at"`
}

func ValidAppResourceKind(kind string) bool {
	switch kind {
	case "domains", "crons", "buckets", "storage_bindings", "databases":
		return true
	}
	return false
}

func (c *Client) GetAppResourceInventory(ctx context.Context, slug, kind, environment string, limit, offset int) (AppResourceInventoryResponse, error) {
	q := url.Values{"kind": {kind}, "offset": {strconv.Itoa(offset)}}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if environment != "" {
		q.Set("environment", environment)
	}
	var out AppResourceInventoryResponse
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/resource-inventory?"+q.Encode(), nil, &out)
	return out, err
}
