package state_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestMCPConnectionHealthClassificationAndPagination(t *testing.T) {
	at := time.Now().UTC()
	future := at.Add(time.Hour)
	expired := at.Add(-time.Minute)
	rows := []state.McpHealthRow{
		{AppID: "app", AppSlug: "app", Role: "reader", Guided: true, Enabled: true, OAuthState: "reauthorization_required"},
		{AppID: "app", AppSlug: "app", Role: "writer", Enabled: true, JournalState: "failed"},
		{AppID: "app", AppSlug: "app", Role: "paused", Enabled: true, Paused: true, OAuthState: "connected", ExpiresAt: &expired},
		{AppID: "app", AppSlug: "app", Role: "unknown", JournalState: "reauthorization_required", JournalOutcome: "issuance_unknown"},
		{AppID: "deleted", AppSlug: "deleted", Role: "reader", RetirementID: "retired", RetirementState: "held", RetainUntil: &future, AppDeleted: true},
	}
	result := state.ConnectionHealthSnapshot(rows, true, 0, at)
	if result.TotalIssues != 4 || result.Counts["reauthorization_required"] != 1 || result.Counts["renewal_failed"] != 1 || result.Counts["retirement_expiring"] != 1 || result.Counts["issuance_unknown"] != 1 {
		t.Fatal("wrong connection health counts", result.Counts)
	}
	for _, issue := range result.Items {
		if issue.RecoveryPath == "" || issue.RecoveryAction == "" {
			t.Fatal("missing recovery controls")
		}
	}
	bytes, _ := json.Marshal(result)
	if strings.Contains(string(bytes), "Fingerprints") || strings.Contains(string(bytes), "private_") {
		t.Fatal("internal health data exposed")
	}
	original := result.Fingerprints["renewal_failed"]
	rows[1].LeaseUntil = &future
	if state.ConnectionHealthSnapshot(rows, true, 0, at.Add(time.Second)).Fingerprints["renewal_failed"] != original {
		t.Fatal("poll timestamps changed incident identity")
	}
	for i := 0; i < 60; i++ {
		rows = append(rows, state.McpHealthRow{AppID: uuid.NewString(), AppSlug: "app", Role: "reader", OAuthState: "reauthorization_required"})
	}
	first := state.ConnectionHealthSnapshot(rows, true, 0, at)
	second := state.ConnectionHealthSnapshot(rows, true, first.NextOffset, at)
	if len(first.Items) != api.MCPConnectionHealthPageSize || first.NextOffset == 0 || len(second.Items) == 0 || first.TotalIssues != 64 || first.Counts["reauthorization_required"] != 61 {
		t.Fatal("page truncated counts")
	}
	if first.Fingerprints["reauthorization_required"] != second.Fingerprints["reauthorization_required"] {
		t.Fatal("pagination changed incident identity")
	}
}
func TestMCPConnectionHealthIncompleteAndTenantIsolation(t *testing.T) {
	m := state.NewMemStore()
	(*m.McpCatalogMonitors()) = map[string]api.MCPCatalogMonitorSnapshot{}
	(*m.McpCatalogMonitorOwners()) = map[string]string{}
	for i := 0; i <= api.MCPConnectionHealthSourceLimit; i++ {
		id := uuid.NewString()
		(*m.Apps())[id] = state.App{ID: id, AccountID: "account", Slug: "app"}
		(*m.McpCatalogMonitorOwners())[id] = "account"
		(*m.McpCatalogMonitors())[id] = api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: uuid.NewString(), Observer: &api.MCPCatalogObserverConfig{OAuth: &api.MCPCatalogObserverOAuthConfig{}}}, OAuthLifecycle: &api.MCPCatalogOAuthLifecycle{CredentialState: "reconnect_required"}}
	}
	snapshot, err := m.MCPConnectionHealth(t.Context(), "account", "", 0, time.Now())
	if err != nil || snapshot.Complete || snapshot.TotalIssues != api.MCPConnectionHealthSourceLimit {
		t.Fatal("large inventory claimed completeness", err)
	}
	other, err := m.MCPConnectionHealth(t.Context(), "other", "", 0, time.Now())
	if err != nil || !other.Complete || other.TotalIssues != 0 {
		t.Fatal("health crossed account ownership", err)
	}
	for id := range *m.Apps() {
		filtered, e := m.MCPConnectionHealth(t.Context(), "account", id, 0, time.Now())
		if e != nil || !filtered.Complete || filtered.TotalIssues != 1 {
			t.Fatal("app filter failed", e)
		}
		break
	}
}
