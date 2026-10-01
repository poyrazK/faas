//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgJobComputeRegionPersistenceAndPlacementGuards(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "regional-jobs@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	region := "eu-jobs"
	if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: "Jobs", AcceptingNewApps: true}); err != nil {
		t.Fatal(err)
	}
	peer, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "jobs-peer", TargetURL: "unix:///jobs-peer", Region: &region, Active: true, MemMB: 56000, MaxConcurrency: 200, AdmissionCeilingMB: 1024, VCPUBudget: 8, VPCPUs: 4})
	if err != nil {
		t.Fatal(err)
	}
	local, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.JobCreateScheduledIfUnderQuota(ctx, state.Job{AccountID: acct.ID, Name: "regional-jobs", Kind: "batch", ImageRef: "registry.example/jobs:v1", Command: []string{"/bin/true"}, RAMMB: 128, TaskTimeoutS: 60, MaxParallelism: 1, RetryMax: 1, ComputeRegion: region}, 10)
	if err != nil || job.ComputeRegion != region {
		t.Fatalf("create=%+v %v", job, err)
	}
	for _, read := range []func() (state.Job, error){
		func() (state.Job, error) { return store.JobGetByID(ctx, job.ID) },
		func() (state.Job, error) { return store.JobGetByName(ctx, acct.ID, job.Name) },
		func() (state.Job, error) { return store.JobUpdate(ctx, job.ID, nil, nil, nil, nil, nil, nil, nil, nil) },
	} {
		got, err := read()
		if err != nil || got.ComputeRegion != region {
			t.Fatalf("read=%+v %v", got, err)
		}
	}
	jobs, err := store.JobListByAccount(ctx, acct.ID, 10, 0)
	if err != nil || len(jobs) != 1 || jobs[0].ComputeRegion != region {
		t.Fatalf("list=%+v %v", jobs, err)
	}
	_, err = pool.Exec(ctx, "UPDATE jobs SET compute_region='' WHERE id=$1", job.ID)
	requireRegionConstraint(t, err, "jobs_compute_region_immutable")
	_, err = pool.Exec(ctx, "UPDATE compute_nodes SET region='elsewhere' WHERE id=$1", peer.ID)
	requireRegionConstraint(t, err, "compute_nodes_region_in_use")
	run, _, err := store.JobRunCreate(ctx, job.ID, acct.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	instanceID := uuid.NewString()
	leaseToken := uuid.NewString()
	_, err = store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, 0, "cold_booting", 128, local.ID, instanceID, leaseToken, time.Now().Add(time.Minute), local.ID)
	requireRegionConstraint(t, err, "instances_compute_region")
	task, err := store.JobTaskGet(ctx, run.ID, 0)
	if err != nil || task.Status != "queued" || task.InstanceID != nil {
		t.Fatalf("failed placement claimed task=%+v %v", task, err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM instances WHERE job_id=$1", job.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed placement left instances=%d %v", count, err)
	}
	instance, err := store.CreateAndClaimJobInstance(ctx, instanceID, job.ID, run.ID, 0, "cold_booting", 128, peer.ID, instanceID, leaseToken, time.Now().Add(time.Minute), local.ID)
	if err != nil || instance.NodeID != peer.ID {
		t.Fatalf("regional claim=%+v %v", instance, err)
	}
	_, err = pool.Exec(ctx, "UPDATE instances SET node_id=$2 WHERE id=$1", instanceID, local.ID)
	requireRegionConstraint(t, err, "instances_compute_region")
	if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: "Jobs", AcceptingNewApps: false}); err != nil {
		t.Fatal(err)
	}
	_, err = store.JobCreateScheduledIfUnderQuota(ctx, state.Job{AccountID: acct.ID, Name: "paused-jobs", Kind: "batch", ImageRef: job.ImageRef, Command: job.Command, RAMMB: 128, TaskTimeoutS: 60, MaxParallelism: 1, ComputeRegion: region}, 10)
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeComputeRegionAdmissionPaused {
		t.Fatalf("paused insert=%v", err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO jobs (account_id,kind,name,image_ref,ram_mb,task_timeout_s,max_parallelism,command,compute_region) VALUES ($1,'batch','sql-jobs','image',128,60,1,ARRAY['/bin/true'],$2)", acct.ID, region)
	requireRegionConstraint(t, err, "compute_region_admission_paused")
}
