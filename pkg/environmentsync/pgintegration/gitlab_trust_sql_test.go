package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gitapproval"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgGitLabApprovalGuardRejectsCrossProviderSQL(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	source, desired := seed(t, store)
	source = bindGitLabSource(t, store, source, "protected_branch")
	now := time.Now().UTC()
	polls := state.EnvironmentGitSourcePollStore(store)
	lease, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	evidence := reviewedMergeFixture(source, sha)
	evidence.Profile = gitapproval.GitLabReviewedMergeProfile
	evidence.Policy.Profile = gitapproval.GitLabProtectedBranchProfile
	evidence.ReviewedDefinitionDigest = desired.Digest
	if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), lease, state.EnvironmentGitSourcePollResult{CommitSHA: sha, Digest: desired.Digest, Desired: &desired, Approval: &evidence}, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, source.EnvironmentSlug)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"control", "GitHub merge profile", "GitHub branch profile", "GitHub installation"} {
		t.Run(name, func(t *testing.T) {
			proof := evidence
			switch name {
			case "GitHub merge profile":
				proof.Profile = gitapproval.ReviewedMergeProfile
			case "GitHub branch profile":
				proof.Policy.Profile = gitapproval.ProtectedBranchProfile
			case "GitHub installation":
				proof.Policy.InstallationID = 42
			}
			raw, err := json.Marshal(proof)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			_, err = tx.Exec(t.Context(), `INSERT INTO environment_git_revision_approvals(source_id,revision_id,approved_generation,definition_digest,evidence,poll_lease_token) VALUES($1,$2,$3,$4,$5,$6)`, source.ID, current.ApprovedRevisionID, current.Generation+1, desired.Digest, raw, fresh.LeaseToken)
			if name == "control" {
				if err != nil {
					t.Fatalf("valid provider SQL receipt rejected: %v", err)
				}
				return
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatalf("cross-provider SQL evidence accepted: %v", err)
			}
		})
	}
}
