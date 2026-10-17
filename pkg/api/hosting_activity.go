package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// Activity is stored CI intent and observations, never GitHub job or serving health.
// Source labels are optional caller assertions from a retained release.
type HostingActivityEntry struct {
	RunID            string                    `json:"run_id"`
	ReleaseID        string                    `json:"release_id"`
	PreviewID        string                    `json:"preview_id"`
	CreatedAt        time.Time                 `json:"created_at"`
	PreviewExpiresAt time.Time                 `json:"preview_expires_at"`
	Canceled         bool                      `json:"canceled,omitempty"`
	CanCancel        bool                      `json:"can_cancel,omitempty"`
	State            string                    `json:"state"`
	Provenance       *HostingReleaseProvenance `json:"provenance,omitempty"`
	Checks           []HostingDiagnosticCheck  `json:"checks"`
}
type HostingActivityPage struct {
	SiteID               string                 `json:"site_id"`
	Entries              []HostingActivityEntry `json:"entries"`
	NextCursor           string                 `json:"next_cursor,omitempty"`
	ObservedAt           time.Time              `json:"observed_at"`
	ServingHealth        string                 `json:"serving_health"`
	SourceLabelsIncluded bool                   `json:"source_labels_included"`
}

func (c *Client) ListHostingActivity(ctx context.Context, site, before string) (HostingActivityPage, error) {
	var out HostingActivityPage
	path := hostingSitePath(site) + "/runs"
	if before != "" {
		path += "?before=" + url.QueryEscape(before)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
