package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessOAuthStore(t *testing.T) {
	check := func(t *testing.T, s Store) {
		ctx := t.Context()
		account, err := s.CreateAccount(ctx, "readiness-oauth@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "readiness-oauth", Type: AppTypeApp, Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
		stable, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:stable"})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, stable.ID); err != nil {
			t.Fatal(err)
		}
		candidate, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:candidate", TrafficPercentExplicit: true, TrafficPercent: 0})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
			t.Fatal(err)
		}
		catalog := s.(MCPCatalogMonitorStore)
		observers := s.(MCPCatalogObserverStore)
		oauth := s.(MCPReadinessOAuthStore)
		readiness := s.(MCPReadinessStore)
		cfg := api.MCPCatalogMonitorConfig{Role: "reader", Revision: uuid.NewString(), Enabled: true, FreshnessSeconds: 300, Observer: &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Endpoint: "/mcp", OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader-client"}}}
		if _, err = catalog.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); err != nil {
			t.Fatal(err)
		}
		job, err := observers.ClaimMCPCatalogObserver(ctx, MCPCatalogObserverJob{AccountID: account.ID, AppID: app.ID, Role: "reader", Snapshot: api.MCPCatalogMonitorSnapshot{Config: cfg}})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		expires := now.Add(time.Hour)
		auth := api.MCPCatalogObserverOAuthStatus{State: "connected", SourceSHA256: strings.Repeat("b", 64), Ciphertext: []byte("opaque test grant"), ExpiresAt: &expires, LastRenewalAt: &now}
		if err = observers.SaveMCPCatalogObserverOAuth(ctx, job, auth); err != nil {
			t.Fatal(err)
		}
		if _, err = observers.CompleteMCPCatalogObserver(ctx, job, api.MCPCatalogReceipt{ID: uuid.NewString(), Role: "reader", Revision: cfg.Revision, State: "healthy"}, "", 10*time.Minute); err != nil {
			t.Fatal(err)
		}
		before, err := catalog.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		view, _ := api.CatalogMonitorRoleSnapshot(before, "reader")
		next := *view.ObserverStatus.NextCheckAt
		ref := api.MCPReadinessOAuthConnection{Role: "reader", Revision: cfg.Revision}
		request := api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "managed OAuth readiness", Roles: []api.MCPReadinessRole{{Name: "probe", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64), Monitor: &api.MCPReadinessMonitorConfig{Enabled: true, IntervalSeconds: 60, FailureThreshold: 2, OAuthConnection: &ref}}}}
		if _, err = readiness.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request); err != nil {
			t.Fatal(err)
		}
		subject, err := readiness.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		lease, err := oauth.ClaimMCPReadinessOAuth(ctx, account.ID, app.ID, ref, "/mcp")
		if err != nil {
			t.Fatal("credential claim should ignore future catalog schedule", err)
		}
		if _, err = oauth.ClaimMCPReadinessOAuth(ctx, account.ID, app.ID, ref, "/mcp"); !errors.Is(err, ErrMCPReadinessOAuthBusy) {
			t.Fatal("connection lease duplicated", err)
		}
		if _, err = observers.ClaimMCPCatalogObserver(ctx, job); !errors.Is(err, ErrMCPCatalogMonitorChanged) {
			t.Fatal("catalog observer stole credential lease", err)
		}
		auth.Ciphertext = []byte("rotated opaque grant")
		later := expires.Add(time.Hour)
		auth.ExpiresAt = &later
		if err = observers.SaveMCPCatalogObserverOAuth(ctx, lease, auth); err != nil {
			t.Fatal(err)
		}
		rotated, err := readiness.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if err != nil || rotated.ArtifactSHA256 != subject.ArtifactSHA256 {
			t.Fatal("ordinary token rotation invalidated grant identity", err)
		}
		evidence := api.MCPReadinessEvidence{Role: "probe", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64), Passed: true}
		if _, err = readiness.RecordMCPReadiness(ctx, subject, evidence); err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateDeploymentTraffic(ctx, candidate.ID, 10); err != nil {
			t.Fatal("fresh proof blocked", err)
		}
		auth.State = "reauthorization_required"
		if err = observers.SaveMCPCatalogObserverOAuth(ctx, lease, auth); err != nil {
			t.Fatal(err)
		}
		if _, err = readiness.RecordMCPReadiness(ctx, subject, evidence); !errors.Is(err, ErrConflict) {
			t.Fatal("connection changed during probe was accepted", err)
		}
		if _, err = s.UpdateDeploymentTraffic(ctx, candidate.ID, 20); !IsMCPReadinessRequired(err) {
			t.Fatal("changed connection reused old proof", err)
		}
		if pg, ok := s.(*PgStore); ok {
			if _, err = pg.pool.Exec(ctx, `UPDATE deployments SET traffic_percent=20 WHERE id=$1`, candidate.ID); !IsMCPReadinessRequired(err) {
				t.Fatal("raw traffic update bypassed connection fence", err)
			}
		}
		if err = oauth.ReleaseMCPReadinessOAuth(ctx, lease); err != nil {
			t.Fatal(err)
		}
		after, err := catalog.GetMCPCatalogMonitor(ctx, account.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		view, _ = api.CatalogMonitorRoleSnapshot(after, "reader")
		if view.ObserverStatus.NextCheckAt == nil || !view.ObserverStatus.NextCheckAt.Equal(next) {
			t.Fatal("readiness credential use changed catalog cadence")
		}
		if _, err = oauth.ClaimMCPReadinessOAuth(ctx, account.ID, app.ID, ref, "/mcp"); !errors.Is(err, ErrMCPReadinessOAuthReconnect) {
			t.Fatal("revoked grant not surfaced", err)
		}
		if _, err = oauth.ClaimMCPReadinessOAuth(ctx, account.ID, app.ID, ref, "/other"); !errors.Is(err, ErrMCPReadinessOAuthChanged) {
			t.Fatal("connection allowed another endpoint", err)
		}
		if _, err = oauth.ClaimMCPReadinessOAuth(ctx, uuid.NewString(), app.ID, ref, "/mcp"); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign account obtained a credential lease", err)
		}

		policyBefore, err := readiness.GetMCPReadinessPolicy(ctx, account.ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Revision = uuid.NewString()
		if _, err = catalog.ConfigureMCPCatalogMonitor(ctx, account.ID, app.ID, cfg); err != nil {
			t.Fatal(err)
		}
		history := s.(MCPReadinessPolicyHistoryStore)
		if _, err = history.RestoreMCPReadinessPolicy(ctx, account.ID, app.ID, policyBefore.Revision, api.RestoreMCPReadinessPolicyRequest{ExpectedRevision: policyBefore.Revision, Reason: "review prior OAuth policy"}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("restore accepted obsolete OAuth binding", err)
		}
		policyAfter, err := readiness.GetMCPReadinessPolicy(ctx, account.ID, app.ID)
		if err != nil || policyAfter.Revision != policyBefore.Revision {
			t.Fatal("failed restore changed policy", err)
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}

// Compile-time checks keep this optional contract available on both stores.
var _ MCPReadinessOAuthStore = (*MemStore)(nil)
var _ MCPReadinessOAuthStore = (*PgStore)(nil)
