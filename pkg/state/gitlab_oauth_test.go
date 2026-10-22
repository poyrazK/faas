package state_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGitLabOAuthCredentialRotationIsAccountScopedAndVersioned(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "gitlab-oauth@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(t.Context(), "other-gitlab-oauth@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := store.PutGitLabOAuthConnection(t.Context(), state.GitLabOAuthConnection{
		AccountID: account.ID, GitLabUserID: 41, Username: "developer", CredentialsSealed: []byte("sealed-one"), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil || initial.Version != 1 {
		t.Fatalf("initial connection = %+v, %v", initial, err)
	}
	if _, err := store.GitLabOAuthConnection(t.Context(), other.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read: %v", err)
	}
	initial.CredentialsSealed[0] = 'x'
	stored, err := store.GitLabOAuthConnection(t.Context(), account.ID)
	if err != nil || !bytes.Equal(stored.CredentialsSealed, []byte("sealed-one")) {
		t.Fatalf("stored connection changed: %+v, %v", stored, err)
	}
	if _, err := store.RotateGitLabOAuthConnection(t.Context(), account.ID, 2, []byte("sealed-two"), time.Now().Add(time.Hour)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale refresh accepted: %v", err)
	}
	rotated, err := store.RotateGitLabOAuthConnection(t.Context(), account.ID, 1, []byte("sealed-two"), time.Now().Add(time.Hour))
	if err != nil || rotated.Version != 2 || !bytes.Equal(rotated.CredentialsSealed, []byte("sealed-two")) {
		t.Fatalf("rotation = %+v, %v", rotated, err)
	}
	if err := store.DeleteGitLabOAuthConnection(t.Context(), account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GitLabOAuthConnection(t.Context(), account.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disconnected credential readable: %v", err)
	}
}

func TestGitLabSourceBindsStandaloneProjectAndSurvivesDisconnect(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "gitlab-source@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "standalone"})
	if err != nil {
		t.Fatal(err)
	}
	spec := state.EnvironmentGitSourceSpec{Provider: "gitlab", RepositoryID: 41, Repository: "team/subgroup/service",
		Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: "manual"}
	if _, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", spec); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unconnected GitLab source accepted: %v", err)
	}
	if _, err := store.PutGitLabOAuthConnection(t.Context(), state.GitLabOAuthConnection{AccountID: account.ID,
		GitLabUserID: 41, Username: "developer", CredentialsSealed: []byte("sealed"), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	bound, err := store.CreateEnvironmentGitSource(t.Context(), account.ID, project.ID, "production", spec)
	if err != nil || bound.Spec.Provider != "gitlab" || bound.Spec.InstallationID != 0 {
		t.Fatalf("standalone GitLab source = %+v, %v", bound, err)
	}
	if err := store.DeleteGitLabOAuthConnection(t.Context(), account.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := store.EnvironmentGitSource(t.Context(), account.ID, project.ID, "production")
	if err != nil || retained.ID != bound.ID {
		t.Fatalf("disconnected source was lost: %+v, %v", retained, err)
	}
}
