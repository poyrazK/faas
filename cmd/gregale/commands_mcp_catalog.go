package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const mcpCatalogHealthMaxBytes = 64 << 10

var mcpCatalogSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// mcpCatalogHealthReport describes the health endpoint contract implemented by
// the Gregale MCP starters. Unsupported apps remain deployable, but cannot
// provide a revision-bound catalog integrity check.
type mcpCatalogHealthReport struct {
	HealthURL       string `json:"health_url"`
	Supported       bool   `json:"supported"`
	Healthy         bool   `json:"healthy"`
	HTTPStatus      int    `json:"http_status,omitempty"`
	Status          string `json:"status,omitempty"`
	InstanceID      string `json:"instance_id,omitempty"`
	CatalogRevision *int64 `json:"catalog_revision,omitempty"`
	CatalogSHA256   string `json:"catalog_sha256,omitempty"`
	Error           string `json:"error,omitempty"`
}

type mcpCatalogFleetHealthReport struct {
	Supported          bool                     `json:"supported"`
	Healthy            bool                     `json:"healthy"`
	ExpectedReplicas   int                      `json:"expected_replicas"`
	CheckedReplicas    int                      `json:"checked_replicas"`
	CatalogSHA256      string                   `json:"catalog_sha256,omitempty"`
	Replicas           []mcpCatalogHealthReport `json:"replicas"`
	MissingInstanceIDs []string                 `json:"missing_instance_ids,omitempty"`
	Error              string                   `json:"error,omitempty"`
}

type mcpCatalogDeploymentReport struct {
	Admission      *mcpAdmissionHistory          `json:"admission,omitempty"`
	DeploymentID   string                        `json:"deployment_id"`
	TrafficPercent int                           `json:"traffic_percent"`
	Health         mcpCatalogFleetHealthReport   `json:"health"`
	Protocol       mcpCatalogProtocolFleetReport `json:"protocol"`
	Error          string                        `json:"error,omitempty"`
}

type mcpCatalogStatusReport struct {
	Convergence              *mcpConvergenceReport        `json:"convergence,omitempty"`
	Supported                bool                         `json:"supported"`
	Healthy                  bool                         `json:"healthy"`
	ProtocolQualified        bool                         `json:"protocol_qualified"`
	ExpectedReplicas         int                          `json:"expected_replicas"`
	CheckedReplicas          int                          `json:"checked_replicas"`
	ProtocolExpectedReplicas int                          `json:"protocol_expected_replicas"`
	ProtocolCheckedReplicas  int                          `json:"protocol_checked_replicas"`
	Deployments              []mcpCatalogDeploymentReport `json:"deployments"`
	Error                    string                       `json:"error,omitempty"`
}

func mcpHealthURL(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("MCP endpoint must be an HTTP(S) URL without credentials, query, or fragment")
	}
	u.Path, u.RawPath = "/healthz", ""
	return u.String(), nil
}

func probeMCPCatalogHealth(ctx context.Context, endpoint string, timeout time.Duration) mcpCatalogHealthReport {
	healthURL, err := mcpHealthURL(endpoint)
	report := mcpCatalogHealthReport{HealthURL: healthURL}
	if err != nil {
		report.Error = err.Error()
		return report
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache, no-store")
	req.Close = true
	resp, err := client.Do(req)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	defer func() { _ = resp.Body.Close() }()
	report.HTTPStatus = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, mcpCatalogHealthMaxBytes+1))
	if err != nil {
		report.Error = fmt.Sprintf("read MCP health response: %v", err)
		return report
	}
	if len(body) > mcpCatalogHealthMaxBytes {
		report.Error = "MCP health response exceeds 64 KiB"
		return report
	}
	parsed := parseMCPCatalogHealth(body, resp.StatusCode, "")
	parsed.HealthURL = healthURL
	return parsed
}

func parseMCPCatalogHealth(body []byte, status int, platformInstanceID string) mcpCatalogHealthReport {
	report := mcpCatalogHealthReport{HTTPStatus: status}
	if len(body) > mcpCatalogHealthMaxBytes {
		report.Error = "MCP health response exceeds 64 KiB"
		return report
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		report.Error = "MCP health response is not a JSON object"
		return report
	}
	rawDigest, hasDigest := payload["catalog_sha256"]
	if !hasDigest {
		report.Error = "MCP health response does not expose catalog integrity"
		return report
	}
	report.Supported = true
	if err := json.Unmarshal(rawDigest, &report.CatalogSHA256); err != nil || !mcpCatalogSHA256Pattern.MatchString(report.CatalogSHA256) {
		report.Error = "catalog_sha256 must be a lowercase 64-character SHA-256 digest"
		return report
	}
	if rawRevision, ok := payload["catalog_revision"]; ok {
		var revision int64
		if err := json.Unmarshal(rawRevision, &revision); err == nil && revision >= 0 {
			report.CatalogRevision = &revision
		}
	}
	if report.CatalogRevision == nil {
		report.Error = "catalog_revision must be a nonnegative integer"
		return report
	}
	if rawStatus, ok := payload["status"]; ok {
		_ = json.Unmarshal(rawStatus, &report.Status)
	}
	if rawInstance, ok := payload["instance_id"]; ok {
		var workloadInstanceID string
		_ = json.Unmarshal(rawInstance, &workloadInstanceID)
		workloadInstanceID = strings.TrimSpace(workloadInstanceID)
		if platformInstanceID != "" && workloadInstanceID != "" && workloadInstanceID != platformInstanceID {
			report.InstanceID = platformInstanceID
			report.Error = "MCP health response instance_id does not match the platform-selected instance"
			return report
		}
		if platformInstanceID == "" {
			report.InstanceID = workloadInstanceID
		}
	}
	if platformInstanceID != "" {
		report.InstanceID = platformInstanceID
	}
	if report.HTTPStatus != http.StatusOK {
		report.Error = fmt.Sprintf("MCP health endpoint returned HTTP %d", report.HTTPStatus)
		return report
	}
	if report.Status != "ok" {
		report.Error = fmt.Sprintf("MCP catalog health status is %q", report.Status)
		return report
	}
	report.Healthy = true
	return report
}

func mcpDeploymentReplicaIDs(instances []api.InstanceResponse, deploymentID string) []string {
	deploymentID = canonicalMCPUUID(deploymentID)
	seen := make(map[string]struct{})
	ids := make([]string, 0)
	for _, instance := range instances {
		instanceID := canonicalMCPUUID(instance.ID)
		if canonicalMCPUUID(instance.DeploymentID) != deploymentID || instance.State != "running" || instanceID == "" {
			continue
		}
		if _, ok := seen[instanceID]; ok {
			continue
		}
		seen[instanceID] = struct{}{}
		ids = append(ids, instanceID)
	}
	sort.Strings(ids)
	return ids
}

func canonicalMCPUUID(value string) string {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return value
	}
	return parsed.String()
}

func probeMCPCatalogInstance(ctx context.Context, c *Client, slug, deploymentID, instanceID, healthURL string, timeout time.Duration) (mcpCatalogHealthReport, bool) {
	report := mcpCatalogHealthReport{InstanceID: instanceID}
	u, err := url.Parse(healthURL)
	if c == nil || timeout <= 0 || err != nil || u == nil || !api.ValidMCPProbeHost(u.Host) {
		report.Error = "MCP exact-instance probe is not configured correctly"
		return report, false
	}
	report.HealthURL = healthURL
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := c.ProbeMCPInstanceHealth(probeCtx, slug, deploymentID, instanceID, u.Host)
	if err != nil {
		report.Error = fmt.Sprintf("probe instance %s: %v", instanceID, err)
		return report, false
	}
	if response.AppID == "" || response.DeploymentID != deploymentID || response.InstanceID != instanceID {
		report.Supported = true
		report.Error = "platform returned an identity that does not match the requested app, deployment, and instance"
		return report, false
	}
	if response.HealthBodyTooLarge {
		report.Supported = true
		report.HTTPStatus = response.HTTPStatus
		report.Error = "MCP health response exceeds 64 KiB"
		return report, true
	}
	report = parseMCPCatalogHealth([]byte(response.HealthBody), response.HTTPStatus, response.InstanceID)
	report.InstanceID = response.InstanceID
	report.HealthURL = healthURL
	return report, true
}

func probeMCPCatalogExactFleet(ctx context.Context, c *Client, slug, deploymentID, endpoint string, expectedIDs []string, timeout time.Duration) mcpCatalogFleetHealthReport {
	report := mcpCatalogFleetHealthReport{ExpectedReplicas: len(expectedIDs), Replicas: []mcpCatalogHealthReport{}}
	if len(expectedIDs) == 0 {
		report.Error = "deployment has no running replicas to verify"
		return report
	}
	if timeout <= 0 {
		report.Error = "MCP catalog probe timeout must be positive"
		return report
	}
	healthURL, err := mcpHealthURL(endpoint)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	u, err := url.Parse(healthURL)
	if err != nil || !api.ValidMCPProbeHost(u.Host) {
		report.Error = "MCP endpoint does not contain a valid host for replica verification"
		return report
	}
	checkedIDs := make(map[string]struct{}, len(expectedIDs))
	for _, instanceID := range expectedIDs {
		if instanceID == "" {
			report.Error = "platform returned a running replica without an instance ID"
			continue
		}
		health, checked := probeMCPCatalogInstance(ctx, c, slug, deploymentID, instanceID, healthURL, timeout)
		report.Replicas = append(report.Replicas, health)
		if checked {
			checkedIDs[instanceID] = struct{}{}
			report.CheckedReplicas++
		}
		if health.Supported {
			report.Supported = true
		}
		if health.CatalogSHA256 != "" {
			if report.CatalogSHA256 == "" {
				report.CatalogSHA256 = health.CatalogSHA256
			} else if report.CatalogSHA256 != health.CatalogSHA256 && report.Error == "" {
				report.Error = "running replicas report different MCP catalog digests"
			}
		}
		if !health.Healthy && report.Error == "" {
			report.Error = fmt.Sprintf("replica %s is unhealthy: %s", health.InstanceID, health.Error)
		}
	}
	for _, instanceID := range expectedIDs {
		if _, ok := checkedIDs[instanceID]; !ok {
			report.MissingInstanceIDs = append(report.MissingInstanceIDs, instanceID)
		}
	}
	if len(report.MissingInstanceIDs) > 0 && report.Error == "" {
		report.Error = fmt.Sprintf("catalog health verified %d of %d running replicas", report.CheckedReplicas, report.ExpectedReplicas)
	}
	sort.Slice(report.Replicas, func(i, j int) bool { return report.Replicas[i].InstanceID < report.Replicas[j].InstanceID })
	report.Healthy = report.Supported && report.CheckedReplicas == report.ExpectedReplicas && report.Error == ""
	return report
}

func probeMCPCatalogDeployment(ctx context.Context, c *Client, slug, deploymentID, endpoint string, timeout time.Duration) mcpCatalogFleetHealthReport {
	deploymentID = canonicalMCPUUID(deploymentID)
	instances, err := c.ListInstances(ctx, slug)
	if err != nil {
		return mcpCatalogFleetHealthReport{Replicas: []mcpCatalogHealthReport{}, Error: fmt.Sprintf("list deployment replicas: %v", err)}
	}
	expected := mcpDeploymentReplicaIDs(instances, deploymentID)
	if len(expected) == 0 {
		return mcpCatalogFleetHealthReport{Replicas: []mcpCatalogHealthReport{}, Error: "deployment has no running replicas to verify"}
	}
	report := probeMCPCatalogExactFleet(ctx, c, slug, deploymentID, endpoint, expected, timeout)
	if !report.Supported || !report.Healthy {
		return report
	}
	latest, err := c.ListInstances(ctx, slug)
	if err != nil {
		report.Healthy = false
		report.Error = fmt.Sprintf("confirm replica set after catalog probe: %v", err)
		return report
	}
	if current := mcpDeploymentReplicaIDs(latest, deploymentID); !sameMCPReplicaIDs(expected, current) {
		report.Healthy = false
		report.Error = "running replica set changed during catalog verification; retry"
	}
	return report
}

func sameMCPReplicaIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left, right := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// mcpCatalogDigestForPin returns the candidate's observed catalog digest when
// the health response is complete. A 503 degraded response is accepted here:
// the Gregale starters report the actual digest while refusing requests when
// an existing pin differs, so the next isolated revision can be pinned to the
// observed catalog and then rechecked by the normal release gate.
func mcpCatalogDigestForPin(report mcpCatalogHealthReport) (string, error) {
	if !report.Supported {
		return "", nil
	}
	if report.CatalogRevision == nil || !mcpCatalogSHA256Pattern.MatchString(report.CatalogSHA256) {
		return "", fmt.Errorf("candidate exposes an incomplete catalog integrity contract: %s", report.Error)
	}
	if (report.Status == "ok" && report.HTTPStatus == http.StatusOK && report.Healthy) ||
		(report.Status == "degraded" && report.HTTPStatus == http.StatusServiceUnavailable) {
		return report.CatalogSHA256, nil
	}
	return "", fmt.Errorf("candidate catalog health cannot be pinned safely: %s", report.Error)
}

type mcpStatusWatchEvent struct {
	Event     string                 `json:"event"`
	Timestamp time.Time              `json:"timestamp"`
	App       string                 `json:"app"`
	Changed   []string               `json:"changed,omitempty"`
	Catalog   mcpCatalogStatusReport `json:"catalog"`
}

type mcpStatusReplicaSet struct {
	DeploymentID   string   `json:"deployment_id"`
	TrafficPercent int      `json:"traffic_percent"`
	HealthIDs      []string `json:"health_ids"`
	ProtocolIDs    []string `json:"protocol_ids"`
}

func mcpStatusJSONFingerprint(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("serialization-error:%v", err)
	}
	return string(encoded)
}

func mcpStatusReplicaFingerprint(report mcpCatalogStatusReport) string {
	sets := make([]mcpStatusReplicaSet, 0, len(report.Deployments))
	for _, deployment := range report.Deployments {
		set := mcpStatusReplicaSet{DeploymentID: deployment.DeploymentID, TrafficPercent: deployment.TrafficPercent, HealthIDs: []string{}, ProtocolIDs: []string{}}
		for _, replica := range deployment.Health.Replicas {
			set.HealthIDs = append(set.HealthIDs, replica.InstanceID)
		}
		for _, replica := range deployment.Protocol.Replicas {
			set.ProtocolIDs = append(set.ProtocolIDs, replica.InstanceID)
		}
		sort.Strings(set.HealthIDs)
		sort.Strings(set.ProtocolIDs)
		sets = append(sets, set)
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].DeploymentID < sets[j].DeploymentID })
	return mcpStatusJSONFingerprint(sets)
}

func mcpStatusHealthFingerprint(report mcpCatalogStatusReport) string {
	type healthReplica struct {
		InstanceID string `json:"instance_id"`
		Healthy    bool   `json:"healthy"`
		HTTPStatus int    `json:"http_status"`
		Status     string `json:"status"`
		Error      string `json:"error"`
	}
	type deploymentHealth struct {
		DeploymentID string          `json:"deployment_id"`
		Error        string          `json:"error"`
		ProbeError   string          `json:"probe_error"`
		Supported    bool            `json:"supported"`
		Healthy      bool            `json:"healthy"`
		Expected     int             `json:"expected"`
		Checked      int             `json:"checked"`
		Missing      []string        `json:"missing"`
		Replicas     []healthReplica `json:"replicas"`
	}
	parts := struct {
		Supported bool               `json:"supported"`
		Items     []deploymentHealth `json:"items"`
	}{Supported: report.Supported, Items: []deploymentHealth{}}
	for _, deployment := range report.Deployments {
		item := deploymentHealth{
			DeploymentID: deployment.DeploymentID,
			Error:        deployment.Error,
			ProbeError:   deployment.Health.Error,
			Supported:    deployment.Health.Supported,
			Healthy:      deployment.Health.Healthy,
			Expected:     deployment.Health.ExpectedReplicas,
			Checked:      deployment.Health.CheckedReplicas,
			Missing:      append([]string(nil), deployment.Health.MissingInstanceIDs...),
			Replicas:     []healthReplica{},
		}
		sort.Strings(item.Missing)
		for _, replica := range deployment.Health.Replicas {
			item.Replicas = append(item.Replicas, healthReplica{InstanceID: replica.InstanceID, Healthy: replica.Healthy, HTTPStatus: replica.HTTPStatus, Status: replica.Status, Error: replica.Error})
		}
		sort.Slice(item.Replicas, func(i, j int) bool { return item.Replicas[i].InstanceID < item.Replicas[j].InstanceID })
		parts.Items = append(parts.Items, item)
	}
	sort.Slice(parts.Items, func(i, j int) bool { return parts.Items[i].DeploymentID < parts.Items[j].DeploymentID })
	return mcpStatusJSONFingerprint(parts)
}

func mcpStatusProtocolFingerprint(report mcpCatalogStatusReport) string {
	type deploymentProtocol struct {
		DeploymentID string                     `json:"deployment_id"`
		Qualified    bool                       `json:"qualified"`
		Expected     int                        `json:"expected"`
		Checked      int                        `json:"checked"`
		Replicas     []mcpReplicaProtocolReport `json:"replicas"`
		PublicRoles  []mcpRoleProtocolReport    `json:"public_roles"`
		Failed       []string                   `json:"failed"`
		Error        string                     `json:"error"`
	}
	parts := struct {
		Qualified bool                 `json:"qualified"`
		Items     []deploymentProtocol `json:"items"`
	}{Qualified: report.ProtocolQualified, Items: []deploymentProtocol{}}
	for _, deployment := range report.Deployments {
		item := deploymentProtocol{
			DeploymentID: deployment.DeploymentID,
			Qualified:    deployment.Protocol.Qualified,
			Expected:     deployment.Protocol.ExpectedReplicas,
			Checked:      deployment.Protocol.CheckedReplicas,
			Replicas:     append([]mcpReplicaProtocolReport(nil), deployment.Protocol.Replicas...),
			PublicRoles:  append([]mcpRoleProtocolReport(nil), deployment.Protocol.PublicRoles...),
			Failed:       append([]string(nil), deployment.Protocol.FailedInstanceIDs...),
			Error:        deployment.Protocol.Error,
		}
		for i := range item.Replicas {
			item.Replicas[i].Roles = append([]mcpRoleProtocolReport(nil), item.Replicas[i].Roles...)
			sort.Slice(item.Replicas[i].Roles, func(a, b int) bool { return item.Replicas[i].Roles[a].Name < item.Replicas[i].Roles[b].Name })
			for j := range item.Replicas[i].Roles {
				item.Replicas[i].Roles[j].CatalogSHA256 = ""
			}
		}
		sort.Slice(item.Replicas, func(i, j int) bool { return item.Replicas[i].InstanceID < item.Replicas[j].InstanceID })
		for i := range item.PublicRoles {
			item.PublicRoles[i].CatalogSHA256 = ""
		}
		sort.Slice(item.PublicRoles, func(i, j int) bool { return item.PublicRoles[i].Name < item.PublicRoles[j].Name })
		sort.Strings(item.Failed)
		parts.Items = append(parts.Items, item)
	}
	sort.Slice(parts.Items, func(i, j int) bool { return parts.Items[i].DeploymentID < parts.Items[j].DeploymentID })
	return mcpStatusJSONFingerprint(parts)
}

func mcpStatusCatalogFingerprints(report mcpCatalogStatusReport) (string, string) {
	type catalogReplica struct {
		DeploymentID string `json:"deployment_id"`
		InstanceID   string `json:"instance_id,omitempty"`
		Revision     int64  `json:"revision"`
		Digest       string `json:"digest"`
	}
	type catalogRole struct {
		DeploymentID string `json:"deployment_id"`
		InstanceID   string `json:"instance_id,omitempty"`
		Scope        string `json:"scope"`
		Name         string `json:"name"`
		Digest       string `json:"digest"`
	}
	type catalogParts struct {
		Replicas []catalogReplica `json:"replicas"`
		Roles    []catalogRole    `json:"roles"`
	}
	detailed := catalogParts{Replicas: []catalogReplica{}, Roles: []catalogRole{}}
	values := catalogParts{Replicas: []catalogReplica{}, Roles: []catalogRole{}}
	for _, deployment := range report.Deployments {
		for _, replica := range deployment.Health.Replicas {
			revision := valueMCPRevision(replica.CatalogRevision)
			detailed.Replicas = append(detailed.Replicas, catalogReplica{DeploymentID: deployment.DeploymentID, InstanceID: replica.InstanceID, Revision: revision, Digest: replica.CatalogSHA256})
			values.Replicas = append(values.Replicas, catalogReplica{DeploymentID: deployment.DeploymentID, Revision: revision, Digest: replica.CatalogSHA256})
		}
		for _, replica := range deployment.Protocol.Replicas {
			for _, role := range replica.Roles {
				detailed.Roles = append(detailed.Roles, catalogRole{DeploymentID: deployment.DeploymentID, InstanceID: replica.InstanceID, Scope: "replica", Name: role.Name, Digest: role.CatalogSHA256})
				values.Roles = append(values.Roles, catalogRole{DeploymentID: deployment.DeploymentID, Scope: "replica", Name: role.Name, Digest: role.CatalogSHA256})
			}
		}
		for _, role := range deployment.Protocol.PublicRoles {
			detailed.Roles = append(detailed.Roles, catalogRole{DeploymentID: deployment.DeploymentID, InstanceID: "public", Scope: "public", Name: role.Name, Digest: role.CatalogSHA256})
			values.Roles = append(values.Roles, catalogRole{DeploymentID: deployment.DeploymentID, Scope: "public", Name: role.Name, Digest: role.CatalogSHA256})
		}
	}
	sort.Slice(detailed.Replicas, func(i, j int) bool {
		if detailed.Replicas[i].DeploymentID != detailed.Replicas[j].DeploymentID {
			return detailed.Replicas[i].DeploymentID < detailed.Replicas[j].DeploymentID
		}
		if detailed.Replicas[i].InstanceID != detailed.Replicas[j].InstanceID {
			return detailed.Replicas[i].InstanceID < detailed.Replicas[j].InstanceID
		}
		if detailed.Replicas[i].Revision != detailed.Replicas[j].Revision {
			return detailed.Replicas[i].Revision < detailed.Replicas[j].Revision
		}
		return detailed.Replicas[i].Digest < detailed.Replicas[j].Digest
	})
	sort.Slice(values.Replicas, func(i, j int) bool {
		if values.Replicas[i].DeploymentID != values.Replicas[j].DeploymentID {
			return values.Replicas[i].DeploymentID < values.Replicas[j].DeploymentID
		}
		if values.Replicas[i].Revision != values.Replicas[j].Revision {
			return values.Replicas[i].Revision < values.Replicas[j].Revision
		}
		return values.Replicas[i].Digest < values.Replicas[j].Digest
	})
	sort.Slice(detailed.Roles, func(i, j int) bool {
		if detailed.Roles[i].DeploymentID != detailed.Roles[j].DeploymentID {
			return detailed.Roles[i].DeploymentID < detailed.Roles[j].DeploymentID
		}
		if detailed.Roles[i].InstanceID != detailed.Roles[j].InstanceID {
			return detailed.Roles[i].InstanceID < detailed.Roles[j].InstanceID
		}
		if detailed.Roles[i].Name != detailed.Roles[j].Name {
			return detailed.Roles[i].Name < detailed.Roles[j].Name
		}
		return detailed.Roles[i].Digest < detailed.Roles[j].Digest
	})
	sort.Slice(values.Roles, func(i, j int) bool {
		if values.Roles[i].DeploymentID != values.Roles[j].DeploymentID {
			return values.Roles[i].DeploymentID < values.Roles[j].DeploymentID
		}
		if values.Roles[i].Scope != values.Roles[j].Scope {
			return values.Roles[i].Scope < values.Roles[j].Scope
		}
		if values.Roles[i].Name != values.Roles[j].Name {
			return values.Roles[i].Name < values.Roles[j].Name
		}
		return values.Roles[i].Digest < values.Roles[j].Digest
	})
	return mcpStatusJSONFingerprint(detailed), mcpStatusJSONFingerprint(values)
}

func mcpStatusCatalogFingerprint(report mcpCatalogStatusReport) string {
	detailed, _ := mcpStatusCatalogFingerprints(report)
	return detailed
}

func mcpStatusCatalogValuesFingerprint(report mcpCatalogStatusReport) string {
	_, values := mcpStatusCatalogFingerprints(report)
	return values
}

type mcpStatusCatalogKey struct {
	DeploymentID string
	InstanceID   string
	Scope        string
	Name         string
}

func mcpStatusCatalogRecords(report mcpCatalogStatusReport) map[mcpStatusCatalogKey]string {
	records := make(map[mcpStatusCatalogKey]string)
	for _, deployment := range report.Deployments {
		for _, replica := range deployment.Health.Replicas {
			records[mcpStatusCatalogKey{DeploymentID: deployment.DeploymentID, InstanceID: replica.InstanceID, Scope: "health"}] = mcpStatusJSONFingerprint(struct {
				Revision int64  `json:"revision"`
				Digest   string `json:"digest"`
			}{Revision: valueMCPRevision(replica.CatalogRevision), Digest: replica.CatalogSHA256})
		}
		for _, replica := range deployment.Protocol.Replicas {
			for _, role := range replica.Roles {
				records[mcpStatusCatalogKey{DeploymentID: deployment.DeploymentID, InstanceID: replica.InstanceID, Scope: "replica", Name: role.Name}] = role.CatalogSHA256
			}
		}
		for _, role := range deployment.Protocol.PublicRoles {
			records[mcpStatusCatalogKey{DeploymentID: deployment.DeploymentID, InstanceID: "public", Scope: "public", Name: role.Name}] = role.CatalogSHA256
		}
	}
	return records
}

func mcpStatusCatalogDrift(previous, current mcpCatalogStatusReport) bool {
	if mcpStatusCatalogFingerprint(previous) == mcpStatusCatalogFingerprint(current) {
		return false
	}
	if mcpStatusCatalogValuesFingerprint(previous) != mcpStatusCatalogValuesFingerprint(current) {
		return true
	}
	previousRecords, currentRecords := mcpStatusCatalogRecords(previous), mcpStatusCatalogRecords(current)
	for key, previousValue := range previousRecords {
		if currentValue, ok := currentRecords[key]; ok && currentValue != previousValue {
			return true
		}
	}
	return false
}

func mcpStatusChangeKinds(previous, current mcpCatalogStatusReport) []string {
	changed := make([]string, 0, 4)
	replicasChanged := mcpStatusReplicaFingerprint(previous) != mcpStatusReplicaFingerprint(current)
	if mcpStatusHealthFingerprint(previous) != mcpStatusHealthFingerprint(current) ||
		(len(previous.Deployments) == 0 || len(current.Deployments) == 0) && previous.Error != current.Error {
		changed = append(changed, "health")
	}
	if mcpStatusProtocolFingerprint(previous) != mcpStatusProtocolFingerprint(current) {
		changed = append(changed, "protocol")
	}
	if mcpStatusCatalogDrift(previous, current) {
		changed = append(changed, "catalog")
	}
	if replicasChanged {
		changed = append(changed, "replicas")
	}
	if len(changed) == 0 {
		changed = append(changed, "details")
	}
	return changed
}

func mcpStatusFingerprint(app string, report mcpCatalogStatusReport) string {
	return mcpStatusJSONFingerprint(struct {
		App     string                 `json:"app"`
		Catalog mcpCatalogStatusReport `json:"catalog"`
	}{App: app, Catalog: report})
}

func mcpCollectCatalogStatus(ctx context.Context, client *Client, slug, endpoint string, roles []mcpReleaseRole, timeout time.Duration) (string, mcpCatalogStatusReport, error) {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return slug, mcpCatalogStatusReport{}, fmt.Errorf("MCP app: %w", err)
	}
	deployments, err := client.ListAppDeploymentsAll(ctx, app.Slug)
	if err != nil {
		return app.Slug, mcpCatalogStatusReport{}, fmt.Errorf("MCP deployments: %w", err)
	}
	report := mcpCollectCatalogStatusForDeployments(ctx, client, app, deployments, endpoint, roles, timeout)
	attachMCPAdmissionHistory(ctx, client, &report)
	convergence := collectMCPConvergence(ctx, client, app.Slug, app.ID)
	report.Convergence = &convergence
	return app.Slug, report, nil
}

func mcpCollectCatalogStatusForDeployments(ctx context.Context, client *Client, app api.AppResponse, deployments []api.DeploymentResponse, endpoint string, roles []mcpReleaseRole, timeout time.Duration) mcpCatalogStatusReport {
	report := mcpCatalogStatusReport{Deployments: []mcpCatalogDeploymentReport{}}
	for _, deployment := range deployments {
		if deployment.Status != statusLive || deployment.TrafficPercent <= 0 {
			continue
		}
		preview, previewErr := client.GetDeploymentURL(ctx, deployment.ID)
		deploymentReport := mcpCatalogDeploymentReport{DeploymentID: deployment.ID, TrafficPercent: deployment.TrafficPercent}
		if previewErr != nil || !preview.Alive || preview.URL == "" {
			if previewErr != nil {
				deploymentReport.Error = previewErr.Error()
			} else {
				deploymentReport.Error = "serving deployment has no available preview URL"
			}
			deploymentReport.Health = mcpCatalogFleetHealthReport{Replicas: []mcpCatalogHealthReport{}, Error: deploymentReport.Error}
		} else {
			deploymentReport.Health = probeMCPCatalogDeployment(ctx, client, app.Slug, deployment.ID, preview.URL, timeout)
			deploymentReport.Protocol = qualifyMCPProtocolDeployment(ctx, client, app.Slug, deployment.ID, preview.URL, endpoint, roles, timeout)
			deploymentReport.Protocol.PublicRoles = qualifyMCPProtocolPublicRoles(ctx, preview.URL, endpoint, roles, deploymentReport.Protocol, timeout)
			for _, role := range deploymentReport.Protocol.PublicRoles {
				if !role.Qualified {
					deploymentReport.Protocol.Qualified = false
					if deploymentReport.Protocol.Error == "" {
						deploymentReport.Protocol.Error = fmt.Sprintf("public endpoint role %s failed protocol qualification: %s", role.Name, role.Error)
					}
				}
			}
		}
		report.ExpectedReplicas += deploymentReport.Health.ExpectedReplicas
		report.CheckedReplicas += deploymentReport.Health.CheckedReplicas
		report.ProtocolExpectedReplicas += deploymentReport.Protocol.ExpectedReplicas
		report.ProtocolCheckedReplicas += deploymentReport.Protocol.CheckedReplicas
		if deploymentReport.Health.Supported {
			report.Supported = true
		}
		if !deploymentReport.Health.Healthy && report.Error == "" {
			report.Error = deploymentReport.Health.Error
			if report.Error == "" {
				report.Error = "one or more serving deployments did not pass replica catalog verification"
			}
		}
		if !deploymentReport.Protocol.Qualified && report.Error == "" {
			report.Error = deploymentReport.Protocol.Error
			if report.Error == "" {
				report.Error = "one or more serving deployments did not pass exact-replica MCP protocol qualification"
			}
		}
		report.Deployments = append(report.Deployments, deploymentReport)
	}
	sort.Slice(report.Deployments, func(i, j int) bool { return report.Deployments[i].DeploymentID < report.Deployments[j].DeploymentID })
	if len(report.Deployments) == 0 {
		report.Error = "app has no live deployment receiving production traffic"
	} else {
		report.ProtocolQualified = report.ProtocolExpectedReplicas > 0 && report.ProtocolCheckedReplicas == report.ProtocolExpectedReplicas
		for _, deployment := range report.Deployments {
			if !deployment.Protocol.Qualified {
				report.ProtocolQualified = false
				break
			}
		}
		report.Healthy = report.Supported && report.ExpectedReplicas > 0 && report.CheckedReplicas == report.ExpectedReplicas && report.ProtocolQualified && report.Error == ""
	}
	return report
}

func writeMCPStatusReport(app string, report mcpCatalogStatusReport, update string) int {
	result := struct {
		App     string                 `json:"app"`
		Catalog mcpCatalogStatusReport `json:"catalog"`
	}{App: app, Catalog: report}
	if jsonOutput && update == "" {
		if code := jsonOut(writeJSON(result)); code != 0 {
			return code
		}
	} else if report.Supported {
		state := "degraded"
		if report.Healthy {
			state = "healthy"
		}
		if update != "" {
			_, _ = fmt.Fprintf(osStdout, "MCP status %s for %s\n", update, app)
		}
		writeMCPConvergence(report.Convergence)
		_, _ = fmt.Fprintf(osStdout, "MCP catalog for %s: %s (%d/%d health probes; %d/%d exact protocol checks across %d serving deployments)\n", app, state, report.CheckedReplicas, report.ExpectedReplicas, report.ProtocolCheckedReplicas, report.ProtocolExpectedReplicas, len(report.Deployments))
		for _, deployment := range report.Deployments {
			_, _ = fmt.Fprintf(osStdout, "  Deployment %s (%d%% traffic): %d/%d replicas", deployment.DeploymentID, deployment.TrafficPercent, deployment.Health.CheckedReplicas, deployment.Health.ExpectedReplicas)
			if deployment.Health.CatalogSHA256 != "" {
				_, _ = fmt.Fprintf(osStdout, " · SHA-256 %s", deployment.Health.CatalogSHA256)
			}
			_, _ = fmt.Fprintln(osStdout)
			if deployment.Admission != nil {
				_ = writeMCPAdmissionHistory(*deployment.Admission)
			}
			_, _ = fmt.Fprintf(osStdout, "    MCP protocol: %d/%d replicas qualified", deployment.Protocol.CheckedReplicas, deployment.Protocol.ExpectedReplicas)
			if deployment.Protocol.Error != "" {
				_, _ = fmt.Fprintf(osStdout, " · %s", deployment.Protocol.Error)
			}
			_, _ = fmt.Fprintln(osStdout)
			for _, replica := range deployment.Protocol.Replicas {
				for _, role := range replica.Roles {
					roleState := "qualified"
					if !role.Qualified {
						roleState = "failed"
					}
					_, _ = fmt.Fprintf(osStdout, "      %s / role %s: %s (%d tools, %d resources, %d templates, %d prompts)", replica.InstanceID, role.Name, roleState, role.Tools, role.Resources, role.ResourceTemplates, role.Prompts)
					if role.CatalogSHA256 != "" {
						_, _ = fmt.Fprintf(osStdout, " · catalog SHA-256 %s", role.CatalogSHA256)
					}
					if role.Error != "" {
						_, _ = fmt.Fprintf(osStdout, " · %s", role.Error)
					}
					_, _ = fmt.Fprintln(osStdout)
				}
			}
			for _, role := range deployment.Protocol.PublicRoles {
				roleState := "qualified"
				if !role.Qualified {
					roleState = "failed"
				}
				_, _ = fmt.Fprintf(osStdout, "      public endpoint / role %s: %s (%d tools, %d resources, %d templates, %d prompts)", role.Name, roleState, role.Tools, role.Resources, role.ResourceTemplates, role.Prompts)
				if role.CatalogSHA256 != "" {
					_, _ = fmt.Fprintf(osStdout, " · catalog SHA-256 %s", role.CatalogSHA256)
				}
				if role.Error != "" {
					_, _ = fmt.Fprintf(osStdout, " · %s", role.Error)
				}
				_, _ = fmt.Fprintln(osStdout)
			}
			for _, replica := range deployment.Health.Replicas {
				state := "healthy"
				if !replica.Healthy {
					state = "degraded"
				}
				_, _ = fmt.Fprintf(osStdout, "    %s: %s, revision %d, SHA-256 %s\n", replica.InstanceID, state, valueMCPRevision(replica.CatalogRevision), replica.CatalogSHA256)
			}
		}
		if report.Error != "" {
			_, _ = fmt.Fprintf(osStdout, "  Issue:    %s\n", report.Error)
		}
	} else {
		if update != "" {
			_, _ = fmt.Fprintf(osStdout, "MCP status %s for %s\n", update, app)
		}
		_, _ = fmt.Fprintf(osStdout, "MCP catalog integrity is unavailable for %s\n", app)
		if report.Error != "" {
			_, _ = fmt.Fprintf(osStdout, "  Issue: %s\n", report.Error)
		}
	}
	if !report.Supported || !report.Healthy {
		return 1
	}
	return 0
}

func runMCPStatusWatch(ctx context.Context, client *Client, app, endpoint string, roles []mcpReleaseRole, timeout, interval time.Duration) int {
	previousFingerprint := ""
	var previousReport mcpCatalogStatusReport
	for {
		pollCtx, cancel := context.WithTimeout(ctx, timeout)
		currentApp, report, err := mcpCollectCatalogStatus(pollCtx, client, app, endpoint, roles, timeout)
		cancel()
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			currentApp = app
			report = mcpCatalogStatusReport{Deployments: []mcpCatalogDeploymentReport{}, Error: err.Error()}
		}
		fingerprint := mcpStatusFingerprint(currentApp, report)
		if fingerprint != previousFingerprint {
			watchEvent := "change"
			var changed []string
			if previousFingerprint == "" {
				watchEvent = "snapshot"
			} else {
				changed = mcpStatusChangeKinds(previousReport, report)
			}
			if jsonOutput {
				event := mcpStatusWatchEvent{Event: watchEvent, Timestamp: time.Now().UTC(), App: currentApp, Changed: changed, Catalog: report}
				if err := json.NewEncoder(osStdout).Encode(event); err != nil {
					return printErr("MCP status watch output", err)
				}
			} else {
				label := "snapshot"
				if watchEvent == "change" {
					label = "changed (" + strings.Join(changed, ", ") + ")"
				}
				_ = writeMCPStatusReport(currentApp, report, label)
			}
			previousFingerprint = fingerprint
			previousReport = report
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0
		case <-timer.C:
		}
	}
}

func cmdMCPStatus(args []string) int {
	fs := newFlagSet("mcp-status", flag.ContinueOnError)
	appFlag := fs.String("app", "", "Gregale MCP app slug")
	endpointPath := fs.String("endpoint", "/mcp", "literal MCP endpoint path")
	tokenEnv := fs.String("token-env", "", "MCP client bearer token environment variable")
	releasePolicy := fs.String("release-policy", "", "role-specific catalog baselines and token environments")
	timeout := fs.Duration("timeout", 10*time.Second, "overall replica catalog health check deadline")
	watch := fs.Bool("watch", false, "poll and report MCP hosting health or catalog changes")
	interval := fs.Duration("interval", 30*time.Second, "poll interval when --watch is enabled")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || strings.TrimSpace(*appFlag) == "" || *timeout <= 0 || *timeout > 2*time.Minute || !api.ValidMCPProbeEndpointPath(*endpointPath) || *interval <= 0 || *interval > time.Hour {
		return printErr("Invalid MCP status flags", errors.New("--app, a literal --endpoint path, a positive --timeout no greater than 2m, and a positive --interval up to 1h are required"))
	}
	if *releasePolicy != "" && *tokenEnv != "" {
		return printErr("Invalid MCP status auth flags", errors.New("choose --token-env or --release-policy"))
	}
	var roles []mcpReleaseRole
	if *releasePolicy != "" {
		var err error
		roles, err = loadMCPReleasePolicy(*releasePolicy)
		if err != nil {
			return printErr("MCP release policy", err)
		}
	} else {
		token, err := mcpToken(*tokenEnv)
		if err != nil {
			return printErr("MCP client token", err)
		}
		roles = []mcpReleaseRole{{Name: "default", token: token}}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app := strings.TrimSpace(*appFlag)
	if *watch {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if !jsonOutput {
			_, _ = fmt.Fprintf(osStderr, "Watching MCP hosting status for %s every %s. Press Ctrl+C to stop.\n", app, interval.String())
		}
		return runMCPStatusWatch(ctx, client, app, *endpointPath, roles, *timeout, *interval)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	resolvedApp, report, err := mcpCollectCatalogStatus(ctx, client, app, *endpointPath, roles, *timeout)
	if err != nil {
		return printErr("MCP status", err)
	}
	return writeMCPStatusReport(resolvedApp, report, "")
}

func valueMCPRevision(revision *int64) int64 {
	if revision == nil {
		return 0
	}
	return *revision
}
