// adr: 1139
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemQualificationRecordsProjectSecrets(t *testing.T) {
	testQualificationRecordsProjectSecrets(t, state.NewMemStore())
}

// Legacy clients work for environments that never had shared secrets. Once
// the set changes, clients must return the revision read before their probes.
func testQualificationRecordsProjectSecrets(t *testing.T, store contractTestStore) {
	t.Helper()
	ctx := context.Background()
	account, project, releases := testProjectReleaseReadContract(t, store)
	active := releases[len(releases)-1]
	httpOK := 204
	result := state.ProjectEnvironmentQualificationResult{WorkloadSlug: "release-read-api", DeploymentID: active.Members[0].DeploymentID, Status: "passed", HTTPStatus: &httpOK}
	checks := []state.ProjectEnvironmentQualificationCheck{
		{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}},
		{Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{result}},
	}
	workloadHash, err := api.ProjectEnvironmentSecretRevisionHash(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := map[string]string{"release-read-api": workloadHash}
	configHash := api.EmptyProjectEnvironmentConfigHash()

	before, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0, configHash, client, checks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := before.SecretRevisionHashes[api.ProjectSecretsRevisionKey]; recorded || len(before.SecretRevisionHashes) != 1 {
		t.Fatalf("hashes without shared secrets = %v, want only the workload's", before.SecretRevisionHashes)
	}

	if _, err := store.SetProjectEnvironmentSecret(ctx, state.ProjectEnvironmentSecret{AccountID: account.ID, ProjectID: project.ID,
		EnvironmentSlug: "production", Key: "DATABASE_PASSWORD", Ciphertext: []byte("sealed"),
		Kid: "age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3290gq", ValueHash: "ab"}, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0, configHash, client, checks, nil); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("legacy client silently qualified shared secrets: %v", err)
	}
	shared, err := any(store).(state.ProjectSharedSecretRevisionReader).ProjectEnvironmentSharedSecretRevision(ctx, account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	client[api.ProjectSecretsRevisionKey] = shared.Hash
	after, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0, configHash, client, checks, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := shared.Hash
	if after.SecretRevisionHashes[api.ProjectSecretsRevisionKey] != want || after.SecretRevisionHashes["release-read-api"] != workloadHash {
		t.Fatalf("hashes with a shared secret = %v, want the workload's and %s=%s", after.SecretRevisionHashes, api.ProjectSecretsRevisionKey, want)
	}
	latest, err := store.LatestProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID)
	if err != nil || latest.SecretRevisionHashes[api.ProjectSecretsRevisionKey] != want {
		t.Fatalf("stored qualification = %+v, %v", latest.SecretRevisionHashes, err)
	}
}

func TestWithProjectSecretRevision(t *testing.T) {
	base := map[string]string{"api": "workload-hash"}
	if got, err := state.WithProjectSecretRevision(base, nil); err != nil || len(got) != 1 {
		t.Fatalf("no shared secrets = %v, %v", got, err)
	}
	one, err := state.WithProjectSecretRevision(base, []state.ProjectEnvironmentSecret{{Key: "A", SecretVersion: 1}})
	if err != nil || len(one) != 2 || one["api"] != "workload-hash" || one[api.ProjectSecretsRevisionKey] == "" {
		t.Fatalf("one shared secret = %v, %v", one, err)
	}
	if len(base) != 1 {
		t.Fatalf("the input map was modified: %v", base)
	}
	rotated, _ := state.WithProjectSecretRevision(base, []state.ProjectEnvironmentSecret{{Key: "A", SecretVersion: 2}})
	if rotated[api.ProjectSecretsRevisionKey] == one[api.ProjectSecretsRevisionKey] {
		t.Fatal("a new version kept the revision hash")
	}
	resealed, _ := state.WithProjectSecretRevision(base, []state.ProjectEnvironmentSecret{{Key: "A", SecretVersion: 1, Kid: "other", Ciphertext: []byte("x")}})
	if resealed[api.ProjectSecretsRevisionKey] != one[api.ProjectSecretsRevisionKey] {
		t.Fatal("an envelope change moved the revision hash")
	}
}
