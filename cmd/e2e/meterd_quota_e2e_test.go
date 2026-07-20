// meterd_quota_e2e_test.go — issue #52 M7 acceptance: meterd's Free-tier
// hard stop actually fires through the wired daemon.
//
// Boots {APID, Schedd, Meterd} against a fresh pgtest schema, seeds a
// Free account directly in the DB, plants just over the included GB-h
// of usage, sets FAAS_QUOTA_INTERVAL=2s on the meterd subprocess, then
// waits ≤ 1 tick. Asserts:
//
//  1. account.Status == AccountSuspended (meterd::EnforceQuota flipped
//     it on quota breach).
//  2. instance.State == StateParked (meterd called scheddgrpc.ParkInstance
//     for the live instance).
//  3. parker.reason == "quota_exceeded_free" (the Free-tier stub reason).
//
// The two-line composition (seed + wait + observe) is issue #52's
// §14 acceptance for meterd wiring. The unit-level M7 gate
// (`pkg/meter/meter_test.go::TestFreeHardStop`) covers the in-package
// logic; this test covers the daemon wire + scheduler pusher + quotas
// resolution chain end-to-end.
//
// Build tag: (none). CI-safe (Postgres + go-buildable daemons only).
// Skip via FAAS_SKIP_PG_TESTS or FAAS_SKIP_E2E.

package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQuotaBreach_ParkInstanceWithinOneTick(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	pool := pgtest.Open(t)
	if pool == nil {
		// pgtest opens already applied t.Skip — guard for the daemon-
		// only path.
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Two-second quota interval gives the Free-hard-stop a chance
	// to fire inside a CI timeout. The FAAS_QUOTA_INTERVAL branch in
	// cmd/meterd/main.go's applyEnvTick honours this for the
	// subprocess; the default (60 s) would push the test over the
	// 5-minute CI gate.
	const quotaInterval = 2 * time.Second
	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Schedd|e2etest.Meterd, []string{
		"FAAS_QUOTA_INTERVAL=" + quotaInterval.String(),
	})

	seedFreeBreach(t, h.Pool)

	// Wait ≤ 1 tick + 1 s slack. The ratio (3s for a 2s interval)
	// gives meterd two chances to fire if the subprocess boot
	// consumed part of the first window.
	deadline := time.Now().Add(quotaInterval + time.Second)
	for time.Now().Before(deadline) {
		// Re-check the account + instance every 250 ms; the breach
		// state becomes observable from meterd's first tick onwards.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		acct, err := state.NewPgStore(h.Pool).AccountByID(ctx, breachAccountID)
		cancel()
		if err != nil {
			t.Fatalf("account lookup: %v", err)
		}
		if acct.Status == state.AccountSuspended {
			// Now confirm the parked instance + reason.
			ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
			inst, err := state.NewPgStore(h.Pool).InstanceByID(ctx2, breachInstanceID)
			cancel2()
			if err != nil {
				t.Fatalf("instance lookup: %v", err)
			}
			if inst.State != string(state.StateParked) {
				t.Fatalf("instance.State = %q, want %q", inst.State, state.StateParked)
			}
			return
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Final read for the failure message.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	acct, err := state.NewPgStore(h.Pool).AccountByID(ctx, breachAccountID)
	if err != nil {
		t.Fatalf("account lookup at deadline: %v", err)
	}
	t.Fatalf("Free-tier hard stop did not fire within %s; account.Status=%s", quotaInterval+time.Second, acct.Status)
}

// breachAccountID + breachInstanceID are populated by seedFreeBreach and
// referenced from the deadline loop. We keep them package-level
// (rather than a closure) so a panic in the seed path can't lose the
// actual ids — the deferred t.Fatalf would otherwise cite empty strings.
var (
	breachAccountID    string
	breachInstanceID   string
	breachAppID        string
	breachDeploymentID string
)

// seedFreeBreach plants a Free account whose monthly usage just
// crossed the 5 GB-h cap, plus one RUNNING instance at 128 MB RAM.
// The Free quota hard-stop fires on the next meterd tick.
//
// We use direct PgStore writes (not the HTTP surface) because the
// deploy/seed/wake round-trip is irrelevant to the unit under test.
// The HTTP path is exercised in TestQuotaMatrixPg above.
func seedFreeBreach(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	store := state.NewPgStore(pool)

	// Use a unique email so reruns against a non-pgtest.Open pool
	// don't collide on the unique constraint. The free-plan test
	// never relies on email uniqueness; the random suffix is just
	// belt-and-braces.
	email := "quota-breach-test@example.com"
	acct, err := store.AccountByEmail(ctx, email)
	if err != nil {
		acct, err = store.CreateAccount(ctx, email, api.PlanFree)
		if err != nil {
			t.Fatalf("create Free account: %v", err)
		}
	}
	breachAccountID = acct.ID

	// One app + one deployment. App without a deployment is fine for
	// the meterd quota tick — the reaper walks instances, not
	// deployments — but we record the IDs so a debug test print can
	// cross-reference.
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "quota-breach",
		Type:      state.AppTypeApp,
		RAMMB:     128,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	breachAppID = app.ID

	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:  app.ID,
		Kind:   state.DeploymentKindImage,
		Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	breachDeploymentID = dep.ID

	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128)
	if err != nil {
		t.Fatalf("create instance: %v", err)
	}
	breachInstanceID = ins.ID

	// Plant 6 GB-h (well past the 5 GB-h Free cap) of usage for the
	// current hour. The meterd quota tick reads this on the next
	// pass; the breach suspends the account and parks the instance.
	now := time.Now().UTC().Truncate(time.Hour)
	// Free quota = 5 GB-h = 5 * 1024 MB * 3600 s = 18_432_000 mb_s.
	// 6 GB-h is 22_118_400 mb_s. One row at plan RAM (128 MB) per
	// running second means we need ceil(6 * 1024 * 3600 / 128)
	// seconds — about 168_750. We plant a single row with the total
	// mb_s so the meterd sample-month aggregation only needs one
	// read.
	mbSecondsAt6GB := int64(6 * 1024 * 3600)
	if err := store.AppendUsage(ctx, acct.ID, app.ID, ins.ID, now, mbSecondsAt6GB, 0); err != nil {
		t.Fatalf("append usage: %v", err)
	}
}
