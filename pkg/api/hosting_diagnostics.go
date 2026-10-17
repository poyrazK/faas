package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// HostingDiagnostics contains only bounded identifiers and classified evidence.
// It never contains manifests, source labels, URLs, provider payloads or tokens.
type HostingDiagnostics struct {
	SiteID              string                   `json:"site_id"`
	RunID               string                   `json:"run_id,omitempty"`
	ReleaseID           string                   `json:"release_id,omitempty"`
	PreviewID           string                   `json:"preview_id,omitempty"`
	ManifestFingerprint string                   `json:"manifest_fingerprint,omitempty"`
	StartedAt           time.Time                `json:"started_at"`
	ObservedAt          time.Time                `json:"observed_at"`
	ServingHealth       string                   `json:"serving_health"`
	RetryAdvice         string                   `json:"retry_advice"`
	Checks              []HostingDiagnosticCheck `json:"checks"`
}

type HostingDiagnosticCheck struct {
	Step                string     `json:"step"`
	State               string     `json:"state"`
	Code                string     `json:"code"`
	Summary             string     `json:"summary"`
	Action              string     `json:"action"`
	Generation          int64      `json:"generation,omitempty"`
	PublishedGeneration int64      `json:"published_generation,omitempty"`
	NextAttemptAt       *time.Time `json:"next_attempt_at,omitempty"`
}

func (c *Client) GetHostingDiagnostics(ctx context.Context, site, run string) (HostingDiagnostics, error) {
	var out HostingDiagnostics
	path := hostingSitePath(site) + "/diagnostics"
	if run != "" {
		path += "?run_id=" + url.QueryEscape(run)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
