// Package e2e — cmd/e2e acceptance tests. See cmd/e2e/quota_e2e_test.go
// for the build-tag policy. meterd_quota_e2e_test.go is the M7 §14 gate
// for issue #52: a Free-tier breach parks the test app within one
// FAAS_QUOTA_INTERVAL tick.
//
// This test boots real daemon subprocesses (apid + schedd + meterd) so
// the meter loop runs in the production wire — not the in-process fakes
// pkg/meter/meter_test.go uses. The migration race (daemon reading
// accounts before goose.Up finishes) is gated by
// pgtest.WaitForMigration, which this test invokes before
// e2etest.StartWithEnv.
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.

//go:build !no_pg

package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestQuotaBreach_ParkInstanceWithinOneTick is the §14 M7 acceptance
// gate (issue #52): a Free account crossing 5 GB-h flips to suspended
// AND has every live instance parked within one meterd quota tick.
//
// This is the daemon-subprocess analogue of
// pkg/meter/meter_test.go::TestFreeHardStop. The e2e form proves the
// wire-up: scheddgrpc.ParkInstance actually fires, the account row
// actually transitions, the instance row actually parks.
func TestQuotaBreach_ParkInstanceWithinOneTick(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 12, 10*time.Second)

	const quotaInterval = 2 * time.Second
	// StartWithEnv registers its own t.Cleanup(h.stop); the harness
	// doesn't expose a Stop method because t.Cleanup runs the teardown
	// automatically on test exit (success, failure, or panic).
	e2etest.StartWithEnv(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.Meterd,
		[]string{"FAAS_QUOTA_INTERVAL=" + quotaInterval.String()})

	store := state.NewPgStore(pool)

	acct, err := store.CreateAccount(ctx, "free-breach@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	app, err := store.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "freebreach",
		Type:           state.AppTypeApp,
		RAMMB:          128,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Status:      state.DeployLive,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	// Plant enough usage to breach the Free 5 GB-h cap in a single minute
	// (so the quota loop reads it on its very next tick). The math:
	// 6 GB-h * 1024 MB/GB * 3600 s/h = 22_118_400 mb_seconds.
	mbSecOverQuota := int64(float64(api.PlanFree.PlanIncludedGBHours()+1) * 1024 * 3600)
	minute := meter.AccountMonthKey(time.Now().UTC()).AddDate(0, 0, 1) // 1st of current month + 1d
	if err := store.AppendUsage(ctx, acct.ID, app.ID, ins.ID, minute, mbSecOverQuota, 1); err != nil {
		t.Fatalf("AppendUsage: %v", err)
	}

	// Poll up to 1.5× quota_interval for the account status to flip.
	deadline := time.Now().Add(quotaInterval + time.Second)
	for {
		got, err := store.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatalf("AccountByID: %v", err)
		}
		if got.Status == state.AccountSuspended {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("account.status = %s after %s; want suspended (quota_interval=%s)",
				got.Status, quotaInterval+time.Second, quotaInterval)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Final assertions.
	got, err := store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatalf("AccountByID final: %v", err)
	}
	if got.Status != state.AccountSuspended {
		t.Fatalf("account.status = %s; want suspended", got.Status)
	}

	instGot, err := store.InstanceByID(ctx, ins.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	// The meterd→schedd.ParkInstance wire is the contract this test
	// verifies (issue #52). The instance must transition OFF RUNNING
	// within one quota tick. PARKED is the happy path (snapshot
	// succeeded via vmmd); STOPPED is the documented fallback when
	// vmmd cannot snapshot (engine.go:349, ADR-005: cold boot always
	// works, so a missing snapshot just drops the instance into
	// STOPPED — the next wake will cold-boot). Both prove the park
	// landed; either one is a passing gate.
	switch state.State(instGot.State) {
	case state.StateParked, state.StateStopped:
		// pass
	default:
		t.Fatalf("instance.state = %s; want parked or stopped (meterd→schedd.ParkInstance did not transition the instance off RUNNING)",
			instGot.State)
	}
}
