package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpRoleProtocolReport struct {
	Name              string             `json:"name"`
	ProtocolVersion   string             `json:"protocol_version"`
	Qualified         bool               `json:"qualified"`
	CatalogSHA256     string             `json:"catalog_sha256,omitempty"`
	Tools             int                `json:"tools"`
	Resources         int                `json:"resources"`
	ResourceTemplates int                `json:"resource_templates"`
	Prompts           int                `json:"prompts"`
	Error             string             `json:"error,omitempty"`
	Authorization     []mcphosting.Check `json:"authorization,omitempty"`
}

type mcpReplicaProtocolReport struct {
	InstanceID string                  `json:"instance_id"`
	Qualified  bool                    `json:"qualified"`
	Roles      []mcpRoleProtocolReport `json:"roles"`
	Error      string                  `json:"error,omitempty"`
}

type mcpCatalogProtocolFleetReport struct {
	Qualified         bool                       `json:"qualified"`
	ExpectedReplicas  int                        `json:"expected_replicas"`
	CheckedReplicas   int                        `json:"checked_replicas"`
	Replicas          []mcpReplicaProtocolReport `json:"replicas"`
	PublicRoles       []mcpRoleProtocolReport    `json:"public_roles,omitempty"`
	FailedInstanceIDs []string                   `json:"failed_instance_ids,omitempty"`
	Error             string                     `json:"error,omitempty"`
}

type mcpInstanceProtocolRoundTripper struct {
	client          *api.Client
	slug            string
	deploymentID    string
	instanceID      string
	host            string
	endpoint        string
	protocolVersion string
	protocolErr     error
}

type mcpPublicProtocolRoundTripper struct {
	next        http.RoundTripper
	version     string
	protocolErr error
}

func (t *mcpPublicProtocolRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.next.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if err := validateMCPProtocolResponseHeaders(response.StatusCode, response.Header, t.version); response.StatusCode >= 200 && response.StatusCode < 300 && err != nil && t.protocolErr == nil {
		t.protocolErr = err
	}
	return response, nil
}

func validateMCPProtocolResponseHeaders(status int, header http.Header, version string) error {
	if header.Get("MCP-Session-Id") != "" {
		return fmt.Errorf("MCP response created a stateful session")
	}
	// A notification's 202 Accepted has no body and no version to echo;
	// the starters answer notifications/initialized with a bare 202, which
	// failed every mcp deploy qualification (Hunt 14 H14-2).
	if status == http.StatusAccepted {
		return nil
	}
	if actual := header.Get("MCP-Protocol-Version"); actual != version {
		return fmt.Errorf("MCP response reported protocol version %q, want %q", actual, version)
	}
	return nil
}

func (t *mcpInstanceProtocolRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		defer func() { _ = request.Body.Close() }()
	}
	if request.Method != http.MethodPost || request.URL.Path != t.endpoint {
		return nil, fmt.Errorf("exact-instance MCP probe only accepts POST to the configured endpoint")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, api.MCPProbeMaxRequestBytes+1))
	if err != nil || len(body) > api.MCPProbeMaxRequestBytes {
		return nil, fmt.Errorf("MCP protocol request exceeds its size limit")
	}
	method, valid := api.MCPProbeRPCMethod(body)
	version := request.Header.Get("MCP-Protocol-Version")
	if !valid || !api.MCPProbeRPCMatchesVersion(body, version) || version != t.protocolVersion {
		return nil, fmt.Errorf("MCP request is outside the exact-instance probe allowlist")
	}
	token := ""
	if authorization := request.Header.Get("Authorization"); authorization != "" {
		const bearerPrefix = "Bearer "
		if !strings.HasPrefix(authorization, bearerPrefix) {
			return nil, fmt.Errorf("MCP probe only accepts bearer authorization")
		}
		token = strings.TrimPrefix(authorization, bearerPrefix)
	}
	if !api.ValidMCPProbeToken(token) {
		return nil, fmt.Errorf("MCP bearer token is not valid for forwarding")
	}
	probe := api.MCPInstanceProtocolProbeRequest{
		Host: t.host, Endpoint: t.endpoint, ProtocolVersion: version, RPCRequest: body,
	}
	result, err := t.client.ProbeMCPInstanceProtocol(request.Context(), t.slug, t.deploymentID, t.instanceID, probe, token)
	if err != nil {
		return nil, fmt.Errorf("probe %s on instance %s: %w", method, t.instanceID, err)
	}
	if result.AppID == "" || result.DeploymentID != t.deploymentID || result.InstanceID != t.instanceID {
		return nil, fmt.Errorf("platform returned an identity that does not match the requested instance")
	}
	if result.BodyTooLarge {
		return nil, fmt.Errorf("MCP %s response from instance %s exceeds %d bytes", method, t.instanceID, api.MCPProbeMaxResponseBytes)
	}
	if result.SessionIDPresent && t.protocolErr == nil {
		t.protocolErr = fmt.Errorf("MCP %s response from instance %s created a session", method, t.instanceID)
	}
	protocolHeaders := make(http.Header)
	protocolHeaders.Set("MCP-Protocol-Version", result.ProtocolVersion)
	protocolHeaders.Set("Mcp-Session-Id", result.SessionID)
	if err := validateMCPProtocolResponseHeaders(result.HTTPStatus, protocolHeaders, t.protocolVersion); result.HTTPStatus >= 200 && result.HTTPStatus < 300 && err != nil && t.protocolErr == nil {
		t.protocolErr = fmt.Errorf("MCP %s response from instance %s: %w", method, t.instanceID, err)
	}
	header := make(http.Header)
	if result.ContentType != "" {
		header.Set("Content-Type", result.ContentType)
	}
	if result.ProtocolVersion != "" {
		header.Set("MCP-Protocol-Version", result.ProtocolVersion)
	}
	if result.SessionID != "" {
		header.Set("Mcp-Session-Id", result.SessionID)
	}
	responseBody := []byte(result.Body)
	return &http.Response{
		StatusCode:    result.HTTPStatus,
		Status:        fmt.Sprintf("%d %s", result.HTTPStatus, http.StatusText(result.HTTPStatus)),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(responseBody)),
		ContentLength: int64(len(responseBody)),
		Request:       request,
	}, nil
}

func qualifyMCPProtocolInstance(ctx context.Context, client *api.Client, slug, deploymentID, instanceID, host, endpoint string, role mcpReleaseRole, timeout time.Duration) mcpRoleProtocolReport {
	version := role.contract.ProtocolVersion
	if !api.ValidMCPProbeProtocolVersion(version) {
		version = mcphosting.ProtocolVersion
	}
	name := role.Name
	if name == "" {
		name = "default"
	}
	report := mcpRoleProtocolReport{Name: name, ProtocolVersion: version}
	if client == nil || timeout <= 0 || !api.ValidMCPProbeHost(host) || !api.ValidMCPProbeEndpointPath(endpoint) {
		report.Error = "exact-instance MCP protocol probe is not configured correctly"
		return report
	}
	probe, err := mcphosting.NewClient("https://mcp-instance-probe.invalid"+endpoint, role.token, version)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	transport := &mcpInstanceProtocolRoundTripper{
		client: client, slug: slug, deploymentID: deploymentID, instanceID: instanceID,
		host: host, endpoint: endpoint, protocolVersion: version,
	}
	probe.HTTP = &http.Client{
		Timeout: timeout, Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	report = qualifyMCPProtocolClient(ctx, probe, role, version, name)
	if transport.protocolErr != nil && report.Qualified {
		report.Qualified = false
		report.Error = transport.protocolErr.Error()
	}
	return report
}

func qualifyMCPProtocolClient(ctx context.Context, probe *mcphosting.Client, role mcpReleaseRole, version, name string) mcpRoleProtocolReport {
	report := mcpRoleProtocolReport{Name: name, ProtocolVersion: version}
	var baseline *mcphosting.Contract
	if role.contract.Version == 2 {
		baseline = &role.contract
	}
	validation := mcphosting.ValidateCompatibility(ctx, probe, baseline)
	if !validation.OK {
		for _, check := range validation.Checks {
			if check.Status == "failed" {
				report.Error = check.Name + ": " + check.Detail
				break
			}
		}
		return report
	}
	report.CatalogSHA256 = validation.CatalogSHA256
	report.Tools, report.Resources = validation.Tools, validation.Resources
	report.ResourceTemplates, report.Prompts = validation.ResourceTemplates, validation.Prompts
	if role.Authorization != nil {
		catalog := validation.Catalog
		report.Authorization = probe.CheckAuthorization(ctx, role.Authorization.AuthorizationExpectations, catalog, role.Authorization.insufficientToken)
		if !mcpAuthorizationChecksPassed(report.Authorization) {
			report.Error = "role authorization rejection or catalog visibility expectations failed"
			return report
		}
	}
	report.Qualified = true
	return report
}

func qualifyMCPProtocolEndpoint(ctx context.Context, endpoint string, role mcpReleaseRole, timeout time.Duration) mcpRoleProtocolReport {
	version := role.contract.ProtocolVersion
	if !api.ValidMCPProbeProtocolVersion(version) {
		version = mcphosting.ProtocolVersion
	}
	name := role.Name
	if name == "" {
		name = "default"
	}
	report := mcpRoleProtocolReport{Name: name, ProtocolVersion: version}
	if timeout <= 0 {
		report.Error = "MCP endpoint qualification requires a positive timeout"
		return report
	}
	probe, err := mcphosting.NewClient(endpoint, role.token, version)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	transport := &mcpPublicProtocolRoundTripper{next: http.DefaultTransport, version: version}
	probe.HTTP = &http.Client{
		Timeout: timeout, Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	report = qualifyMCPProtocolClient(ctx, probe, role, version, name)
	if transport.protocolErr != nil && report.Qualified {
		report.Qualified = false
		report.Error = transport.protocolErr.Error()
	}
	return report
}

func qualifyMCPProtocolPublicRoles(ctx context.Context, endpointURL, endpointPath string, roles []mcpReleaseRole, exact mcpCatalogProtocolFleetReport, timeout time.Duration) []mcpRoleProtocolReport {
	if len(roles) == 0 {
		roles = []mcpReleaseRole{{Name: "default"}}
	}
	u, err := url.Parse(endpointURL)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || !api.ValidMCPProbeHost(u.Host) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !api.ValidMCPProbeEndpointPath(endpointPath) {
		return []mcpRoleProtocolReport{{Name: "public", Error: "MCP public endpoint is invalid for protocol qualification"}}
	}
	u.Path, u.RawPath = endpointPath, ""
	reports := make([]mcpRoleProtocolReport, 0, len(roles))
	for _, role := range roles {
		report := qualifyMCPProtocolEndpoint(ctx, u.String(), role, timeout)
		if report.Qualified {
			for _, replica := range exact.Replicas {
				for _, exactRole := range replica.Roles {
					if exactRole.Name == report.Name && exactRole.Qualified && exactRole.CatalogSHA256 != report.CatalogSHA256 {
						report.Qualified = false
						report.Error = "public endpoint exposes a different catalog than the exact replicas"
						break
					}
				}
				if !report.Qualified {
					break
				}
			}
		}
		reports = append(reports, report)
	}
	return reports
}

func qualifyMCPProtocolDeployment(ctx context.Context, client *api.Client, slug, deploymentID, endpointURL, endpointPath string, roles []mcpReleaseRole, timeout time.Duration) mcpCatalogProtocolFleetReport {
	report := mcpCatalogProtocolFleetReport{Replicas: []mcpReplicaProtocolReport{}}
	if timeout <= 0 || !api.ValidMCPProbeEndpointPath(endpointPath) {
		report.Error = "MCP protocol qualification requires a positive timeout and literal endpoint path"
		return report
	}
	u, err := url.Parse(endpointURL)
	if err != nil || u == nil || !api.ValidMCPProbeHost(u.Host) {
		report.Error = "MCP endpoint does not contain a valid host for replica qualification"
		return report
	}
	if len(roles) == 0 {
		roles = []mcpReleaseRole{{Name: "default"}}
	}
	instances, err := client.ListInstances(ctx, slug)
	if err != nil {
		report.Error = fmt.Sprintf("list deployment replicas for protocol qualification: %v", err)
		return report
	}
	expected := mcpDeploymentReplicaIDs(instances, deploymentID)
	report.ExpectedReplicas = len(expected)
	if len(expected) == 0 {
		report.Error = "deployment has no running replicas to qualify"
		return report
	}
	roleDigests := make(map[string]string, len(roles))
	checkedIDs := make(map[string]struct{}, len(expected))
	for _, instanceID := range expected {
		replica := mcpReplicaProtocolReport{InstanceID: instanceID, Qualified: true, Roles: make([]mcpRoleProtocolReport, 0, len(roles))}
		for _, role := range roles {
			roleReport := qualifyMCPProtocolInstance(ctx, client, slug, deploymentID, instanceID, u.Host, endpointPath, role, timeout)
			replica.Roles = append(replica.Roles, roleReport)
			if !roleReport.Qualified {
				replica.Qualified = false
				if replica.Error == "" {
					replica.Error = fmt.Sprintf("role %s: %s", roleReport.Name, roleReport.Error)
				}
				continue
			}
			if previous, ok := roleDigests[roleReport.Name]; ok && previous != roleReport.CatalogSHA256 {
				replica.Qualified = false
				if replica.Error == "" {
					replica.Error = fmt.Sprintf("role %s sees a different catalog on this replica", roleReport.Name)
				}
			} else {
				roleDigests[roleReport.Name] = roleReport.CatalogSHA256
			}
		}
		if replica.Qualified {
			report.CheckedReplicas++
			checkedIDs[instanceID] = struct{}{}
		} else if report.Error == "" {
			report.Error = fmt.Sprintf("replica %s failed protocol qualification: %s", instanceID, replica.Error)
		}
		report.Replicas = append(report.Replicas, replica)
	}
	for _, instanceID := range expected {
		if _, ok := checkedIDs[instanceID]; !ok {
			report.FailedInstanceIDs = append(report.FailedInstanceIDs, instanceID)
		}
	}
	latest, err := client.ListInstances(ctx, slug)
	if err != nil {
		report.Qualified = false
		if report.Error == "" {
			report.Error = fmt.Sprintf("confirm replica set after protocol qualification: %v", err)
		}
		return report
	}
	if current := mcpDeploymentReplicaIDs(latest, deploymentID); !sameMCPReplicaIDs(expected, current) {
		report.Qualified = false
		if report.Error == "" {
			report.Error = "running replica set changed during MCP protocol qualification; retry"
		}
		return report
	}
	if len(report.FailedInstanceIDs) > 0 && report.Error == "" {
		report.Error = fmt.Sprintf("protocol qualification verified %d of %d running replicas", report.CheckedReplicas, report.ExpectedReplicas)
	}
	report.Qualified = report.CheckedReplicas == report.ExpectedReplicas && report.Error == ""
	return report
}
