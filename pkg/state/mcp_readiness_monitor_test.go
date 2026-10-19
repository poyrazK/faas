package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessContinuousStore(t *testing.T) {
	check := func(t *testing.T, s Store) {
		ctx := t.Context()
		source := s.(MCPReadinessStore)
		monitors := s.(MCPReadinessMonitorStore)
		a, err := s.CreateAccount(ctx, "continuous@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, App{AccountID: a.ID, Slug: "continuous", Type: AppTypeApp, Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:stable"})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		request := api.SetMCPReadinessPolicyRequest{Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "continuous monitoring", Roles: []api.MCPReadinessRole{{Name: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64), Monitor: &api.MCPReadinessMonitorConfig{Enabled: true, IntervalSeconds: 60, FailureThreshold: 2}}}}
		policy, err := source.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		list := func() []api.MCPReadinessMonitorStatus {
			t.Helper()
			out, e := monitors.ListMCPReadinessMonitoring(ctx, a.ID, app.ID, "")
			if e != nil || len(out) != 1 {
				t.Fatalf("list=%+v %v", out, e)
			}
			return out
		}
		if list()[0].State != "pending" {
			t.Fatal("unobserved monitor reported healthy")
		}
		claim := func() MCPReadinessMonitorJob {
			t.Helper()
			jobs, e := monitors.ClaimMCPReadinessMonitors(ctx)
			if e != nil || len(jobs) != 1 {
				t.Fatalf("claim=%+v %v", jobs, e)
			}
			return jobs[0]
		}
		forceDue := func() {
			t.Helper()
			switch store := s.(type) {
			case *MemStore:
				store.mu.Lock()
				for key, j := range store.readinessMonitorJobsLocked() {
					j.Status.NextProbeAt = time.Now().Add(-time.Second)
					store.mcpReadiness.monitors[key] = j
				}
				store.mu.Unlock()
			case *PgStore:
				if _, e := store.pool.Exec(ctx, `UPDATE deployment_mcp_readiness_monitors SET next_probe_at=now()-interval '1 second'`); e != nil {
					t.Fatal(e)
				}
			}
		}
		job := claim()
		if jobs, e := monitors.ClaimMCPReadinessMonitors(ctx); e != nil || len(jobs) != 0 {
			t.Fatal("lease duplicated", e)
		}
		failed := api.MCPReadinessEvidence{Role: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, Checks: []api.MCPReadinessCheck{{Name: "ping", Status: "failed"}}}
		if err = monitors.CompleteMCPReadinessMonitor(ctx, job, failed, "protocol_failure"); err != nil {
			t.Fatal(err)
		}
		if list()[0].State != "degraded" {
			t.Fatal("transient failure fired early")
		}
		if err = monitors.CompleteMCPReadinessMonitor(ctx, job, failed, "protocol_failure"); !errors.Is(err, ErrConflict) {
			t.Fatal("duplicate completion", err)
		}
		forceDue()
		job = claim()
		if err = monitors.CompleteMCPReadinessMonitor(ctx, job, failed, "credential_unavailable"); err != nil {
			t.Fatal(err)
		}
		if list()[0].State != "firing" || list()[0].ConsecutiveFailures != 2 {
			t.Fatal("threshold missing")
		}
		forceDue()
		job = claim()
		passed := api.MCPReadinessEvidence{Role: "reader", ProtocolVersion: api.MCPProtocolVersion, CatalogFormat: 1, CatalogSHA256: strings.Repeat("a", 64), Passed: true}
		if err = monitors.CompleteMCPReadinessMonitor(ctx, job, passed, ""); err != nil {
			t.Fatal(err)
		}
		status := list()[0]
		if status.State != "healthy" || status.LastSuccessAt == nil || len(status.History) != 3 {
			t.Fatalf("recovery=%+v", status)
		}
		snapshot, err := source.ListMCPReadiness(ctx, a.ID, app.ID, "")
		if err != nil || len(snapshot.Evidence) != 1 || !snapshot.Evidence[0].Passed {
			t.Fatal("monitor evidence not committed", err)
		}
		forceDue()
		job = claim()
		request.ExpectedRevision = policy.Revision
		request.Roles[0].Monitor.FailureThreshold = 3
		if _, err = source.SetMCPReadinessPolicy(ctx, a.ID, app.ID, request); err != nil {
			t.Fatal(err)
		}
		if err = monitors.CompleteMCPReadinessMonitor(ctx, job, passed, ""); !errors.Is(err, ErrConflict) {
			t.Fatal("stale policy accepted", err)
		}
		if list()[0].State != "pending" {
			t.Fatal("prior policy success reused")
		}
		expireLease := func() {
			t.Helper()
			switch store := s.(type) {
			case *MemStore:
				store.mu.Lock()
				for key, j := range store.readinessMonitorJobsLocked() {
					j.LeaseUntil = time.Now().Add(-time.Second)
					store.mcpReadiness.monitors[key] = j
				}
				store.mu.Unlock()
			case *PgStore:
				if _, e := store.pool.Exec(ctx, `UPDATE deployment_mcp_readiness_monitors SET lease_until=now()-interval '1 second',next_probe_at=now()-interval '1 second'`); e != nil {
					t.Fatal(e)
				}
			}
		}
		expireLease()
		if err = monitors.CompleteMCPReadinessMonitor(ctx, job, passed, ""); !errors.Is(err, ErrConflict) {
			t.Fatal("expired lease accepted", err)
		}
		job = claim()
		if err = monitors.CompleteMCPReadinessMonitor(ctx, job, passed, ""); err != nil {
			t.Fatal("lease recovery", err)
		}
		for range 21 {
			forceDue()
			job = claim()
			if err = monitors.CompleteMCPReadinessMonitor(ctx, job, passed, ""); err != nil {
				t.Fatal(err)
			}
		}
		if len(list()[0].History) != 20 {
			t.Fatal("history is not bounded")
		}
		forceDue()
		job = claim()
		if err = s.UpsertAppSecret(ctx, a.ID, app.ID, "ROTATED", []byte("opaque ciphertext")); err != nil {
			t.Fatal(err)
		}
		if err = monitors.CompleteMCPReadinessMonitor(ctx, job, passed, ""); !errors.Is(err, ErrConflict) {
			t.Fatal("changed runtime subject accepted", err)
		}

		if _, err = monitors.ListMCPReadinessMonitoring(ctx, "foreign", app.ID, ""); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign monitor visible")
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
