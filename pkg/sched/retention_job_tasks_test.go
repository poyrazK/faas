// spec: §17; adr: 20261031190030
package sched_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// job_tasks.instance_id has no delete action, so the sweep's DELETE of a
// job task's old terminal instance failed on every tick (production
// 2026-10-31: ~200 failures a day, the same 11 rows). The sweep now leaves
// such rows to the job task; every other old terminal row still goes.
func TestRetentionKeepsInstancesJobTasksReference(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	ms := retentionMemStore(t, store)
	old := func() state.Instance {
		ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateFailed), 512, state.DefaultLocalNodeName, "")
		if err != nil {
			t.Fatal(err)
		}
		ms.SetTerminalAtForTest(ins.ID, time.Now().Add(-40*24*time.Hour))
		return ins
	}
	referenced, unreferenced := old(), old()
	acct, err := store.CreateAccount(ctx, "retention-jobs@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreate(ctx, acct.ID, "retention", "batch", "registry.example/job@sha256:"+strings.Repeat("b", 64), nil, 256, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.JobSetImageMaterialization(ctx, job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
		t.Fatal(err)
	}
	run, _, err := store.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.JobTaskMarkClaimed(ctx, run.ID, 0, referenced.ID, "lease", time.Now().Add(time.Minute), state.DefaultLocalNodeName); err != nil {
		t.Fatalf("JobTaskMarkClaimed: %v", err)
	}
	r := sched.NewRetention(store, slog.Default()).WithRetention(30 * 24 * time.Hour)
	deleted, err := r.SweepOnce(ctx)
	if err != nil || deleted != 1 {
		t.Fatalf("SweepOnce = %d, %v; want the one unreferenced row", deleted, err)
	}
	if _, err := store.InstanceByID(ctx, unreferenced.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("the unreferenced old row survived: %v", err)
	}
	if _, err := store.InstanceByID(ctx, referenced.ID); err != nil {
		t.Fatalf("the job task's instance was deleted: %v", err)
	}
}
