package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectEnvironmentQualificationStore interface {
	state.ProjectEnvironmentQualificationStore
}

func TestMemProjectEnvironmentQualificationContract(t *testing.T) {
	testProjectEnvironmentQualificationContract(t, state.NewMemStore())
}

func testProjectEnvironmentQualificationContract(t *testing.T, store interface {
	projectEnvironmentQualificationStore
	state.ProjectReleaseSetStore
	state.ProjectReleaseSetReader
	state.Store
}) {
	t.Helper()
	ctx := context.Background()
	account, project, releases := testProjectReleaseReadContract(t, store)
	active := releases[len(releases)-1]
	httpOK, httpUnavailable := 204, 503
	passingResult := state.ProjectEnvironmentQualificationResult{
		WorkloadSlug: "release-read-api", DeploymentID: active.Members[0].DeploymentID,
		Status: "passed", HTTPStatus: &httpOK,
	}
	failingResult := state.ProjectEnvironmentQualificationResult{
		WorkloadSlug: "release-read-api", DeploymentID: active.Members[0].DeploymentID,
		Status: "failed", HTTPStatus: &httpUnavailable, ErrorCode: "unexpected_status",
	}
	checks := []state.ProjectEnvironmentQualificationCheck{
		{Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}},
		{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}},
	}
	configHash := api.EmptyProjectEnvironmentConfigHash()
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, active.Members[0].AppID, "production", "TOKEN", "age1test", "0123456789abcdef", []byte("sealed-v1")); err != nil {
		t.Fatal(err)
	}
	secretRevisionHash, err := api.ProjectEnvironmentSecretRevisionHash([]api.ProjectEnvironmentSecretRevision{{Key: "TOKEN", Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	secretRevisionHashes := map[string]string{"release-read-api": secretRevisionHash}
	created, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0, configHash, secretRevisionHashes, checks)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "passed" || created.ConfigurationVersion != 0 || created.ConfigurationHash != configHash ||
		created.SecretRevisionHashes["release-read-api"] != secretRevisionHash ||
		len(created.Checks) != 2 || created.Checks[0].Name != "health" ||
		created.Checks[1].Name != "smoke" || created.ExpiresAt.Sub(created.CreatedAt) != state.ProjectEnvironmentQualificationTTL {
		t.Fatalf("qualification receipt = %+v", created)
	}
	created.Checks[0].Status = "failed"
	created.Checks[0].Results[0].WorkloadSlug = "mutated"
	latest, err := store.LatestProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID)
	if err != nil || latest.ID != created.ID || latest.Checks[0].Status != "passed" || latest.Checks[0].Results[0].WorkloadSlug != "release-read-api" {
		t.Fatalf("latest qualification = %+v, %v", latest, err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, active.Members[0].AppID, "production", "TOKEN", "age1test", "fedcba9876543210", []byte("sealed-v2")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0, configHash, secretRevisionHashes, checks); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale secret qualification error = %v, want conflict", err)
	}
	secretRevisionHash, err = api.ProjectEnvironmentSecretRevisionHash([]api.ProjectEnvironmentSecretRevision{{Key: "TOKEN", Version: 2}})
	if err != nil {
		t.Fatal(err)
	}
	secretRevisionHashes["release-read-api"] = secretRevisionHash
	failed, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0, configHash, secretRevisionHashes,
		[]state.ProjectEnvironmentQualificationCheck{
			{Name: "health", Status: "failed", Results: []state.ProjectEnvironmentQualificationResult{failingResult}},
			{Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}},
		})
	if err != nil || failed.Status != "failed" {
		t.Fatalf("failed qualification = %+v, %v", failed, err)
	}
	latest, err = store.LatestProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID)
	if err != nil || latest.ID != failed.ID || latest.Status != "failed" {
		t.Fatalf("latest failed qualification = %+v, %v", latest, err)
	}
	if _, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", releases[0].ID, 0, configHash, secretRevisionHashes, checks); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retired release qualification error = %v, want conflict", err)
	}
	for _, invalid := range [][]state.ProjectEnvironmentQualificationCheck{
		{{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}},
		{{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}, {Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}},
		{{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}, {Name: "smoke", Status: "unknown", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}},
		{{Name: "logs", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}, {Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}},
		{{Name: "health", Status: "failed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}, {Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}},
		{{Name: "health", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{passingResult}}, {Name: "smoke", Status: "passed", Results: []state.ProjectEnvironmentQualificationResult{{WorkloadSlug: "release-read-api", DeploymentID: active.ID, Status: "passed", HTTPStatus: &httpOK}}}},
	} {
		if _, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0, configHash, secretRevisionHashes, invalid); !errors.Is(err, state.ErrInvalidArgument) {
			t.Errorf("invalid checks %v error = %v, want invalid argument", invalid, err)
		}
	}
	values, nextConfigHash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"region":"eu"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentConfigVersion(ctx, state.ProjectEnvironmentConfig{
		AccountID: account.ID, ProjectID: project.ID, EnvironmentSlug: "production",
		ConfigHash: nextConfigHash, Values: values,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", active.ID, 0, configHash, secretRevisionHashes, checks); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale config qualification error = %v, want conflict", err)
	}
	if _, err := store.LatestProjectEnvironmentQualification(ctx, account.ID, project.ID, "production", "not-a-uuid"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Errorf("invalid release ID error = %v, want invalid argument", err)
	}
	if time.Since(created.CreatedAt) > time.Minute {
		t.Fatalf("test receipt unexpectedly old: %s", created.CreatedAt)
	}
}
