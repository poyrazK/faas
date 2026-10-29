//go:build !no_pg

package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// A commit that is no longer an open PR's recorded head is described so the
// check sync can complete its check: superseded by a newer head, or the
// closed PR's head with its environment's result. An open PR previewing the
// same commit still owns the shared check.
func TestLoadPRPreviewSetCheck_InactiveHeads(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID, projectID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into accounts (id, email) values ($1, $2)`, accountID, "preview-inactive-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into orgs (id, slug, name, personal_org, personal_owner_account_id, plan, status)
		select gen_random_uuid(), 'u-' || substring(replace(a.id::text, '-', '') from 1 for 12), 'Personal', true, a.id, a.plan, a.status
		from accounts a where a.id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into projects (id, account_id, slug) values ($1, $2, $3)`, projectID, accountID, "preview-inactive-"+uuid.NewString()[:8]); err != nil {
		t.Fatal(err)
	}
	suffix := uuid.NewString()[:8]
	insertPreview := func(pr int) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `insert into apps
			(id, account_id, slug, ram_mb, max_concurrency, project_id, workload_name,
			 preview_of_slug, preview_pr_number, preview_pr_state)
			values ($1, $2, $3, 256, 1, $4, 'api', $5, $6, 'open')`,
			id, accountID, "pr-"+strconv.Itoa(pr)+"-inactive-"+suffix, projectID, "inactive-"+suffix, pr); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insertDeployment := func(appID, sha, status string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `insert into deployments (id, app_id, image_digest, kind, status, commit_sha, created_at)
			values ($1, $2, 'sha256:preview-inactive-fixture', 'preview', $3, $4, $5)`,
			uuid.NewString(), appID, status, sha, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	const repo = "octo/preview-inactive"
	shaA, shaB := strings.Repeat("a", 40), strings.Repeat("b", 40)
	sets := state.NewPgStore(pool)
	rootID := insertPreview(42)
	set := state.PRPreviewSet{InstallationID: 78, RepoFullName: repo, PRNumber: 42,
		CommitSHA: shaA, RootAppID: rootID, MemberAppIDs: []string{rootID}}
	if err := sets.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	insertDeployment(rootID, shaA, "building")

	set.CommitSHA = shaB
	if err := sets.PutPRPreviewSet(ctx, set); err != nil {
		t.Fatal(err)
	}
	check, err := loadPRPreviewSetCheck(ctx, pool, 78, repo, 42, shaA)
	if err != nil || check.CurrentHead || check.SupersededBy != shaB {
		t.Fatalf("superseded head = (%+v, %v), want superseded by %s", check, err, shaB)
	}

	insertDeployment(rootID, shaB, "live")
	if err := sets.ClosePRPreviewSet(ctx, 78, repo, 42); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update apps set preview_pr_state = 'closed' where id = $1`, rootID); err != nil {
		t.Fatal(err)
	}
	check, err = loadPRPreviewSetCheck(ctx, pool, 78, repo, 42, shaB)
	if err != nil || check.CurrentHead || !check.ClosedHead || check.Phase != githubdgrpc.CheckPhaseLive {
		t.Fatalf("closed PR head = (%+v, %v), want its live result", check, err)
	}

	otherID := insertPreview(43)
	if err := sets.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: 78, RepoFullName: repo, PRNumber: 43,
		CommitSHA: shaB, RootAppID: otherID, MemberAppIDs: []string{otherID}}); err != nil {
		t.Fatal(err)
	}
	check, err = loadPRPreviewSetCheck(ctx, pool, 78, repo, 42, shaB)
	if err != nil || check.ClosedHead || check.SupersededBy != "" || check.CurrentHead {
		t.Fatalf("commit shared with an open PR = (%+v, %v), want nothing to report", check, err)
	}
}
