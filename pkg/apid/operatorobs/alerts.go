// Package operatorobs collects and projects bounded operator incident evidence.
package operatorobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const firingAlertsQuery = `ALERTS{alertstate="firing",alertname!="FaasWatchdog",severity!="info"} * ignoring(alertstate) group_left() ALERTS_FOR_STATE`

// FiringAlerts retains the persisted activation clock and existing label-based
// identities. Annotation failures degrade enrichment, not the firing signals.
func FiringAlerts(ctx context.Context, client *promql.Client) ([]api.ObsIncident, []string) {
	rows, err := client.QueryVector(ctx, firingAlertsQuery)
	if err != nil {
		return nil, []string{"prometheus"}
	}
	out := make([]api.ObsIncident, 0, len(rows))
	if len(rows) == 0 {
		return out, []string{}
	}
	metadata, metadataErr := client.Alerts(ctx)
	byLabels := make(map[string]promql.Alert, len(metadata))
	for _, alert := range metadata {
		if alert.State == "firing" {
			byLabels[alertMetadataKey(alert.Labels)] = alert
		}
	}
	unavailable := []string{}
	if metadataErr != nil {
		unavailable = append(unavailable, "prometheus_alert_metadata")
	}
	for _, row := range rows {
		if row.Labels["alertname"] == "" || row.Value <= 0 || row.Value > float64(time.Now().Add(time.Minute).Unix()) {
			return nil, []string{"prometheus"}
		}
		item := projectAlert(row)
		if alert, ok := byLabels[alertMetadataKey(row.Labels)]; ok {
			if link := safeRunbookURL(alert.Annotations["runbook_url"]); link != "" {
				item.RunbookURL = link
			}
		}
		out = append(out, item)
	}
	return out, unavailable
}

func projectAlert(row promql.VectorSample) api.ObsIncident {
	severity := "warning"
	switch row.Labels["severity"] {
	case "page", "critical":
		severity = "critical"
	case "error":
		severity = "error"
	}
	labels, _ := json.Marshal(row.Labels)
	key := fmt.Sprintf("platform_alert/%x", sha256.Sum256(labels))
	summary := row.Labels["alertname"]
	if component := row.Labels["component"]; component != "" {
		summary += " (" + component + ")"
	}
	item := api.ObsIncident{ID: key, Type: "platform_alert", Severity: severity, Status: "firing", Summary: summary,
		ResourceID: row.Labels["alertname"], ResourceName: summary, ObservedAt: time.UnixMilli(int64(row.Value * 1000)).UTC(),
		DedupeKey: key, ActionPath: "/v1/admin/obs/health", RunbookURL: "/docs/ops/alert-cookbook.md"}
	item.AccountID = labelUUID(row.Labels["account_id"])
	item.AppID = resourceLabelUUID(row.Labels, "app_id", "app")
	item.NodeID = resourceLabelUUID(row.Labels, "node_id", "node")
	if item.AppID != "" {
		item.ActionPath = "/v1/admin/obs/apps/" + item.AppID
	} else if item.NodeID != "" {
		item.ActionPath = EvidencePath("compute_node", item.NodeID)
	}
	if item.AccountID != "" {
		item.AuditPath = "/v1/admin/obs/audit-log/search?target_account_id=" + item.AccountID
	}
	item.EvidencePath = EvidencePath("platform_alert", strings.TrimPrefix(key, "platform_alert/"))
	return item
}

func resourceLabelUUID(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if id := labelUUID(labels[key]); id != "" {
			return id
		}
	}
	return ""
}

func labelUUID(raw string) string {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return ""
	}
	return id.String()
}

func alertMetadataKey(labels map[string]string) string {
	copyLabels := make(map[string]string, len(labels))
	for key, value := range labels {
		if key != "__name__" && key != "alertstate" {
			copyLabels[key] = value
		}
	}
	encoded, _ := json.Marshal(copyLabels)
	return string(encoded)
}

// Only repository documentation links are emitted. Annotations never cause a
// remote fetch or become an arbitrary scheme in an operator's terminal/browser.
func safeRunbookURL(raw string) string {
	if len(raw) > api.ObsAlertRunbookMaxBytes {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || strings.Contains(u.Path, "..") {
		return ""
	}
	if u.Scheme == "" && u.Host == "" && strings.HasPrefix(u.Path, "/docs/") {
		return raw
	}
	if u.Scheme == "https" && u.Host == "github.com" && strings.HasPrefix(u.Path, "/poyrazK/faas/blob/main/docs/") {
		return raw
	}
	return ""
}

// EvidencePath uses separate path segments for the inbox's type/resource ID.
func EvidencePath(kind, id string) string {
	return "/v1/admin/obs/incidents/" + kind + "/" + id + "/evidence"
}

// Enrich attaches the existing gated action paths to database incidents.
func Enrich(item *api.ObsIncident) {
	item.DedupeKey = strings.Replace(item.ID, "/", ":", 1)
	item.EvidencePath = EvidencePath(item.Type, item.ResourceID)
	if item.AccountID != "" {
		item.AuditPath = "/v1/admin/obs/audit-log/search?target_account_id=" + item.AccountID
	}
	switch item.Type {
	case "deployment":
		item.ActionPath = "/v1/admin/ops/deployments/" + item.ResourceID
		item.RunbookURL = "/docs/runbooks/FaasDeployFailed.md"
	case "job_run":
		item.ActionPath = "/v1/admin/ops/jobs/runs/" + item.ResourceID
		item.RunbookURL = "/docs/ops/gregalectl-operator-quickstart.md#job-run-incidents"
	case "compute_node":
		item.ActionPath = "/v1/admin/obs/nodes/" + url.PathEscape(item.ResourceName) + "/detail"
		item.AuditPath = "/v1/admin/obs/audit-log/search?kind_prefix=operator.action."
		item.RunbookURL = "/docs/runbooks/FaasComputeNodeStuckInactive.md"
	}
}
