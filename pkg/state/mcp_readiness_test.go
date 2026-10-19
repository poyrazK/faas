package state

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessStoreRoundTrip(t *testing.T) {
	check := func(t *testing.T, s Store, kind DeploymentKind) {
		ctx := context.Background()
		source := s.(MCPReadinessStore)
		account, err := s.CreateAccount(ctx, "readiness-gate@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, App{AccountID: account.ID, Slug: "readiness-gate", Type: AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: AppActive})
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
		buildURLs := s.(PublicBuildURLStore)
		if kind == DeploymentKindTarball {
			if err := buildURLs.SetAppPublicBuildURLs(ctx, account.ID, app.ID, map[string]string{"VITE_API_URL": "https://api.example.com/v1"}); err != nil {
				t.Fatal(err)
			}
		}
		candidate, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: kind, SourcePath: "/tmp/readiness-source.tar.gz", ImageDigest: "sha256:candidate", TrafficPercentExplicit: true, TrafficPercent: 0})
		if err != nil {
			t.Fatal(err)
		}
		if kind == DeploymentKindTarball {
			if err := buildURLs.SetAppPublicBuildURLs(ctx, account.ID, app.ID, map[string]string{"VITE_API_URL": "https://new-api.example.com"}); err != nil {
				t.Fatal(err)
			}
		}
		if err = s.MarkDeploymentLive(ctx, candidate.ID); err != nil {
			t.Fatal(err)
		}
		request := api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "Require reviewed reader probe", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64)}}}
		policy, err := source.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		blocked := func() {
			t.Helper()
			_, err := s.UpdateDeploymentTraffic(ctx, candidate.ID, 20)
			if !IsMCPReadinessRequired(err) {
				t.Fatalf("expected readiness hold, got %v", err)
			}
		}

		if pg, ok := s.(*PgStore); ok {
			if _, e := pg.pool.Exec(ctx, `UPDATE deployments SET traffic_percent=100 WHERE id=$1`, candidate.ID); !IsMCPReadinessRequired(e) {
				t.Fatalf("raw SQL bypassed readiness: %v", e)
			}
		}
		blocked()
		if _, err := source.GetMCPReadinessPolicy(ctx, "foreign-account", app.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign policy visible")
		}
		subject, err := source.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		failed := api.MCPReadinessEvidence{Role: "reader", Passed: false, Checks: []api.MCPReadinessCheck{{Name: "ping", Status: "failed", Detail: "Ping failed"}}}
		if _, err = source.RecordMCPReadiness(ctx, subject, failed); err != nil {
			t.Fatal(err)
		}
		passed := api.MCPReadinessEvidence{Role: "reader", Passed: true, ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64), Checks: []api.MCPReadinessCheck{{Name: "ping", Status: "passed"}}}
		if _, e := source.RecordMCPReadiness(ctx, subject, passed); !errors.Is(e, ErrConflict) {
			t.Fatalf("equal-time success replaced a failure: %v", e)
		}
		older := subject
		older.CapturedAt = older.CapturedAt.Add(-time.Second)
		if _, err = source.RecordMCPReadiness(ctx, older, passed); !errors.Is(err, ErrConflict) {
			t.Fatalf("older success replaced failure: %v", err)
		}
		blocked()
		override, err := source.CreateMCPReadinessOverride(ctx, account.ID, candidate.ID, api.MCPReadinessOverrideRequest{ExpectedRevision: policy.Revision, Reason: "Emergency incident recovery"})
		if err != nil || override.ID == "" {
			t.Fatalf("override: %+v %v", override, err)
		}
		if _, e := source.CreateMCPReadinessOverride(ctx, account.ID, candidate.ID, api.MCPReadinessOverrideRequest{ExpectedRevision: policy.Revision, Reason: "Repeated approval request"}); !errors.Is(e, ErrConflict) {
			t.Fatalf("duplicate pending override accepted: %v", e)
		}
		if _, err = s.UpdateDeploymentTraffic(ctx, candidate.ID, 10); err != nil {
			t.Fatalf("override promotion: %v", err)
		}
		snapshot, err := source.ListMCPReadiness(ctx, account.ID, app.ID, candidate.ID)
		if err != nil || len(snapshot.Overrides) != 1 || snapshot.Overrides[0].UsedAt == nil || len(snapshot.Evidence) != 1 {
			t.Fatalf("audit: %+v %v", snapshot, err)
		}
		if kind == DeploymentKindTarball {
			urls, err := buildURLs.DeploymentPublicBuildURLs(ctx, candidate.ID)
			if err != nil || urls["VITE_API_URL"] != "https://api.example.com/v1" {
				t.Fatalf("readiness override promotion changed the captured build URLs: %v %v", urls, err)
			}
		}
		blocked() // The same override cannot authorize the next increase.
		oldSubject, err := source.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		request.ExpectedRevision = policy.Revision
		request.Reason = "Reconfigure readiness"
		policy, err = source.SetMCPReadinessPolicy(ctx, account.ID, app.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = source.RecordMCPReadiness(ctx, oldSubject, passed); !errors.Is(err, ErrConflict) {
			t.Fatalf("old policy report accepted: %v", err)
		}
		subject, err = source.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = source.RecordMCPReadiness(ctx, subject, passed); err != nil {
			t.Fatal(err)
		}
		// Age the persisted evidence, independently of API request timestamps.
		switch store := s.(type) {
		case *MemStore:
			store.mu.Lock()
			e := store.mcpReadiness.evidence[candidate.ID+"/reader"]
			e.ObservedAt = time.Now().Add(-10 * time.Minute)
			store.mcpReadiness.evidence[candidate.ID+"/reader"] = e
			store.mu.Unlock()
		case *PgStore:
			if _, err = store.pool.Exec(ctx, `UPDATE deployment_mcp_readiness_evidence SET observed_at=clock_timestamp()-interval '10 minutes' WHERE deployment_id=$1`, candidate.ID); err != nil {
				t.Fatal(err)
			}
		}
		blocked()
		subject, err = source.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = source.RecordMCPReadiness(ctx, subject, passed); err != nil {
			t.Fatal(err)
		}
		switch store := s.(type) {
		case *MemStore:
			store.mu.Lock()
			d := store.deployments[candidate.ID]
			d.ImageDigest = "sha256:changed"
			store.deployments[candidate.ID] = d
			store.mu.Unlock()
		case *PgStore:
			if _, err = store.pool.Exec(ctx, `UPDATE deployments SET image_digest='sha256:changed' WHERE id=$1`, candidate.ID); err != nil {
				t.Fatal(err)
			}
		}
		blocked()
		if _, err = source.RecordMCPReadiness(ctx, subject, passed); !errors.Is(err, ErrConflict) {
			t.Fatalf("old artifact report accepted: %v", err)
		}
		subject, err = source.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = source.RecordMCPReadiness(ctx, subject, passed); err != nil {
			t.Fatal(err)
		}

		// App-level runtime configuration is bound just like deployment overrides.
		oldConfigSubject, configErr := source.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if configErr != nil {
			t.Fatal(configErr)
		}
		switch store := s.(type) {
		case *MemStore:
			store.mu.Lock()
			configured := store.apps[app.ID]
			configured.Manifest.Env = map[string]string{"MCP_RUNTIME_SETTING": "changed"}
			store.apps[app.ID] = configured
			store.mu.Unlock()
		case *PgStore:
			if _, err = store.pool.Exec(ctx, `UPDATE apps SET manifest=manifest || '{"env":{"MCP_RUNTIME_SETTING":"changed"}}'::jsonb WHERE id=$1`, app.ID); err != nil {
				t.Fatal(err)
			}
		}
		blocked()
		if _, err = source.RecordMCPReadiness(ctx, oldConfigSubject, passed); !errors.Is(err, ErrConflict) {
			t.Fatalf("old app configuration accepted: %v", err)
		}
		subject, err = source.MCPReadinessSubject(ctx, account.ID, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = source.RecordMCPReadiness(ctx, subject, passed); err != nil {
			t.Fatal(err)
		}
		if _, err = s.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err != nil {
			t.Fatalf("fresh evidence promotion: %v", err)
		}
		automatic, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:automatic"})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, automatic.ID); !IsMCPReadinessRequired(err) {
			t.Fatalf("automatic activation bypassed readiness: %v", err)
		}
		live, err := s.DeploymentByID(ctx, candidate.ID)
		if err != nil || live.TrafficPercent != 100 || live.Status != DeployLive {
			t.Fatalf("failed activation changed serving deployment: %+v %v", live, err)
		}
	}
	for _, kind := range []DeploymentKind{DeploymentKindImage, DeploymentKindTarball} {
		t.Run(string(kind), func(t *testing.T) {
			t.Run("memory", func(t *testing.T) { check(t, NewMemStore(), kind) })
			t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t)), kind) })
		})
	}
}

func TestMCPReadinessAlternativeMemoryPaths(t *testing.T) {
	ctx := t.Context()
	s := NewMemStore()
	a, err := s.CreateAccount(ctx, "alternative-readiness@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{AccountID: a.ID, Slug: "alternative-readiness", Type: AppTypeApp, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:old"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:new"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, current.ID); err != nil {
		t.Fatal(err)
	}
	p, err := s.SetMCPReadinessPolicy(ctx, a.ID, app.ID, api.SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "Require release readiness", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AutoRollbackDeploymentsTx(ctx, app.ID, current.ID); !IsMCPReadinessRequired(err) {
		t.Fatalf("automatic rollback bypassed readiness: %v", err)
	}
	if _, err = s.CreateMCPReadinessOverride(ctx, a.ID, stable.ID, api.MCPReadinessOverrideRequest{ExpectedRevision: p.Revision, Reason: "Emergency return to stable revision"}); err != nil {
		t.Fatal(err)
	}
	if target, e := s.AutoRollbackDeploymentsTx(ctx, app.ID, current.ID); e != nil || target != stable.ID {
		t.Fatalf("audited rollback: %s %v", target, e)
	}
	pending, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateDeploymentStatus(ctx, pending.ID, DeployLive, ""); !IsMCPReadinessRequired(err) {
		t.Fatalf("status transition bypassed readiness: %v", err)
	}
	if _, err = s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:insert", Status: DeployLive}); !IsMCPReadinessRequired(err) {
		t.Fatalf("live insert bypassed readiness: %v", err)
	}
}
