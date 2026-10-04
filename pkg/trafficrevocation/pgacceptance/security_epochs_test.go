// adr: 531
package pgacceptance

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
	"github.com/pressly/goose/v3"
)

func TestPGTrafficSecurityGenerationsAndDeletionTombstones(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "traffic-security@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "traffic-security", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	scopes := []trafficrevocation.Scope{{Kind: "account", ID: account.ID}, {Kind: "app", ID: app.ID}, {Kind: "deployment", ID: deployment.ID}}
	backend := state.NewPGTrafficSecurityBackend(pool)
	assertState := func(scope trafficrevocation.Scope, revision int64, revoked bool) {
		t.Helper()
		got, err := backend.Read(ctx, scopes)
		if err != nil || got[scope] != (trafficrevocation.State{Revision: revision, Revoked: revoked}) {
			t.Fatalf("scope=%v state=%v, want %d/%v: %v", scope, got[scope], revision, revoked, err)
		}
	}
	for _, scope := range scopes {
		assertState(scope, 0, false)
	}
	registry := trafficrevocation.New(backend)
	defer registry.Close()
	admitted, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	release, err := registry.Admit(admitted, scopes[:2], cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if placed, err := store.SetAccountAbuseHold(ctx, account.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil || !placed {
		t.Fatalf("place hold: %v/%v", placed, err)
	}
	assertState(scopes[0], 1, true)
	upper := trafficrevocation.Scope{Kind: "account", ID: strings.ToUpper(account.ID)}
	if got, err := backend.Read(ctx, []trafficrevocation.Scope{upper}); err != nil || !got[upper].Revoked {
		t.Fatalf("UUID casing bypassed a recorded revoke: %v/%v", got, err)
	}
	if released, err := store.ReleaseAccountAbuseHold(ctx, account.ID); err != nil || !released {
		t.Fatalf("release hold: %v/%v", released, err)
	}
	assertState(scopes[0], 2, false)
	// No notification/refresh was consumed between revoke and release.
	if err := registry.Refresh(ctx); err != nil || !errors.Is(context.Cause(admitted), trafficrevocation.ErrRevoked) {
		t.Fatalf("missed revoke/release revived admitted traffic: %v/%v", err, context.Cause(admitted))
	}
	if err := store.UpdateAccountPlan(ctx, account.ID, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[0], 2, false)
	if err := store.UpdateAccountStatus(ctx, account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[0], 3, true)
	if err := store.UpdateAccountStatus(ctx, account.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[0], 4, false)
	if _, err := pool.Exec(ctx, "UPDATE deployments SET parked_reason = 'security_scan_regressed' WHERE id = $1", deployment.ID); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[2], 1, true)
	if _, err := pool.Exec(ctx, "UPDATE deployments SET parked_reason = 'liveness_exhausted' WHERE id = $1", deployment.ID); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[2], 2, false)
	if _, err := pool.Exec(ctx, "UPDATE deployments SET traffic_percent = 50 WHERE id = $1", deployment.ID); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[2], 2, false)
	if _, err := pool.Exec(ctx, "DELETE FROM deployments WHERE id = $1", deployment.ID); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[2], 3, true)
	if err := store.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[1], 1, true)
	if _, err := pool.Exec(ctx, "DELETE FROM apps WHERE id = $1", app.ID); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[1], 2, true)
	if _, err := pool.Exec(ctx, "DELETE FROM accounts WHERE id = $1", account.ID); err != nil {
		t.Fatal(err)
	}
	assertState(scopes[0], 5, true)
	// A replacement backend sees the same tombstones; no process-local allow
	// cache can reset the security generations after account cascades.
	if got, err := state.NewPGTrafficSecurityBackend(pool).Read(ctx, scopes); err != nil || len(got) != 3 {
		t.Fatalf("replacement lost durable tombstones: %v/%v", got, err)
	}
	for _, scope := range []trafficrevocation.Scope{{Kind: "invalid", ID: account.ID}, {Kind: "account", ID: "00000000-0000-0000-0000-000000000000"}} {
		if _, err := backend.Read(ctx, []trafficrevocation.Scope{scope}); err == nil {
			t.Fatalf("invalid scope accepted: %v", scope)
		}
	}
}

func TestPGTrafficSecurityMissingBackendRefusesVerification(t *testing.T) {
	if _, err := state.NewPGTrafficSecurityBackend(nil).Read(t.Context(), nil); !errors.Is(err, trafficrevocation.ErrUnavailable) {
		t.Fatalf("missing backend=%v, want unavailable", err)
	}
}

func TestPGTrafficSecurityMigrationBackfillAndRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "security-backfill@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "security-backfill", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", stdlib.RegisterConnConfig(pool.Config().ConnConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := goose.DownContext(ctx, sqlDB, "."); err != nil {
		t.Fatal(err)
	}
	backend := state.NewPGTrafficSecurityBackend(pool)
	if _, err := backend.Read(ctx, nil); err == nil {
		t.Fatal("missing migration passed startup verification")
	}
	if err := store.UpdateAccountStatus(ctx, account.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE deployments SET parked_reason = 'security_scan_regressed' WHERE id = $1", deployment.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	scopes := []trafficrevocation.Scope{{Kind: "account", ID: account.ID}, {Kind: "app", ID: app.ID}, {Kind: "deployment", ID: deployment.ID}}
	got, err := backend.Read(ctx, scopes)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		if got[scope] != (trafficrevocation.State{Revision: 1, Revoked: true}) {
			t.Fatalf("existing blocked identity was not backfilled: %v / %v", scope, got[scope])
		}
	}
}
