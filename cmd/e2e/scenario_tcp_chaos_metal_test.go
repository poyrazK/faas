//go:build metal

// adr: 967 — native acceptance for run-scoped faults on private TCP streams.
package e2e_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioTCPChaosMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_E2E_SERVICE_TCP", "1")
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.Builderd, "FAAS_SERVICE_TCP_ENABLED=1")
	defer h.DumpLogs(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*sourceDeployCtxTimeout())
	defer cancel()
	store := state.NewPgStore(pool)
	node, err := store.ComputeNodeByName(ctx, "default-local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetComputeNodeServiceAddressReady(ctx, node.ID, true); err != nil {
		t.Fatal(err)
	}
	key := h.SeedAccount(ctx, api.PlanPro)
	client := api.NewClient(h.APIDURL, key)
	suffix := randHexSuffix()
	runID := suffix + randHexSuffix()
	registered := false
	projects := make([]string, 0, 2)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cleanupCancel()
		for _, project := range projects {
			if err := client.DestroyDevSession(cleanupCtx, project, runID); err != nil {
				t.Errorf("destroy %s: %v", project, err)
			}
		}
		if registered {
			if err := client.DeleteScenarioTest(cleanupCtx, runID); err != nil {
				t.Errorf("delete TCP test namespace: %v", err)
			}
		}
	}()
	create := func(project string, source []byte) api.DevSessionResponse {
		t.Helper()
		session, err := client.UpsertDevSession(ctx, project, api.UpsertDevSessionRequest{WorkspaceID: runID})
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, project)
		body, code := postMultipartDeploymentWithOverrides(t, h, key, session.App.Slug, source, false, nil, "")
		if code != http.StatusAccepted {
			t.Fatalf("deploy %s: %d %s", project, code, body)
		}
		deployment, _ := parseQueuedDeployment(t, body)
		if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, deployment, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
			t.Fatal(err)
		}
		return session
	}
	cache := create("tcp-cache-"+suffix, NodeFixtureTCPService(t))
	caller := create("tcp-caller-"+suffix, NodeFixtureTCPDialer(t))
	ports := []api.WorkloadPort{{Port: 5432, Protocol: api.WorkloadPortTCP, Internal: true}}
	if _, err := client.UpdateApp(ctx, cache.App.Slug, api.UpdateAppRequest{Ports: &ports}); err != nil {
		t.Fatal(err)
	}
	if err := client.RegisterScenarioTest(ctx, runID, api.RegisterScenarioTestRequest{Members: []api.ScenarioTestWorkload{{Workload: "api", AppSlug: caller.App.Slug}, {Workload: "cache", AppSlug: cache.App.Slug}}}); err != nil {
		t.Fatal(err)
	}
	registered = true
	dial := func(t *testing.T, payload string) privateTCPDial {
		return privateTCPDialFrom(t, h, key, caller.App.Slug, "cache.svc.gregale", 5432, payload)
	}
	if got := dial(t, "baseline"); got.status != http.StatusOK || got.Reply != "tcp-echo:baseline" {
		t.Fatalf("baseline = %+v", got)
	}
	for _, tc := range []struct {
		rule     api.ScenarioTestChaosRule
		duration int64
		payload  string
		minimum  time.Duration
	}{
		{api.ScenarioTestChaosRule{Kind: chaos.KindTCPLatency, LatencyMS: 300, Direction: chaos.DirectionDownstream}, 5000, "latency", 200 * time.Millisecond},
		{api.ScenarioTestChaosRule{Kind: chaos.KindTCPBandwidth, RateKiBPerSecond: 4, Direction: chaos.DirectionDownstream}, 5000, strings.Repeat("b", 1024), 200 * time.Millisecond},
		{api.ScenarioTestChaosRule{Kind: chaos.KindTCPTimeout, Direction: chaos.DirectionDownstream}, 1000, "timeout", 800 * time.Millisecond},
		{api.ScenarioTestChaosRule{Kind: chaos.KindTCPReset}, 1000, "reset", 0},
	} {
		t.Run(tc.rule.Kind, func(t *testing.T) {
			tc.rule.From, tc.rule.To, tc.rule.Port, tc.rule.Percent = "api", "cache", 5432, 100
			receipt, err := client.InjectScenarioTestChaos(ctx, runID, api.InjectScenarioTestChaosRequest{DurationMS: tc.duration, Rules: []api.ScenarioTestChaosRule{tc.rule}})
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			got := dial(t, tc.payload)
			elapsed := time.Since(start)
			if tc.rule.Kind == chaos.KindTCPReset {
				if got.status != http.StatusBadGateway || (got.Error != "ECONNRESET" && got.Error != "EPIPE") {
					t.Fatalf("reset = %+v", got)
				}
			} else if got.status != http.StatusOK || got.Reply != "tcp-echo:"+tc.payload || elapsed < tc.minimum {
				t.Fatalf("fault = %+v in %s, want echo delayed at least %s", got, elapsed, tc.minimum)
			}
			direction := tc.rule.Direction
			if direction == "" {
				direction = chaos.DirectionBoth
			}
			tcpMetalWaitMetric(t, ctx, h.GatewayControlURL, `gatewayd_internal_service_tcp_chaos_injected_total{direction="`+direction+`",kind="`+tc.rule.Kind+`"}`, 1)
			// Expired rules must not affect a fresh connection.
			if wait := time.Until(receipt.ExpiresAt); wait > 0 {
				time.Sleep(wait + 100*time.Millisecond)
			}
			if recovered := dial(t, "recovery"); recovered.status != http.StatusOK || recovered.Reply != "tcp-echo:recovery" {
				t.Fatalf("recovery = %+v", recovered)
			}
		})
	}
}
