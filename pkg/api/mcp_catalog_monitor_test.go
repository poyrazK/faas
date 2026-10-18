package api

import (
	"testing"
	"time"
)

func TestMCPCatalogMonitorSignalNeverClearsUnknown(t *testing.T) {
	now := time.Now().UTC()
	old := now.Add(-91 * time.Second)
	future := now.Add(time.Second)
	snapshot := MCPCatalogMonitorSnapshot{Config: MCPCatalogMonitorConfig{Enabled: true, Revision: "reviewed", FreshnessSeconds: 90}, CheckedAt: &now, Latest: &MCPCatalogReceipt{ID: "receipt", Revision: "reviewed", State: "drift", ChangedCount: 2}}
	if signal := CatalogMonitorSignal(snapshot, "app", now); signal.State != "drift" || signal.ReceiptID != "receipt" {
		t.Fatal("fresh drift lost")
	}
	for _, at := range []*time.Time{nil, &old, &future} {
		snapshot.CheckedAt = at
		if CatalogMonitorSignal(snapshot, "app", now).State != "unavailable" {
			t.Fatal("missing/stale/future observation became healthy")
		}
	}
	snapshot.CheckedAt = &now
	snapshot.Latest.Revision = "old"
	if CatalogMonitorSignal(snapshot, "app", now).State != "unavailable" {
		t.Fatal("old revision trusted")
	}
	snapshot.Config.Enabled = false
	if CatalogMonitorSignal(snapshot, "app", now).State != "disabled" {
		t.Fatal("disabled monitor evaluated")
	}
	for _, metric := range []string{MetricMCPCatalogDrift, MetricMCPCatalogMonitorUnavailable} {
		if !AlertRuleActionAllowedForMetric(metric, "webhook") || AlertRuleActionAllowedForMetric(metric, "rollback") || AlertRuleActionAllowedForMetric(metric, "promote") || AlertRuleActionAllowedForMetric(metric, "demote") {
			t.Fatal("unsafe catalog alert action allowed")
		}
	}
}

func TestMCPCatalogMonitorSummaryRoles(t *testing.T) {
	now := time.Now().UTC()
	role := func(name, state string) MCPCatalogMonitorSnapshot {
		return MCPCatalogMonitorSnapshot{Config: MCPCatalogMonitorConfig{Role: name, Enabled: true, Revision: name, FreshnessSeconds: 90}, CheckedAt: &now, Latest: &MCPCatalogReceipt{ID: name, Revision: name, State: state, ChangedCount: 2}}
	}
	snapshot := MCPCatalogMonitorSnapshot{Roles: map[string]MCPCatalogMonitorSnapshot{"reader": role("reader", "healthy"), "admin": role("admin", "drift"), "writer": role("writer", "unavailable")}}
	got := CatalogMonitorSummary(snapshot, "app", now)
	if got.State != "unavailable" || got.Signal.ChangedCount != 2 || len(got.Signal.DriftedRoles) != 1 || got.Signal.DriftedRoles[0] != "admin" || len(got.Signal.UnavailableRoles) != 1 {
		t.Fatalf("mixed health lost: %+v", got)
	}
	if got.Signal.Roles["admin"].EvidencePath != "/v1/apps/app/mcp-catalog-monitor?evidence=admin&role=admin" {
		t.Fatal("role-specific evidence missing")
	}
	writer := snapshot.Roles["writer"]
	writer.Config.Enabled = false
	snapshot.Roles["writer"] = writer
	if CatalogMonitorSummary(snapshot, "app", now).State != "drift" {
		t.Fatal("disabled role blocked remaining drift")
	}
	admin := snapshot.Roles["admin"]
	admin.Latest.State = "healthy"
	snapshot.Roles["admin"] = admin
	if CatalogMonitorSummary(snapshot, "app", now).State != "healthy" {
		t.Fatal("all enabled roles did not recover")
	}
	old := now.Add(-91 * time.Second)
	admin.CheckedAt = &old
	snapshot.Roles["admin"] = admin
	if CatalogMonitorSummary(snapshot, "app", now).State != "unavailable" {
		t.Fatal("healthy reader concealed stale admin")
	}
	for _, invalid := range []string{"Admin", "../admin", "a b", string(make([]byte, 33))} {
		if ValidCatalogMonitorRole(invalid) {
			t.Fatal("invalid role accepted")
		}
	}
	if !ValidCatalogMonitorRole("") || !ValidCatalogMonitorTokenEnv("MCP_ADMIN_TOKEN") || ValidCatalogMonitorTokenEnv("Bearer secret") {
		t.Fatal("reference validation failed")
	}
}
