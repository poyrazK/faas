//go:build metal

// adr: 20261023171905 — unbilled quiet pause through the real daemons: vmmd
// pauses a between_requests app 2 s after each request, schedd records the
// pauses in the ledger, and meterd bills each minute for the running time
// plus about 2 s per quiet period instead of the whole minute.
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQuietPauseBillingMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal quiet pause billing test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	builderBaseRef := registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideBuilderBase(t, builderBaseRef)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	// Quiet pause, its ledger and unbilled billing are all off by default.
	// meterd keeps its production minute cadence.
	t.Setenv("FAAS_QUIET_PAUSE", "1")
	t.Setenv("FAAS_QUIET_PAUSE_LEDGER", "1")
	t.Setenv("FAAS_QUIET_PAUSE_UNBILLED", "on")
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.Meterd)
	defer h.DumpLogs(t)
	key := h.SeedAccount(ctx, api.PlanPro)
	img, _ := e2etest.HelloImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)

	// A request-mode app opts in to quiet pause by declaring between_requests,
	// so its first wake already carries the 2 s pause delay.
	falsy := false
	create := api.CreateAppRequest{Slug: "quietbill", Type: "app", RequireAuthn: &falsy, Pause: api.PauseBetweenRequests}
	if got := postOK(t, h, key, "/v1/apps", create); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "quietbill")
	t.Cleanup(func() {
		// Neither daemon destroys VMs when it stops.
		_, _ = doReq(t, h, key, http.MethodDelete, "/v1/apps/quietbill", nil)
	})
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/quietbill/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("decode deployment: %v", err)
	}
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 15*time.Minute); err != nil {
		t.Fatalf("deployment did not go live: %v", err)
	}
	// The longest Pro idle timeout keeps one VM resident through the
	// measured minutes, so each minute is one instance's residency.
	pro, _ := api.LimitsFor(api.PlanPro)
	_, longest := pro.IdleTimeoutBounds()
	setAppIdleTimeout(t, h, key, "quietbill", longest)

	client := h.HTTPClient()
	url := gatewayAppURL(h, "quietbill")
	host := "quietbill.apps.test.example"
	if err := e2etest.WaitForHTTPReady(ctx, t, client, url, 5*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}

	// One request every 20 s from a minute boundary: each minute holds three
	// quiet periods, each paused from ~2 s after its request to the next one.
	// Minutes 1-3 after the start are measured; the requests continue until
	// meterd has rolled them.
	const every = 20 * time.Second
	start := time.Now().Truncate(time.Minute).Add(time.Minute)
	measured := []time.Time{start.Add(time.Minute), start.Add(2 * time.Minute), start.Add(3 * time.Minute)}
	end := measured[len(measured)-1].Add(time.Minute)
	// meterd rolls a closed minute within its 60 s sample interval.
	deadline := end.Add(4 * time.Minute)
	var instanceID string
	for next := start; ; next = next.Add(every) {
		time.Sleep(time.Until(next))
		getFrozenApp(t, client, url, host, key)
		if instanceID == "" {
			ins, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 30*time.Second)
			if err != nil {
				t.Fatalf("no running instance: %v", err)
			}
			instanceID = pickState(ins, state.StateRunning).ID
		}
		if next.After(end) && quietBillingRolled(ctx, t, pool, instanceID, measured) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("meterd did not roll minutes %v for instance %s by %v", measured, instanceID, deadline)
		}
	}
	ins, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 10*time.Second)
	if err != nil || pickState(ins, state.StateRunning).ID != instanceID {
		t.Fatalf("instance %s did not stay resident through the measured minutes (%s)", instanceID, instanceStates(ins))
	}

	// Billed seconds are mb_seconds over the billable RAM (plan RAM + 8 MB).
	// Each minute runs at least 2 s per quiet period. Above that: up to
	// 0.25 s per period for the pause pass's tick, 2 s of rounding (resident
	// up, paused down), and a few seconds of a pause open across the minute's
	// end if meterd rolled it before the node's ledger watermark passed it.
	// Billing every running second, as before, would be 60.
	var ramMB int
	if err := pool.QueryRow(ctx, `select ram_mb from instances where id = $1`, instanceID).Scan(&ramMB); err != nil {
		t.Fatalf("read instance RAM: %v", err)
	}
	for _, minute := range measured {
		var mbSeconds int64
		if err := pool.QueryRow(ctx,
			`select mb_seconds from usage_minutes where instance_id = $1 and minute = $2`, instanceID, minute).Scan(&mbSeconds); err != nil {
			t.Fatalf("read usage for %v: %v", minute, err)
		}
		billed := mbSeconds / int64(api.BillableRAMMB(ramMB))
		t.Logf("minute %s: billed %d s (%d MB-seconds at %d MB)", minute.Format(time.TimeOnly), billed, mbSeconds, api.BillableRAMMB(ramMB))
		if billed < 6 || billed > 16 {
			t.Errorf("minute %s billed %d s, want 6-16: three requests, each running ~2 s before its quiet pause", minute.Format(time.TimeOnly), billed)
		}
	}
	// The ledger holds the pauses: three of about 18 s in each minute.
	var closed int
	var pausedSeconds float64
	if err := pool.QueryRow(ctx, `
select count(*), coalesce(sum(extract(epoch from ended_at - started_at)), 0)
  from instance_quiet_pauses
 where instance_id = $1 and started_at >= $2 and ended_at <= $3`,
		instanceID, measured[0], end).Scan(&closed, &pausedSeconds); err != nil {
		t.Fatalf("read pause ledger: %v", err)
	}
	t.Logf("ledger: %d closed pauses, %.1f s paused in the measured minutes", closed, pausedSeconds)
	if closed < 8 || pausedSeconds < 8*15 {
		t.Errorf("ledger has %d closed pauses totalling %.1f s in the measured minutes, want about 9 of ~18 s", closed, pausedSeconds)
	}
}

// quietBillingRolled reports whether meterd has rolled every measured minute
// for the instance.
func quietBillingRolled(ctx context.Context, t *testing.T, pool *pgxpool.Pool, instanceID string, minutes []time.Time) bool {
	t.Helper()
	var rolled int
	if err := pool.QueryRow(ctx,
		`select count(*) from usage_minutes where instance_id = $1 and minute = any($2)`, instanceID, minutes).Scan(&rolled); err != nil {
		t.Fatalf("read usage minutes: %v", err)
	}
	return rolled == len(minutes)
}
