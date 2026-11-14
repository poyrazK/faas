package state_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261031193635 slice 6c: the bridge object relay admits only keys of
// the caller's own branch buckets, never the parent's.
func TestMemEnvironmentBranchObjectRelay(t *testing.T) {
	environmentBranchObjectRelay(t, state.NewMemStore())
}

func environmentBranchObjectRelay(t *testing.T, s contractTestStore) {
	t.Helper()
	ctx := context.Background()
	acct, project, branch, deployment := seedProjectEnvironmentBranch(t, s, "object-relay@example.com")
	source := readyObjectBucket(t, s, acct.ID, deployment.AppID, "assets", "production")
	copyBucket := readyObjectBucket(t, s, acct.ID, deployment.AppID, "assets", branch.Slug)
	row, err := s.RecordProjectEnvironmentBranchBucket(ctx, state.ProjectEnvironmentBranchBucket{AccountID: acct.ID, ProjectID: project.ID,
		EnvironmentID: branch.EnvironmentID, EnvironmentSlug: branch.Slug, AppID: deployment.AppID, SourceBucketID: source.ID})
	if err != nil {
		t.Fatal(err)
	}
	credential := func(bucket state.ObjectBucket, key string) state.ObjectS3Credential {
		t.Helper()
		c, err := s.CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: uuid.NewString(), AccountID: acct.ID, BucketID: bucket.ID,
			AccessKeyID: key, SecretSealed: []byte("sealed"), KID: "kid", Label: "relay", Permission: state.ObjectBucketPermissionWrite,
			Status: state.ObjectS3CredentialStatusActive}, 100)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	branchKey := credential(copyBucket, "GRGABRANCHRELAYKEYAA")
	credential(source, "GRGAPARENTRELAYKEYAA")
	allowed := func(environmentID, key string) bool {
		t.Helper()
		ok, err := s.EnvironmentBranchObjectCredentialAllowed(ctx, environmentID, key)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if allowed(branch.EnvironmentID, branchKey.AccessKeyID) {
		t.Fatal("a branch bucket key is allowed only once the bucket is the branch's copy")
	}
	if err := s.SetProjectEnvironmentBranchBucketCopy(ctx, row.ID, copyBucket.ID); err != nil {
		t.Fatal(err)
	}
	if !allowed(branch.EnvironmentID, branchKey.AccessKeyID) {
		t.Fatal("the branch's own bucket key must be allowed")
	}
	for name, tc := range map[string]struct{ environment, key string }{
		"parent bucket key": {branch.EnvironmentID, "GRGAPARENTRELAYKEYAA"},
		"unknown key":       {branch.EnvironmentID, "GRGAUNKNOWNRELAYKEYA"},
		"other environment": {uuid.NewString(), branchKey.AccessKeyID},
		"malformed env":     {"not-a-uuid", branchKey.AccessKeyID},
		"empty key":         {branch.EnvironmentID, " "},
		"empty environment": {"", branchKey.AccessKeyID},
	} {
		if allowed(tc.environment, tc.key) {
			t.Errorf("%s: allowed", name)
		}
	}
	if err := s.RevokeObjectS3Credential(ctx, acct.ID, copyBucket.ID, branchKey.ID); err != nil {
		t.Fatal(err)
	}
	if allowed(branch.EnvironmentID, branchKey.AccessKeyID) {
		t.Fatal("a revoked key must not be allowed")
	}
}
