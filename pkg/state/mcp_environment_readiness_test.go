package state

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPEnvironmentReadinessPolicies(t *testing.T) {
	check := func(t *testing.T, s Store) {
		ctx := t.Context()
		a, e := s.CreateAccount(ctx, "scoped-readiness@example.com", api.PlanPro)
		if e != nil {
			t.Fatal(e)
		}
		app, e := s.CreateApp(ctx, App{AccountID: a.ID, Slug: "scoped-readiness", Type: AppTypeApp, Status: AppActive})
		if e != nil {
			t.Fatal(e)
		}
		source := s.(MCPReadinessStore)
		monitors := s.(MCPReadinessMonitorStore)
		deps := map[string]Deployment{}
		for _, scope := range []string{"default", "staging", "preview"} {
			d, e := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Scope: scope, Kind: DeploymentKindImage, ImageDigest: "sha256:" + scope, TrafficPercentExplicit: true, TrafficPercent: 0})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.MarkDeploymentLive(ctx, d.ID); e != nil {
				t.Fatal(e)
			}
			deps[scope] = d
		}
		request := api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "reviewed scope policy", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64), Monitor: &api.MCPReadinessMonitorConfig{Enabled: true, IntervalSeconds: 60, FailureThreshold: 2}}}}
		prod, e := source.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request)
		if e != nil {
			t.Fatal(e)
		}
		request.Scope = "staging"
		request.Endpoint = "/staging-mcp"
		request.Roles[0].CatalogSHA256 = strings.Repeat("b", 64)
		stage, e := source.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request)
		if e != nil {
			t.Fatal(e)
		}
		if prod.Revision == stage.Revision || prod.Scope != "default" || stage.Scope != "staging" {
			t.Fatal("policy identities not isolated")
		}
		subjects := map[string]MCPReadinessSubject{}
		for scope, d := range deps {
			sub, e := source.MCPReadinessSubject(ctx, a.ID, d.ID)
			if e != nil {
				t.Fatal(e)
			}
			subjects[scope] = sub
			if scope == "preview" {
				if sub.Policy.Revision != "" || sub.Policy.Enabled {
					t.Fatal("missing scope inherited default")
				}
				continue
			}
			role := sub.Policy.Roles[0]
			evidence := api.MCPReadinessEvidence{Role: role.Name, ProtocolVersion: role.ProtocolVersion, CatalogFormat: role.CatalogFormat, CatalogSHA256: role.CatalogSHA256, Passed: true}
			if _, e = source.RecordMCPReadiness(ctx, sub, evidence); e != nil {
				t.Fatal(e)
			}
		}
		if subjects["staging"].Policy.Endpoint != "/staging-mcp" || subjects["default"].Policy.Endpoint != "/mcp" {
			t.Fatal("wrong scoped expectations")
		}
		override, e := source.CreateMCPReadinessOverride(ctx, a.ID, deps["default"].ID, api.MCPReadinessOverrideRequest{ExpectedRevision: prod.Revision, Reason: "approved production recovery"})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = source.CreateMCPReadinessOverride(ctx, a.ID, deps["staging"].ID, api.MCPReadinessOverrideRequest{ExpectedRevision: prod.Revision, Reason: "wrong scope revision"}); !errors.Is(e, ErrConflict) {
			t.Fatal("cross-scope override", e)
		}
		request.ExpectedRevision = prod.Revision
		if _, e = source.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request); !errors.Is(e, ErrConflict) {
			t.Fatal("cross-scope CAS accepted", e)
		}
		request.ExpectedRevision = stage.Revision
		request.Roles[0].CatalogSHA256 = strings.Repeat("c", 64)
		changed, e := source.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request)
		if e != nil {
			t.Fatal(e)
		}
		current, e := source.MCPReadinessSubject(ctx, a.ID, deps["default"].ID)
		if e != nil {
			t.Fatal(e)
		}
		if current.Policy.Revision != prod.Revision || current.ArtifactSHA256 != subjects["default"].ArtifactSHA256 {
			t.Fatal("staging edit invalidated production")
		}
		evidence := api.MCPReadinessEvidence{Role: "reader", Passed: true, ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("b", 64)}
		if _, e = source.RecordMCPReadiness(ctx, subjects["staging"], evidence); !errors.Is(e, ErrConflict) {
			t.Fatal("stale staging attempt accepted", e)
		}
		for scope, d := range deps {
			snap, e := source.ListMCPReadiness(ctx, a.ID, app.ID, d.ID)
			if e != nil {
				t.Fatal(e)
			}
			if snap.Policy.Scope != scope {
				t.Fatal("deployment read used wrong policy", scope)
			}
			for _, item := range snap.Evidence {
				if item.DeploymentID != d.ID {
					t.Fatal("cross-scope evidence leaked")
				}
			}
		}
		if _, e = source.ListMCPReadiness(ctx, a.ID, app.ID, deps["staging"].ID, "default"); !errors.Is(e, ErrNotFound) {
			t.Fatal("mismatched list scope", e)
		}
		// Raw SQL checks avoid the legacy allocator redistributing sibling traffic.
		setTraffic := func(d Deployment, n int) error {
			if pg, ok := s.(*PgStore); ok {
				_, e := pg.pool.Exec(ctx, `UPDATE deployments SET traffic_percent=$2 WHERE id=$1`, d.ID, n)
				return e
			}
			m := s.(*MemStore)
			m.mu.Lock()
			defer m.mu.Unlock()
			previous := m.deployments[d.ID]
			if e := m.checkMCPReadinessLocked(previous, n); e != nil {
				return e
			}
			next := previous
			next.TrafficPercent = n
			m.consumeMCPReadinessOverrideLocked(previous, next)
			m.deployments[d.ID] = next
			return nil
		}
		if e = setTraffic(deps["staging"], 10); !IsMCPReadinessRequired(e) {
			t.Fatal("stale staging proof admitted traffic", e)
		}
		if e = setTraffic(deps["preview"], 10); !IsMCPReadinessRequired(e) {
			t.Fatal("missing environment policy admitted traffic", e)
		}
		if e = setTraffic(deps["default"], 10); e != nil {
			t.Fatal("production proof/override invalidated", e)
		}
		snap, e := source.ListMCPReadiness(ctx, a.ID, app.ID, "", "default")
		if e != nil {
			t.Fatal(e)
		}
		if len(snap.Overrides) != 1 || snap.Overrides[0].ID != override.ID {
			t.Fatal("production override lost")
		}
		request.Scope = "preview"
		request.Enabled = false
		request.ExpectedRevision = ""
		request.Roles = nil
		if _, e = source.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request); e != nil {
			t.Fatal(e)
		}
		if e = setTraffic(deps["preview"], 10); e != nil {
			t.Fatal("explicit environment opt-out ignored", e)
		}
		// Establish fresh staging proof before creating serving monitor subjects.
		fresh, e := source.MCPReadinessSubject(ctx, a.ID, deps["staging"].ID)
		if e != nil {
			t.Fatal(e)
		}
		evidence.CatalogSHA256 = strings.Repeat("c", 64)
		if _, e = source.RecordMCPReadiness(ctx, fresh, evidence); e != nil {
			t.Fatal(e)
		}
		if e = setTraffic(deps["staging"], 10); e != nil {
			t.Fatal(e)
		}
		jobs, e := monitors.ClaimMCPReadinessMonitors(ctx)
		if e != nil {
			t.Fatal(e)
		}
		found := map[string]bool{}
		for _, job := range jobs {
			found[job.Subject.Scope] = true
			if job.Subject.Scope == "staging" && job.Subject.Policy.Revision != changed.Revision {
				t.Fatal("monitor selected stale scope policy")
			}
		}
		if !found["default"] || !found["staging"] {
			t.Fatal("environment monitors missing", found)
		}
		statuses, e := monitors.ListMCPReadinessMonitoring(ctx, a.ID, app.ID, "", "staging")
		if e != nil || len(statuses) != 1 || statuses[0].Scope != "staging" {
			t.Fatal("monitor filter", statuses, e)
		}
		if _, e = source.GetMCPReadinessPolicy(ctx, a.ID, app.ID, "__all__"); !errors.Is(e, ErrInvalidArgument) {
			t.Fatal("invalid scope accepted", e)
		}
		if _, e = source.GetMCPReadinessPolicy(ctx, "foreign", app.ID, "staging"); !errors.Is(e, ErrNotFound) {
			t.Fatal("ownership bypass", e)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
