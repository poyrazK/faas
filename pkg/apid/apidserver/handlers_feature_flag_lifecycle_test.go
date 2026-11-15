package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR 20261114150000 §4: the lifecycle report and its deduplicated audit
// events.
func TestFeatureFlagLifecycleReportAndAttentionEvents(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*e.S()).EnablePlatformFlagForAll(platformflags.CustomerFlags)
	ctx := context.Background()
	acct := (*e.Acct()).ID
	project, err := (*e.Store()).CreateProject(ctx, state.Project{AccountID: acct, Slug: "flags", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/projects/flags/environments/production/flags"
	zero := int64(0)
	cfg := flags.Config{Flags: []flags.Flag{
		{Key: "orphan", Enabled: true},
		{Key: "owned", Enabled: true, Owner: "payments", ExpiresAt: "2020-01-01"},
	}}
	if res := e.Do(t, http.MethodPut, path, apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &zero, Config: cfg}, nil); res.Code != 200 {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	bad := flags.Config{Flags: []flags.Flag{{Key: "x", Enabled: true, ExpiresAt: "tomorrow"}}}
	one := int64(1)
	if res := e.Do(t, http.MethodPut, path, apidserver.UpdateFeatureFlagsRequest{ExpectedVersion: &one, Config: bad}, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("invalid expires_at accepted: %d", res.Code)
	}

	res := e.Do(t, http.MethodGet, path+"/lifecycle", nil, nil)
	if res.Code != 200 {
		t.Fatalf("lifecycle: %d %s", res.Code, res.Body.String())
	}
	var report struct {
		ConfigVersion int64                 `json:"config_version"`
		Flags         []flags.FlagLifecycle `json:"flags"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil || report.ConfigVersion != 1 || len(report.Flags) != 2 {
		t.Fatalf("report = %s (%v)", res.Body.String(), err)
	}
	byKey := map[string]flags.FlagLifecycle{}
	for _, f := range report.Flags {
		byKey[f.Key] = f
	}
	if f := byKey["orphan"]; !f.ServesOneValue || !slices.Contains(f.Attention, flags.AttentionNoOwner) || f.LastChangedVersion != 1 {
		t.Errorf("orphan = %+v", f)
	}
	if f := byKey["owned"]; f.Owner != "payments" || !slices.Equal(f.Attention, []string{flags.AttentionExpired}) {
		t.Errorf("owned = %+v", f)
	}

	env, err := (*e.Store()).ProjectEnvironmentBySlug(ctx, acct, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	candidate := state.FeatureFlagAutoRolloutCandidate{
		Scope:       state.FeatureFlagScope{AccountID: acct, ProjectID: project.ID, EnvironmentID: env.ID},
		ProjectSlug: "flags", EnvironmentSlug: "production",
	}
	later := time.Now().Add(30 * 24 * time.Hour)
	apidserver.RunFeatureFlagLifecycleCheck(*e.S(), candidate, later)
	apidserver.RunFeatureFlagLifecycleCheck(*e.S(), candidate, later)

	var outbox state.AuditEventOutboxStore = *e.Store()
	var reasons []string
	for {
		event, err := outbox.ClaimAuditEvent(ctx, "test", time.Minute)
		if err != nil || event.ID == 0 {
			break
		}
		if event.Kind != "flags.lifecycle_attention" {
			continue
		}
		var data map[string]any
		_ = json.Unmarshal(event.Data, &data)
		reasons = append(reasons, data["flag"].(string)+":"+data["reason"].(string))
	}
	slices.Sort(reasons)
	want := []string{"orphan:no_owner", "orphan:stale", "owned:expired", "owned:stale"}
	if !slices.Equal(reasons, want) {
		t.Fatalf("attention events = %v, want %v (once each)", reasons, want)
	}
}
