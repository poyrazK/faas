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
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID, projectID := uuid.NewString(), uuid.NewString()
	rootID, workerID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into accounts (id, email) values ($1, $2)`, accountID, "preview-set-"+uuid.NewString()+"@example.test"); err != nil {
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
