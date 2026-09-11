package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// Second batch of PgStore coverage round-trips: node-sharding reads
// (Phase 2 / Gate A), deployment readers + rootfs/source-url stamping,
// ListEnabledCrons, and the instance-list variants. All run against a
// fresh migrated schema via pgStore(t).

func TestPg_CoverageNodeSharding(t *testing.T) {
	s, ctx, account, app, _ := pgCoverageFixture(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	missingID := uuid.NewString()

	// SetAppNodeID — claim the unplaced app, then conflict on re-claim.
	if err := s.SetAppNodeID(ctx, app.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAppNodeID(ctx, app.ID, nodeID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("re-claim = %v", err)
	}
	if err := s.SetAppNodeID(ctx, missingID, nodeID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim missing = %v", err)
	}
	// ListAppsByNodeID — the claimed app is now owned by nodeID.
	if got, err := s.ListAppsByNodeID(ctx, nodeID); err != nil || len(got) != 1 || got[0].ID != app.ID {
		t.Fatalf("apps by node = %+v, %v", got, err)
	}
	// ListUnplacedApps — after the claim, nothing is unplaced.
	if got, err := s.ListUnplacedApps(ctx); err != nil || len(got) != 0 {
		t.Fatalf("unplaced after claim = %+v, %v", got, err)
	}
	// A second app stays unplaced until claimed.
	app2, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-cov-unplaced-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListUnplacedApps(ctx); err != nil || len(got) != 1 || got[0].ID != app2.ID {
		t.Fatalf("unplaced = %+v, %v", got, err)
	}
	// ListInstancesByNodeID + ListOwnedCronsByNodeID.
	if got, err := s.ListInstancesByNodeID(ctx, nodeID); err != nil || len(got) != 0 {
		t.Fatalf("instances by node = %+v, %v", got, err)
	}
	cron, err := s.CreateCron(ctx, app2.ID, "* * * * *", "/health", true)
	if err != nil {
		t.Fatal(err)
	}
	_ = cron
	if got, err := s.ListOwnedCronsByNodeID(ctx, nodeID); err != nil || len(got) != 0 {
		// app2 is unplaced (node_id NULL) so its cron is not owned by
		// nodeID.
		t.Fatalf("crons by node = %+v, %v", got, err)
	}
	if err := s.SetAppNodeID(ctx, app2.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListOwnedCronsByNodeID(ctx, nodeID); err != nil || len(got) != 1 {
		t.Fatalf("crons by node after claim = %+v, %v", got, err)
	}
	// ListEnabledCrons.
	if got, err := s.ListEnabledCrons(ctx); err != nil || len(got) != 1 {
		t.Fatalf("enabled crons = %+v, %v", got, err)
	}
}

func TestPg_CoverageOrphanedAndReassign(t *testing.T) {
	s, ctx, account, app, _ := pgCoverageFixture(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	// Mark the default node inactive so the app (owned by it) becomes
	// orphaned.
	if err := s.MarkComputeNodeInactive(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAppNodeID(ctx, app.ID, nodeID); err != nil {
		t.Fatal(err)
	}
	// ListOrphanedApps — cooldown 0 shows the app (ReassignedAt NULL is
	// always eligible).
	if got, err := s.ListOrphanedApps(ctx, 0, 10); err != nil || len(got) != 1 || got[0].ID != app.ID {
		t.Fatalf("orphaned = %+v, %v", got, err)
	}
	// Reassign to an active node (stamps reassigned_at = now()), then
	// mark it inactive again so the app is orphaned WITH a recent
	// reassigned_at — the cooldown filter now hides it.
	activeNode, err := s.CreateComputeNode(ctx, state.ComputeNode{Name: "pg-active-" + uuid.NewString(), TargetURL: "unix:///run/faas/vmmd.sock", Active: true, AdmissionCeilingMB: 4096, VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReassignAppOwner(ctx, app.ID, nodeID, activeNode.ID); err != nil {
		t.Fatalf("reassign = %v", err)
	}
	if err := s.MarkComputeNodeInactive(ctx, activeNode.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListOrphanedApps(ctx, 3600, 10); err != nil || len(got) != 0 {
		t.Fatalf("orphaned cooldown = %+v, %v", got, err)
	}
	// Reassign with a stale fromNodeID → ErrConflict.
	if err := s.ReassignAppOwner(ctx, app.ID, nodeID, activeNode.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reassign stale from = %v", err)
	}
	// Reassign missing app → ErrConflict (RowsAffected()==0 is
	// indistinguishable from a lost race in the PgStore contract).
	if err := s.ReassignAppOwner(ctx, uuid.NewString(), nodeID, activeNode.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reassign missing = %v", err)
	}
	_ = account
}

func TestPg_CoverageDeploymentReaders(t *testing.T) {
	s, ctx, _, app, deployment := pgCoverageFixture(t)
	// LatestDeployment + LatestSupersededDeployment.
	if got, err := s.LatestDeployment(ctx, app.ID); err != nil || got.ID != deployment.ID {
		t.Fatalf("latest deployment = %+v, %v", got, err)
	}
	if _, err := s.LatestSupersededDeployment(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("superseded before = %v", err)
	}
	// A second deployment leaves the first serving until promotion.
	second, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:second", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.LatestDeployment(ctx, app.ID); err != nil || got.ID != second.ID {
		t.Fatalf("latest after second = %+v, %v", got, err)
	}
	if err := s.MarkDeploymentLive(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.LatestSupersededDeployment(ctx, app.ID); err != nil || got.ID != deployment.ID {
		t.Fatalf("superseded = %+v, %v", got, err)
	}
	// SetDeploymentRootfs (hit + miss).
	if err := s.SetDeploymentRootfs(ctx, second.ID, "/srv/fc/rootfs", "apps/slug/dep.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeploymentByID(ctx, second.ID)
	if err != nil || got.RootfsPath != "/srv/fc/rootfs" || got.RootfsKey != "apps/slug/dep.ext4" || got.RootfsBytes != 4096 {
		t.Fatalf("rootfs = %+v, %v", got, err)
	}
	if err := s.SetDeploymentRootfs(ctx, uuid.NewString(), "p", "k", 1); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rootfs missing = %v", err)
	}
	// SetDeploymentSourceURL (hit + miss). commit_sha must satisfy the
	// DB shape CHECK (64 hex chars).
	if err := s.SetDeploymentSourceURL(ctx, second.ID, "https://github.com/acme/app", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentSourceURL(ctx, uuid.NewString(), "url", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("source url missing = %v", err)
	}
}

func TestPg_CoverageInstanceLists(t *testing.T) {
	s, ctx, account, app, deployment := pgCoverageFixture(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	// CreateInstance + the list variants. wakeID must be a valid uuid.
	ins, err := s.CreateInstance(ctx, app.ID, deployment.ID, "running", 512, nodeID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListInstancesForApp(ctx, app.ID); err != nil || len(got) != 1 || got[0].ID != ins.ID {
		t.Fatalf("instances for app = %+v, %v", got, err)
	}
	if got, err := s.ListLatestInstancesForApp(ctx, app.ID, 10); err != nil || len(got) != 1 {
		t.Fatalf("latest instances = %+v, %v", got, err)
	}
	if got, err := s.ListAllInstances(ctx); err != nil || len(got) != 1 {
		t.Fatalf("all instances = %+v, %v", got, err)
	}
	if got, err := s.ListInstancesForAccountPaged(ctx, account.ID, 10, ""); err != nil || len(got) != 1 {
		t.Fatalf("paged = %+v, %v", got, err)
	}
	// UpsertComputeNode + ListComputeNodes + SetComputeNodeActive.
	if _, err := s.UpsertComputeNode(ctx, state.ComputeNode{Name: "pg-upsert", TargetURL: "unix:///run/faas/vmmd.sock", Active: true, AdmissionCeilingMB: 4096, VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, MaxConcurrency: 4}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListComputeNodes(ctx, true); err != nil || len(got) < 2 {
		t.Fatalf("list compute nodes = %+v, %v", got, err)
	}
	// AppendComputeNodeHeartbeat + ListComputeNodeHeartbeats. Source must
	// satisfy the CHECK ('heartbeat_tick'|'deactivation'|'reactivation').
	if err := s.AppendComputeNodeHeartbeat(ctx, nodeID, time.Now(), time.Now(), "heartbeat_tick"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListComputeNodeHeartbeats(ctx, nodeID, time.Time{}, 10); err != nil || len(got) != 1 {
		t.Fatalf("heartbeats = %+v, %v", got, err)
	}
}
