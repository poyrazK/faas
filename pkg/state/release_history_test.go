package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type releaseRunTestStore interface {
	state.Store
	state.AppTaskStore
	state.ReleaseRunStore
}

func TestReleaseRunsCrossedByMem(t *testing.T) { releaseRunsSuite(t, state.NewMemStore()) }

// TestReleaseRunsCrossedByPG also checks the served path: the cut-off is the
// target's serving_ended_at once it has one.
func TestReleaseRunsCrossedByPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	fx := releaseRunsSuite(t, s)
	if _, err := pool.Exec(ctx, `UPDATE deployments SET serving_ended_at = $2 WHERE id = $1`, fx.target.ID, fx.base.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	runs, err := s.ReleaseRunsCrossedBy(ctx, fx.app.ID, fx.target.ID)
	if err != nil || len(runs) != 1 || runs[0].DeploymentID != fx.failed.ID {
		t.Fatalf("runs after the target served until +15s = %+v, %v; want only the failed release", runs, err)
	}
}

type releaseRunFixture struct {
	app            state.App
	target, failed state.Deployment
	base           time.Time
}

// releaseRunsSuite pins ADR-1448's crossing rule at the store: started
// release tasks of other deployments in the target's scope after the target
// was created (it never served), oldest first, failed ones included.
func releaseRunsSuite(t *testing.T, store releaseRunTestStore) releaseRunFixture {
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "release-runs-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "release-runs-" + uuid.NewString()[:8], RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	release := func(scope string, offset time.Duration, status state.AppTaskStatus) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(),
			Status: state.DeploySnapshotting, Scope: scope,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
			if err := store.UpdateDeploymentStatus(ctx, d.ID, s, ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/tmp/r.ext4", "apps/release/"+d.ID+".ext4", 4096); err != nil {
			t.Fatal(err)
		}
		started := base.Add(offset)
		task, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{
			AccountID: account.ID, AppID: app.ID, DeploymentID: d.ID,
			Kind: state.AppTaskKindRelease, Command: []string{"bin/migrate"}, CreatedAt: started.Add(-2 * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimNextAppTask(ctx, "schedd", started.Add(-time.Second), time.Minute)
		if err != nil || claimed.ID != task.ID {
			t.Fatalf("claim = %+v, %v", claimed, err)
		}
		if _, err := store.MarkAppTaskRunning(ctx, task.ID, *claimed.LeaseToken, started); err != nil {
			t.Fatal(err)
		}
		exit := 0
		completion := state.CompleteAppTaskParams{
			ID: task.ID, LeaseToken: *claimed.LeaseToken, Status: status, ExitCode: &exit, FinishedAt: started.Add(time.Second),
		}
		if status == state.AppTaskFailed {
			exit = 1
			code, message := "exit_nonzero", "migration failed"
			completion.FailureCode, completion.FailureMessage = &code, &message
		}
		if _, err := store.CompleteAppTask(ctx, completion); err != nil {
			t.Fatal(err)
		}
		got, err := store.DeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	release("", -10*time.Second, state.AppTaskSucceeded) // before the target existed
	target := release("", 5*time.Second, state.AppTaskSucceeded)
	succeeded := release("", 10*time.Second, state.AppTaskSucceeded)
	failed := release("", 20*time.Second, state.AppTaskFailed)
	release("staging", 30*time.Second, state.AppTaskSucceeded) // another scope

	runs, err := store.ReleaseRunsCrossedBy(ctx, app.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %+v, want the succeeded and the failed release", runs)
	}
	want := []struct {
		d      state.Deployment
		status state.AppTaskStatus
		at     time.Time
	}{{succeeded, state.AppTaskSucceeded, base.Add(10 * time.Second)}, {failed, state.AppTaskFailed, base.Add(20 * time.Second)}}
	for i, w := range want {
		r := runs[i]
		if r.DeploymentID != w.d.ID || r.Revision != w.d.Revision || r.Status != w.status || !r.StartedAt.Equal(w.at) {
			t.Fatalf("run %d = %+v, want %s rev %d %s at %s", i, r, w.d.ID, w.d.Revision, w.status, w.at)
		}
	}
	if runs, err := store.ReleaseRunsCrossedBy(ctx, app.ID, uuid.NewString()); err != nil || len(runs) != 0 {
		t.Fatalf("runs for an unknown target = %+v, %v; want none", runs, err)
	}
	if runs, err := store.ReleaseRunsCrossedBy(ctx, uuid.NewString(), target.ID); err != nil || len(runs) != 0 {
		t.Fatalf("runs for another app's target = %+v, %v; want none", runs, err)
	}
	return releaseRunFixture{app: app, target: target, failed: failed, base: base}
}

// TestReleaseMigrationDatabasesPG lists only live migration bindings that
// deliver into the scope, the databases a release task can change.
func TestReleaseMigrationDatabasesPG(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "release-dbs-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "release-dbs-" + uuid.NewString()[:8], RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	database := func(name string) string {
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,name,region,postgres_major,service_class,availability,backend_id,backend_fingerprint) VALUES($1,$2,$3,'eu',17,'development','single_zone','test',$4)`,
			id, account.ID, name, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	bind := func(databaseID, scope, access, key string, deleted bool) {
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_bindings(id,account_id,database_id,app_id,scope,environment_key,access) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			id, account.ID, databaseID, app.ID, scope, key, access); err != nil {
			t.Fatal(err)
		}
		if err := store.PutManagedPostgresSecret(ctx, state.AppSecret{AccountID: account.ID, AppID: app.ID, Scope: scope, Key: key,
			ManagedPostgresBindingID: id, ManagedCredentialRef: "private-ref-" + id, ManagedCredentialGeneration: 1,
			Kid: "kid", Ciphertext: []byte("private-ciphertext"), ValueHash: "0123456789abcdef"}); err != nil {
			t.Fatal(err)
		}
		if deleted {
			if _, err := pool.Exec(ctx, `UPDATE managed_postgres_bindings SET state = 'deleted', deleted_at = now() WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	orders, audit, staging, gone := database("orders"), database("audit"), database("staging-db"), database("gone")
	bind(orders, "default", "migration", "MIGRATION_DATABASE_URL", false)
	bind(orders, "default", "read_write", "DATABASE_URL", false)
	bind(audit, "default", "read_write", "AUDIT_DATABASE_URL", false)
	bind(staging, "staging", "migration", "MIGRATION_DATABASE_URL", false)
	bind(gone, "default", "migration", "OLD_MIGRATION_DATABASE_URL", true)

	got, err := store.ReleaseMigrationDatabases(ctx, app.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != orders || got[0].Name != "orders" {
		t.Fatalf("migration databases = %+v, want only orders", got)
	}
}
