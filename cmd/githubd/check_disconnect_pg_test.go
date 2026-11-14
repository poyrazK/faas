//go:build !no_pg

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentCheckDoesNotReuseDisconnectedPreviewHistory(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "check-disconnect@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGitHubInstall(ctx, state.GitHubInstall{AccountID: account.ID, InstallationID: 42, AuditGithubLogin: "octocat", SealedToken: []byte("sealed"), TokenExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "check-parent", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertGithubInstallBinding(ctx, state.GitHubBinding{AccountID: account.ID, AppID: parent.ID, BindingID: "bind-" + parent.ID, InstallID: 42, RepoFullName: "octo/shared", ProductionBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	preview, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pr-check-disconnect", RAMMB: 128, PreviewOfSlug: "check-parent", PreviewPrNumber: 42, PreviewPrState: state.PreviewPrStateOpen, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	if err := store.PutPRPreviewSet(ctx, state.PRPreviewSet{InstallationID: 42, RepoFullName: "octo/shared", PRNumber: 42, CommitSHA: sha, RootAppID: preview.ID, MemberAppIDs: []string{preview.ID}}); err != nil {
		t.Fatal(err)
	}
	deploymentID := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into deployments(id,app_id,image_digest,kind,status,commit_sha)values($1,$2,'sha256:fixture','preview','building',$3)`, deploymentID, preview.ID, sha); err != nil {
		t.Fatal(err)
	}
	if err := store.DisconnectGitHubAccountInstallation(ctx, account.ID, 42); err != nil {
		t.Fatal(err)
	}
	// A missing token/HTTP writer would fail if projection reached GitHub.
	if err := syncDeploymentCheck(ctx, pool, &githubd.ChecksAPI{}, deploymentID); err != nil {
		t.Fatalf("disconnected account recovered provider access: %v", err)
	}
}
