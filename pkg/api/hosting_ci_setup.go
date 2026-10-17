package api

import (
	"context"
	"net/http"
	"time"
)

type HostingCISetupConfig struct {
	APIBase            string `json:"api_base"`
	ActionCommit       string `json:"action_commit"`
	CheckoutCommit     string `json:"checkout_commit"`
	PrepareAction      string `json:"prepare_action"`
	CLIPath            string `json:"cli_path"`
	CLISHA256          string `json:"cli_sha256"`
	OutputPath         string `json:"output_path"`
	ReservePreview     bool   `json:"reserve_preview,omitempty"`
	SPAFallback        bool   `json:"spa_fallback"`
	PreviewTTLSeconds  int64  `json:"preview_ttl_seconds"`
	WaitTimeoutSeconds int64  `json:"wait_timeout_seconds"`
	Cron               string `json:"cron,omitempty"`
	DefaultBranch      string `json:"default_branch,omitempty"`
}
type HostingCISetupRequest struct {
	TrustID string               `json:"trust_id"`
	Config  HostingCISetupConfig `json:"config"`
}
type HostingCISetupCheck struct {
	Code    string `json:"code"`
	State   string `json:"state"`
	Message string `json:"message"`
}
type HostingCISetup struct {
	SiteID             string                `json:"site_id"`
	TrustID            string                `json:"trust_id"`
	Repository         string                `json:"repository"`
	WorkflowPath       string                `json:"workflow_path"`
	ConfigurationValid bool                  `json:"configuration_valid"`
	WorkflowYAML       string                `json:"workflow_yaml,omitempty"`
	ObservedAt         time.Time             `json:"observed_at"`
	Checks             []HostingCISetupCheck `json:"checks"`
	Recovery           []string              `json:"recovery"`
}

func (c *Client) PrepareHostingCISetup(ctx context.Context, site string, req HostingCISetupRequest) (HostingCISetup, error) {
	var out HostingCISetup
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, hostingSitePath(site)+"/ci-setup", req, &out)
	return out, err
}
