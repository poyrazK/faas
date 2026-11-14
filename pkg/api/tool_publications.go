package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ToolPublicationIntent is the reviewed, credential-free publication input.
// Local source paths and token environment references never enter this record.
type ToolPublicationIntent struct {
	EndpointPath                string              `json:"endpoint_path,omitempty"`
	App                         string              `json:"app"`
	AppID                       string              `json:"app_id,omitempty"`
	ExpectedServingDeploymentID string              `json:"expected_serving_deployment_id,omitempty"`
	SourceSHA256                string              `json:"source_sha256"`
	FixturesSHA256              string              `json:"fixtures_sha256"`
	AccessMode                  string              `json:"access_mode"`
	ToolScopes                  map[string][]string `json:"tool_scopes"`
	TestCases                   []string            `json:"test_cases"`
}

type ToolPublicationCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ToolPublicationOutcome is publisher-attested runtime evidence. APID binds
// successful evidence to the native deployment and its source artifact.
type ToolPublicationOutcome struct {
	Status        string `json:"status"`
	DeploymentID  string `json:"deployment_id,omitempty"`
	CatalogSHA256 string `json:"catalog_sha256,omitempty"`
	// Base64 preserves canonical catalog bytes through JSONB storage, whose
	// object representation otherwise changes whitespace and key ordering.
	Catalog          []byte                 `json:"catalog_base64,omitempty"`
	ProtocolVerified bool                   `json:"protocol_verified"`
	Checks           []ToolPublicationCheck `json:"checks,omitempty"`
}

type ToolPublicationRecord struct {
	ID                     string                  `json:"id"`
	AccountID              string                  `json:"account_id"`
	Intent                 ToolPublicationIntent   `json:"intent"`
	Status                 string                  `json:"status"`
	Outcome                *ToolPublicationOutcome `json:"outcome,omitempty"`
	DeploymentSourceSHA256 string                  `json:"deployment_source_sha256,omitempty"`
	CreatedAt              time.Time               `json:"created_at"`
	Deadline               time.Time               `json:"deadline"`
	FinishedAt             *time.Time              `json:"finished_at,omitempty"`
	EvidenceOrigin         string                  `json:"evidence_origin"`
}

type BeginToolPublicationResponse struct {
	Created bool                  `json:"created"`
	Record  ToolPublicationRecord `json:"record"`
}

type ToolRegistryVersion struct {
	AppID                  string   `json:"app_id,omitempty"`
	Version                string   `json:"version"`
	AccountID              string   `json:"account_id"`
	App                    string   `json:"app"`
	Tools                  []string `json:"tools"`
	DeploymentID           string   `json:"deployment_id"`
	SourceSHA256           string   `json:"source_sha256"`
	DeploymentSourceSHA256 string   `json:"deployment_source_sha256"`
	CatalogSHA256          string   `json:"catalog_sha256"`
	EvidenceOrigin         string   `json:"evidence_origin"`
	AccessMode             string   `json:"access_mode"`
	Availability           string   `json:"availability"`
}

type ToolRegistryPage struct {
	Versions   []ToolRegistryVersion `json:"versions"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func (c *Client) BeginToolPublication(ctx context.Context, id string, in ToolPublicationIntent) (BeginToolPublicationResponse, error) {
	var out BeginToolPublicationResponse
	err := c.do(ctx, http.MethodPut, "/v1/tool-publications/"+url.PathEscape(id), in, &out)
	return out, err
}

func (c *Client) FinishToolPublication(ctx context.Context, id string, in ToolPublicationOutcome) (ToolPublicationRecord, error) {
	var out ToolPublicationRecord
	err := c.do(ctx, http.MethodPost, "/v1/tool-publications/"+url.PathEscape(id)+"/outcome", in, &out)
	return out, err
}

func (c *Client) GetToolPublication(ctx context.Context, id string) (ToolPublicationRecord, error) {
	var out ToolPublicationRecord
	err := c.do(ctx, http.MethodGet, "/v1/tool-publications/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) SearchToolRegistry(ctx context.Context, query, cursor string, limit int) (ToolRegistryPage, error) {
	q := url.Values{"query": {query}, "cursor": {cursor}, "limit": {strconv.Itoa(limit)}}
	var out ToolRegistryPage
	err := c.do(ctx, http.MethodGet, "/v1/tool-registry?"+q.Encode(), nil, &out)
	return out, err
}
