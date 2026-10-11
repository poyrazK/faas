//go:build !no_pg

// adr: 590
package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrations_ProductionReleasePolicyChangesInvalidateIngress(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "release-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "release-policy"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "release-policy-api", RAMMB: 128, MaxConcurrency: 1,
		Status: state.AppActive, Manifest: state.AppManifest{RevisionPinTTLSeconds: 600}})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Status: state.DeployPending,
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
		t.Fatal(err)
	}
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "LISTEN app_changed"); err != nil {
		t.Fatal(err)
	}
	assertNotification := func() {
		t.Helper()
		waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		notification, err := listener.Conn().WaitForNotification(waitCtx)
		if err != nil || notification.Payload != app.ID {
			t.Fatalf("release policy notification = %+v, err=%v", notification, err)
		}
	}
	assertChanges := func(want int) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM control_plane_change_log WHERE resource_type = 'project_release' AND app_id = $1`, app.ID).Scan(&count); err != nil || count != want {
			t.Fatalf("release policy ledger count=%d want=%d err=%v", count, want, err)
		}
	}
	release, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 600, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: deployment.ID}})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(1)
	assertNotification()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE project_release_sets SET active = false, expires_at = now() + interval '10 minutes' WHERE id = $1`, release.ID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertChanges(1)
	for i, active := range []bool{false, false, true} {
		if _, err := pool.Exec(ctx, `UPDATE project_release_sets SET active = $2, expires_at = CASE WHEN $2 THEN NULL ELSE now() + interval '10 minutes' END WHERE id = $1`, release.ID, active); err != nil {
			t.Fatal(err)
		}
		if i != 1 {
			assertNotification()
		}
	}
	assertChanges(3)
	if _, err := pool.Exec(ctx, `DELETE FROM project_release_sets WHERE id = $1`, release.ID); err != nil {
		t.Fatal(err)
	}
	assertChanges(4)
	assertNotification()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	stage, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Status: state.DeployPending,
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, stage.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "staging", 600, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: stage.ID}}); err != nil {
		t.Fatal(err)
	}
	assertChanges(4)
}
