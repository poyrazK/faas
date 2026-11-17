//go:build !no_pg

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLoadPRPreviewSetCheck_CurrentHeadAndFullClosure(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID, projectID := uuid.NewString(), uuid.NewString()
	rootID, workerID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into accounts (id, email) values ($1, $2)`, accountID, "preview-set-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	// Projects belong to an organization since #4882; a raw account insert
	// has none, so seed the personal org the store would have created.
	if _, err := pool.Exec(ctx, `insert into orgs (id, slug, name, personal_org, personal_owner_account_id, plan, status)
		select gen_random_uuid(), 'u-' || substring(replace(a.id::text, '-', '') from 1 for 12), 'Personal', true, a.id, a.plan, a.status
		from accounts a where a.id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into projects (id, account_id, slug) values ($1, $2, $3)`, projectID, accountID, "preview-set-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	for _, app := range []struct{ id, slug, workload, parent string }{
		{rootID, "pr-42-preview-set-api", "api", "preview-set-api"},
		{workerID, "pr-42-preview-set-worker", "worker", "preview-set-worker"},
	} {
		if _, err := pool.Exec(ctx, `insert into apps
			(id, account_id, slug, ram_mb, max_concurrency, project_id, workload_name,
			 preview_of_slug, preview_pr_number, preview_pr_state)
			values ($1, $2, $3, 256, 1, $4, $5, $6, 42, 'open')`,
			app.id, accountID, app.slug, projectID, app.workload, app.parent); err != nil {
			t.Fatal(err)
		}
	}
	shaA, shaB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	sets := state.NewPgStore(pool)
	set := state.PRPreviewSet{InstallationID: 77, RepoFullName: "octo/preview-set", PRNumber: 42,
		CommitSHA: shaA, RootAppID: rootID, MemberAppIDs: []string{workerID, rootID}}
	if err := sets.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	insertDeployment := func(appID, sha, status string, createdAt time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `insert into deployments (id, app_id, image_digest, kind, status, commit_sha, created_at)
			values ($1, $2, 'sha256:preview-set-fixture', 'preview', $3, $4, $5)`,
			uuid.NewString(), appID, status, sha, createdAt); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	insertDeployment(rootID, shaA, "live", now)
	insertDeployment(workerID, shaA, "building", now)
	check, err := loadPRPreviewSetCheck(ctx, pool, 77, set.RepoFullName, 42, shaA)
	if err != nil || !check.CurrentHead || check.Phase != githubdgrpc.CheckPhaseBuilding || check.RootSlug != "pr-42-preview-set-api" {
		t.Fatalf("partial set check = (%+v, %v)", check, err)
	}
	if _, err := pool.Exec(ctx, `update deployments set status = 'failed' where app_id = $1 and commit_sha = $2`, workerID, shaA); err != nil {
		t.Fatal(err)
	}
	check, err = loadPRPreviewSetCheck(ctx, pool, 77, set.RepoFullName, 42, shaA)
	if err != nil || check.Phase != githubdgrpc.CheckPhaseFailed || !strings.Contains(check.Summary, "worker") {
		t.Fatalf("failed sibling check = (%+v, %v)", check, err)
	}
	insertDeployment(workerID, shaA, "live", now.Add(time.Second))
	check, err = loadPRPreviewSetCheck(ctx, pool, 77, set.RepoFullName, 42, shaA)
	if err != nil || check.Phase != githubdgrpc.CheckPhaseLive {
		t.Fatalf("all-live set check = (%+v, %v)", check, err)
	}
	environment, err := sets.PRPreviewEnvironmentByRoot(ctx, rootID)
	if err != nil || environment.Set.CommitSHA != shaA || len(environment.Members) != 2 ||
		environment.Members[0].DeploymentStatus != "live" || environment.Members[0].DeploymentID == "" {
		t.Fatalf("customer environment projection = (%+v, %v)", environment, err)
	}
	// GitHub keys the static check by repository and commit, not PR. A second
	// PR pointing at the same SHA must hold the shared check until it is live.
	otherRootID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into apps
		(id, account_id, slug, ram_mb, max_concurrency, project_id, workload_name,
		 preview_of_slug, preview_pr_number, preview_pr_state)
		values ($1, $2, 'pr-43-preview-set-api', 256, 1, $3, 'api', 'preview-set-api', 43, 'open')`,
		otherRootID, accountID, projectID); err != nil {
		t.Fatal(err)
	}
	otherSet := state.PRPreviewSet{InstallationID: 77, RepoFullName: set.RepoFullName, PRNumber: 43,
		CommitSHA: shaA, RootAppID: otherRootID, MemberAppIDs: []string{otherRootID}}
	if err := sets.PutPRPreviewSet(ctx, otherSet); err != nil {
		t.Fatal(err)
	}
	check, err = loadPRPreviewSetCheck(ctx, pool, 77, set.RepoFullName, 42, shaA)
	if err != nil || check.Phase != githubdgrpc.CheckPhaseBuilding || !strings.Contains(check.Summary, "1/2 PR environments") || !strings.Contains(check.CommentSummary, "all 2 workloads") {
		t.Fatalf("shared SHA with unfinished PR = (%+v, %v)", check, err)
	}
	insertDeployment(otherRootID, shaA, "live", now)
	check, err = loadPRPreviewSetCheck(ctx, pool, 77, set.RepoFullName, 42, shaA)
	if err != nil || check.Phase != githubdgrpc.CheckPhaseLive || !strings.Contains(check.Summary, "all 2 PR environments") {
		t.Fatalf("shared SHA with both PRs live = (%+v, %v)", check, err)
	}
	set.CommitSHA, set.MemberAppIDs = shaB, []string{rootID}
	if err := sets.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	check, err = loadPRPreviewSetCheck(ctx, pool, 77, set.RepoFullName, 42, shaA)
	if err != nil || check.CurrentHead {
		t.Fatalf("old head may overwrite new check: (%+v, %v)", check, err)
	}
	check, err = loadPRPreviewSetCheck(ctx, pool, 77, set.RepoFullName, 42, shaB)
	if err != nil || !check.CurrentHead || check.Phase != githubdgrpc.CheckPhaseBuilding {
		t.Fatalf("new head with no deployment = (%+v, %v)", check, err)
	}
	environment, err = sets.PRPreviewEnvironmentByRoot(ctx, rootID)
	if err != nil || environment.Set.CommitSHA != shaB || len(environment.Members) != 1 || environment.Members[0].DeploymentStatus != "missing" {
		t.Fatalf("new head inherited old deployment: (%+v, %v)", environment, err)
	}
	if err := sets.ClosePRPreviewSet(ctx, 77, set.RepoFullName, 42); err != nil {
		t.Fatal(err)
	}
	check, err = loadPRPreviewSetCheck(ctx, pool, 77, set.RepoFullName, 42, shaB)
	if err != nil || check.CurrentHead {
		t.Fatalf("closed PR may overwrite check: (%+v, %v)", check, err)
	}
	if _, err := pool.Exec(ctx, `update apps set status = 'deleted' where id = $1`, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := sets.GetPRPreviewSet(ctx, 77, set.RepoFullName, 42); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("tombstoned root retained preview set: %v", err)
	}
}

// A push that skips a member pins it to the commit its live deployment was
// built from; the check and the customer projection read that member there,
// and the next head drops the pins (ADR-20261108150000).
func TestLoadPRPreviewSetCheck_PinnedMemberReadAtItsCommit(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID, projectID := uuid.NewString(), uuid.NewString()
	rootID, workerID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into accounts (id, email) values ($1, $2)`, accountID, "preview-pin-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs (id, slug, name, personal_org, personal_owner_account_id, plan, status)
		select gen_random_uuid(), 'u-' || substring(replace(a.id::text, '-', '') from 1 for 12), 'Personal', true, a.id, a.plan, a.status
		from accounts a where a.id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into projects (id, account_id, slug) values ($1, $2, $3)`, projectID, accountID, "preview-pin-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	for _, app := range []struct{ id, slug, workload, parent string }{
		{rootID, "pr-42-preview-pin-api", "api", "preview-pin-api"},
		{workerID, "pr-42-preview-pin-worker", "worker", "preview-pin-worker"},
	} {
		if _, err := pool.Exec(ctx, `insert into apps
			(id, account_id, slug, ram_mb, max_concurrency, project_id, workload_name,
			 preview_of_slug, preview_pr_number, preview_pr_state)
			values ($1, $2, $3, 256, 1, $4, $5, $6, 42, 'open')`,
			app.id, accountID, app.slug, projectID, app.workload, app.parent); err != nil {
			t.Fatal(err)
		}
	}
	insertDeployment := func(appID, sha, status string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `insert into deployments (id, app_id, image_digest, kind, status, commit_sha, created_at)
			values ($1, $2, 'sha256:preview-pin-fixture', 'preview', $3, $4, now())`,
			uuid.NewString(), appID, status, sha); err != nil {
			t.Fatal(err)
		}
	}
	shaA, shaB, shaC := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	sets := state.NewPgStore(pool)
	set := state.PRPreviewSet{InstallationID: 78, RepoFullName: "octo/preview-pin", PRNumber: 42,
		CommitSHA: shaB, RootAppID: rootID, MemberAppIDs: []string{workerID, rootID}}
	if err := sets.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	insertDeployment(workerID, shaA, "live")
	insertDeployment(rootID, shaB, "live")
	check, err := loadPRPreviewSetCheck(ctx, pool, 78, set.RepoFullName, 42, shaB)
	if err != nil || check.Phase != githubdgrpc.CheckPhaseBuilding {
		t.Fatalf("unpinned worker at old commit = (%+v, %v), want building", check, err)
	}
	head := state.PRPreviewHead{InstallationID: 78, RepoFullName: set.RepoFullName, PRNumber: 42, CommitSHA: shaB}
	stale := head
	stale.CommitSHA = shaA
	if err := sets.PinPRPreviewMembers(ctx, stale, map[string]string{workerID: shaC}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("pin for a superseded head = %v, want not found", err)
	}
	if err := sets.PinPRPreviewMembers(ctx, head, map[string]string{uuid.NewString(): shaA}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("pin for a non-member = %v, want not found", err)
	}
	if err := sets.PinPRPreviewMembers(ctx, head, map[string]string{workerID: shaA}); err != nil {
		t.Fatal(err)
	}
	check, err = loadPRPreviewSetCheck(ctx, pool, 78, set.RepoFullName, 42, shaB)
	if err != nil || !check.CurrentHead || check.Phase != githubdgrpc.CheckPhaseLive {
		t.Fatalf("pinned worker check = (%+v, %v), want live", check, err)
	}
	environment, err := sets.PRPreviewEnvironmentByRoot(ctx, rootID)
	if err != nil || environment.Set.MemberCommit(workerID) != shaA || len(environment.Members) != 2 ||
		environment.Members[0].DeploymentStatus != "live" || environment.Members[1].DeploymentStatus != "live" {
		t.Fatalf("pinned customer projection = (%+v, %v)", environment, err)
	}
	// The previous release moves commit_sha without knowing the pins; they
	// must not apply to that new head.
	if _, err := pool.Exec(ctx, `update pr_preview_sets set commit_sha = $1 where root_app_id = $2`, shaC, rootID); err != nil {
		t.Fatal(err)
	}
	got, err := sets.GetPRPreviewSet(ctx, 78, set.RepoFullName, 42)
	if err != nil || got.MemberCommitSHAs != nil || got.MemberCommit(workerID) != shaC {
		t.Fatalf("pins leaked to an old-release head = (%+v, %v)", got, err)
	}
	set.CommitSHA = shaC
	if err := sets.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	var pins, pinsHead *string
	if err := pool.QueryRow(ctx, `select member_pins::text, member_pins_head from pr_preview_sets where root_app_id = $1`, rootID).Scan(&pins, &pinsHead); err != nil || pins != nil || pinsHead != nil {
		t.Fatalf("new head kept pins: %v %v %v", pins, pinsHead, err)
	}
}
