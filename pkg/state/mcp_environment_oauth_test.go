package state_test

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestMCPEnvironmentOAuthIsolation(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		ctx := t.Context()
		account, err := s.CreateAccount(ctx, "environment-oauth@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "environment-oauth", Type: state.AppTypeApp, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		catalog := s.(state.MCPCatalogMonitorStore)
		observer := s.(state.MCPCatalogObserverStore)
		creds := s.(state.MCPReadinessOAuthStore)
		readiness := s.(state.MCPReadinessStore)
		refs := map[string]api.MCPReadinessOAuthConnection{}
		for _, scope := range []string{"default", "staging"} {
			if err = s.UpsertAppSecretInScope(ctx, account.ID, app.ID, scope, "REFRESH", []byte("opaque source")); err != nil {
				t.Fatal(err)
			}
			cfg := api.MCPCatalogMonitorConfig{Scope: scope, Role: scope + "-reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 300, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: scope + "-client", RefreshTokenSecretRef: "secret:REFRESH"}}}
			if _, err = catalog.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); err != nil {
				t.Fatal("same secret name in distinct environments", err)
			}
			job, e := observer.ClaimMCPCatalogObserver(ctx, state.MCPCatalogObserverJob{AccountID: account.ID, AppID: app.ID, Role: cfg.Role, Snapshot: api.MCPCatalogMonitorSnapshot{Config: cfg}})
			if e != nil {
				t.Fatal(e)
			}
			now := time.Now().UTC()
			expires := now.Add(time.Hour)
			auth := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: strings.Repeat("a", 64), Ciphertext: []byte("opaque grant " + scope), ExpiresAt: &expires, LastRenewalAt: &now}
			if e = observer.SaveMCPCatalogObserverOAuth(ctx, job, auth); e != nil {
				t.Fatal(e)
			}
			if e = creds.ReleaseMCPReadinessOAuth(ctx, job); e != nil {
				t.Fatal(e)
			}
			refs[scope] = api.MCPReadinessOAuthConnection{Scope: scope, Role: cfg.Role, Revision: cfg.Revision}
			cfg.Scope = "other"
			if _, e = catalog.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); !errors.Is(e, state.ErrInvalidArgument) {
				t.Fatal("moved grant to another environment", e)
			}
		}
		bad := refs["default"]
		bad.Scope = "staging"
		if _, err = creds.ClaimMCPReadinessOAuth(ctx, account.ID, app.ID, bad, "/mcp"); !errors.Is(err, state.ErrMCPReadinessOAuthChanged) {
			t.Fatal("cross-environment claim", err)
		}
		leases := map[string]state.MCPCatalogObserverJob{}
		for scope, ref := range refs {
			job, e := creds.ClaimMCPReadinessOAuth(ctx, account.ID, app.ID, ref, "/mcp")
			if e != nil {
				t.Fatal(e)
			}
			leases[scope] = job
		}
		policy := api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "isolated environments", Roles: []api.MCPReadinessRole{{Name: "probe", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("b", 64), Monitor: &api.MCPReadinessMonitorConfig{Enabled: true, IntervalSeconds: 60, FailureThreshold: 1, OAuthConnections: refs}}}}
		if _, err = readiness.SetMCPReadinessPolicy(ctx, account.ID, app.ID, policy); err != nil {
			t.Fatal(err)
		}
		policy.Scope = "staging"
		if _, err = readiness.SetMCPReadinessPolicy(ctx, account.ID, app.ID, policy); err != nil {
			t.Fatal(err)
		}
		subjects := map[string]state.MCPReadinessSubject{}
		for _, scope := range []string{"default", "staging"} {
			d, e := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + scope, TrafficPercentExplicit: true, TrafficPercent: 0})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.MarkDeploymentLive(ctx, d.ID); e != nil {
				t.Fatal(e)
			}
			sub, e := readiness.MCPReadinessSubject(ctx, account.ID, d.ID)
			if e != nil {
				t.Fatal(e)
			}
			subjects[scope] = sub
			evidence := api.MCPReadinessEvidence{Role: "probe", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("b", 64), Passed: true}
			if _, e = readiness.RecordMCPReadiness(ctx, sub, evidence); e != nil {
				t.Fatal(e)
			}
		}
		// Rotate a staging source while both credential leases are live. Only the
		// staging lease/proof is fenced; production remains usable.
		if err = s.UpsertAppSecretInScope(ctx, account.ID, app.ID, "staging", "REFRESH", []byte("replacement source")); err != nil {
			t.Fatal(err)
		}
		for scope, old := range subjects {
			current, e := readiness.MCPReadinessSubject(ctx, account.ID, old.DeploymentID)
			if e != nil {
				t.Fatal(e)
			}
			changed := current.ArtifactSHA256 != old.ArtifactSHA256
			if changed != (scope == "staging") {
				t.Fatal("environment proof isolation", scope, changed)
			}
			evidence := api.MCPReadinessEvidence{Role: "probe", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("b", 64), Passed: true}
			if scope == "default" {
				old = current
			}
			_, e = readiness.RecordMCPReadiness(ctx, old, evidence)
			if scope == "staging" && !errors.Is(e, state.ErrConflict) || scope == "default" && e != nil {
				t.Fatal("evidence fence", scope, e)
			}
		}
		if err = creds.ReleaseMCPReadinessOAuth(ctx, leases["default"]); err != nil {
			t.Fatal("production lease changed", err)
		}
		if err = creds.ReleaseMCPReadinessOAuth(ctx, leases["staging"]); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
			t.Fatal("staging lease survived source change", err)
		}
		root, e := catalog.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
		if e != nil {
			t.Fatal(e)
		}
		stage, _ := api.CatalogMonitorRoleSnapshot(root, "staging-reader")
		prod, _ := api.CatalogMonitorRoleSnapshot(root, "default-reader")
		if stage.ObserverStatus.OAuth != nil || prod.ObserverStatus.OAuth == nil {
			t.Fatal("source mutation changed wrong environment grant")
		}
		if _, e = s.UpdateDeploymentTraffic(ctx, subjects["staging"].DeploymentID, 10); !state.IsMCPReadinessRequired(e) {
			t.Fatal("old staging proof authorized traffic", e)
		}
		if _, e = s.UpdateDeploymentTraffic(ctx, subjects["default"].DeploymentID, 100); e != nil {
			t.Fatal("staging invalidated production traffic proof", e)
		}
		if pg, ok := s.(*state.PgStore); ok {
			_, err = (*pg.Pool()).Exec(ctx, `UPDATE deployments SET traffic_percent=10 WHERE id=$1`, subjects["staging"].DeploymentID)
			if !state.IsMCPReadinessRequired(err) {
				t.Fatal("raw SQL reused stale environment proof", err)
			}
			_, err = (*pg.Pool()).Exec(ctx, `UPDATE mcp_catalog_monitors SET payload=jsonb_set(payload,'{roles,default-reader,config,scope}','"staging"') WHERE app_id=$1`, app.ID)
			if err == nil {
				t.Fatal("SQL moved environment binding")
			}
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func TestMCPEnvironmentOAuthRetirementScope(t *testing.T) {
	for _, scope := range []string{"", "default", "staging"} {
		record := api.MCPOAuthRetirement{ID: uuid.NewString(), AppID: uuid.NewString(), Role: "reader", Scope: scope, Revocation: &api.MCPCatalogOAuthRevocation{State: "pending"}}
		job := state.RetirementJob(uuid.NewString(), record)
		if api.MCPConnectionScope(job.Snapshot.Config.Scope) != api.MCPConnectionScope(scope) {
			t.Fatal("retirement worker lost environment scope", scope)
		}
	}
}
