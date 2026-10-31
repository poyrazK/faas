//go:build !no_pg

// adr: 570
package state_test

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// imaged holds its deployment activation lock on a pinned direct-pool
// connection across the live transition, and MarkDeploymentLive's traffic
// guard pins another one. With the notify hub parked on a third, the guard
// waited for a connection that never freed: image deploys hung in
// snapshotting (metal, 2026-10-25). The direct pool is sized as production
// sizes it for imaged.
func TestPgImagedMarksLiveWhileHoldingItsActivationLock(t *testing.T) {
	migrated := pgtest.OpenMigrated(t)
	cfg := migrated.Config().ConnConfig
	dsn := (&url.URL{Scheme: "postgres", User: url.UserPassword(cfg.User, cfg.Password),
		Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), Path: "/" + cfg.Database,
		RawQuery: "sslmode=disable"}).String()
	t.Setenv(db.NotifyHubEnv, "")
	t.Setenv(db.DirectDSNEnv, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.OpenWithAppName(ctx, dsn, "imaged")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "imaged-activation-guard@test.local", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "imaged-activation-guard", Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("2", 64)})
	if err != nil {
		t.Fatal(err)
	}

	// The notify hub parks one direct connection for the process lifetime.
	hub, err := db.DirectPool(pool).Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Release()
	release, err := store.AcquireDeploymentActivationLock(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release(ctx)
	live, cancelLive := context.WithTimeout(ctx, 10*time.Second)
	defer cancelLive()
	if err := store.MarkDeploymentLive(live, dep.ID); err != nil {
		t.Fatalf("mark live under the activation lock: %v", err)
	}
}
