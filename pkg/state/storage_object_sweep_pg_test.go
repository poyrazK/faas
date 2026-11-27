//go:build !no_pg

// adr: 20261117190000
package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func oldUUIDv7(t *testing.T, at time.Time) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	ms := uint64(at.UnixMilli())
	for i := 0; i < 6; i++ {
		id[i] = byte(ms >> (40 - 8*i))
	}
	return id.String()
}

// The worker's re-check for what the control-plane sweep requests: a jobs/
// attempt is live while its job materializes or publishes it; a source
// archive is live while its build is queued or running, or while young.
func TestStorageObjectReferenced_JobAttemptsAndSources(t *testing.T) {
	store, pool, ctx := pgJobsStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "pg-sweep@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	newJob := func(name, status, key string) string {
		job, err := store.JobCreate(ctx, account.ID, name, "batch", "apps/"+name+"/a.ext4", []string{"/bin/true"}, 256, 60, 1, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `update jobs set image_materialization_status = $2, image_storage_key = nullif($3, '') where id = $1::uuid`, job.ID, status, key); err != nil {
			t.Fatal(err)
		}
		return job.ID
	}
	attempt := func(job string) string { return "jobs/" + job + "__" + uuid.NewString() + ".ext4" }

	pending := newJob("sweep-pending", "pending", "")
	failed := newJob("sweep-failed", "failed", "")
	ready := newJob("sweep-ready", "pending", "")
	published := attempt(ready)
	if _, err := pool.Exec(ctx, `update jobs set image_materialization_status = 'ready', image_storage_key = $2 where id = $1::uuid`, ready, published); err != nil {
		t.Fatal(err)
	}

	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "sweep-src", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	build := func(status string) string {
		id := oldUUIDv7(t, old)
		if _, err := pool.Exec(ctx, `insert into builds (id, deployment_id, kind, source_bytes, status) values ($1::uuid, $2, 'tarball', 1, $3)`, id, dep.ID, status); err != nil {
			t.Fatal(err)
		}
		return "sources/" + id + ".tar.gz"
	}
	src := func(id string) string { return "sources/" + id + ".tar.gz" }

	for key, want := range map[string]bool{
		attempt(pending):                    true,
		attempt(failed):                     false,
		published:                           true,
		attempt(ready):                      false,
		attempt(uuid.NewString()):           false, // job gone
		build("queued"):                     true,
		build("running"):                    true,
		build("succeeded"):                  false,
		src(oldUUIDv7(t, old)):              false, // build gone
		src(oldUUIDv7(t, time.Now())):       true,  // inside the orphan grace
		src(uuid.NewString()):               true,  // v4: no creation time
		"sources/" + strings.Repeat("x", 8): true,
	} {
		ref, class, err := store.StorageObjectReferenced(ctx, key)
		if err != nil || ref != want {
			t.Errorf("%s referenced = %v (%s), %v; want %v", key, ref, class, err, want)
		}
	}
}
