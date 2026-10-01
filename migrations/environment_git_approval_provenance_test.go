//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitApprovalProvenanceGuardsAndReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, "approval-evidence@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "approval-shop", RepoFullName: "example/shop", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "report", ApprovalPolicy: "protected_branch"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: "shop-api", Variables: map[string]string{"MODE": "production"}}}})
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := json.Marshal(desired.Definition)
	sha := strings.Repeat("a", 40)
	var legacyID string
	if err := pool.QueryRow(ctx, `insert into environment_desired_revisions(source_id,commit_sha,definition_digest,definition,approved_by) values($1,$2,$3,$4,'legacy') returning id`, source.ID, sha, desired.Digest, definition).Scan(&legacyID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update environment_git_sources set approved_revision_id=$2,generation=1 where id=$1`, source.ID, legacyID); err == nil {
		t.Fatal("raw SQL bypassed protected approval")
	}
	now := time.Now().UTC()
	head := strings.Repeat("b", 40)
	evidence := gitapproval.MergeEvidence{Qualified: true, Profile: gitapproval.ReviewedMergeProfile, Policy: gitapproval.PolicyEvidence{Qualified: true, Profile: gitapproval.ProtectedBranchProfile, InstallationID: 42, RepositoryID: 123, Repository: "example/shop", Branch: "main", CommitSHA: sha, PolicyDigest: strings.Repeat("c", 64), RequiredReviewCount: 1, CheckedAt: now}, PullRequestID: 1234, PullRequestNumber: 7, AuthorID: 10, HeadSHA: head, MergedAt: now.Add(-time.Hour), CheckedAt: now, Reviews: []gitapproval.ReviewEvidence{{ID: 1, ReviewerID: 11, Reviewer: "reviewer", HeadSHA: head, SubmittedAt: now.Add(-2 * time.Hour)}}}
	evidence.ReviewedDefinitionDigest = desired.Digest
	lease, err := store.ClaimEnvironmentGitSourcePoll(ctx, uuid.NewString(), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishEnvironmentGitSourcePoll(ctx, lease, state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest, Desired: &desired, Approval: &evidence}, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err := store.EnvironmentGitSource(ctx, account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.EnvironmentGitRevisionApproval(ctx, account.ID, source.ID, current.ApprovedRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`update environment_git_revision_approvals set evidence='{}' where source_id=$1`,
		`delete from environment_git_revision_approvals where source_id=$1`,
		`update environment_git_sources set source_ref='refs/heads/staging' where id=$1`,
		`update environment_git_sources set approval_policy='manual' where id=$1`,
		`update environment_git_sources set generation=0 where id=$1`,
		`update environment_desired_revisions set definition_digest=repeat('f',64) where source_id=$1`,
	} {
		if _, err := pool.Exec(ctx, statement, source.ID); err == nil {
			t.Fatalf("contract mutation accepted: %s", statement)
		}
	}
	versions := []int64{
		20260930183000001, 20260930183000002, 20260930183000003, 20260930183000004, 20260930183000005, 20260930183000006,
		20261001010000001, 20261001020000001, 20261001020000002, 20261001030000001, 20261001040000001, 20261001050000001,
		20261001060000001, 20261001070000001, 20261001070000002, 20261001080000001, 20261001080000002, 20261001080000003, 20261001081007501,
		20261001094704872, 20261001110831601, 20261001143949543, 20261001164005579,
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=any($1::bigint[])`, versions); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	retained, err := store.EnvironmentGitRevisionApproval(ctx, account.ID, source.ID, current.ApprovedRevisionID)
	if err != nil || !reflect.DeepEqual(retained, record) {
		t.Fatalf("replay changed provenance: %+v %v", retained, err)
	}
	if _, err := pool.Exec(ctx, `delete from accounts where id=$1`, account.ID); err != nil {
		t.Fatalf("history blocked account cascade: %v", err)
	}
}
