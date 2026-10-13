package pgintegration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsImageFullRootfsSQLFence(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, desired, lease := newWorkloadFixture(t, store, "enforce", api.PlanPro)
	intent := state.EnvironmentGitOpsIntentStore(store)
	workloads := state.EnvironmentGitOpsWorkloadCreationStore(store)
	plan := planForStore(t, intent, lease, desired)
	if _, err := workloads.PrepareEnvironmentGitOpsWorkloads(t.Context(), lease, plan); err != nil {
		t.Fatal(err)
	}
	plan = planForStore(t, intent, lease, desired)
	if _, err := intent.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
		t.Fatal(err)
	}
	plan = planForStore(t, intent, lease, desired)
	preparation := state.EnvironmentGitOpsPreparationStore(store)
	requests, err := preparation.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
	if err != nil || len(requests) != 1 {
		t.Fatalf("source requests: %+v %v", requests, err)
	}
	candidates, err := preparation.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan,
		map[string]state.EnvironmentWorkloadSourceArtifact{requests[0].Resource: sourceArtifact(t, requests[0])})
	if err != nil {
		t.Fatal(err)
	}
	var imageID string
	for _, candidate := range candidates {
		if candidate.Resource == "workload/api" {
			imageID = candidate.DeploymentID
		}
	}
	if imageID == "" {
		t.Fatal("reviewed image candidate missing")
	}
	// The old candidate trigger validates the approved source and lease. The
	// new guard must additionally reject a direct SQL candidate whose image
	// policy disagrees with that source, even when no live deployment exists.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_lease',$1,true)`, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), `insert into deployments
		(app_id,scope,kind,image_digest,commit_sha,status,traffic_percent,traffic_percent_explicit,revision,
		 environment_workload_runtime,environment_workload_held,full_rootfs_override)
		select app_id,scope,kind,image_digest,commit_sha,'pending',0,true,revision+1,
		 environment_workload_runtime,true,false from deployments where id=$1`, imageID)
	if err == nil || !strings.Contains(err.Error(), "reviewed image requires full-rootfs opt-in") {
		t.Fatalf("direct SQL changed reviewed image policy: %v", err)
	}
}
