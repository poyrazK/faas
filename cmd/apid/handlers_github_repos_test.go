package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestListGitHubRepositoriesReturnsAccountOwnedCatalog(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := store.UpsertGitHubInstall(t.Context(), state.GitHubInstall{
		AccountID: acct.ID, InstallationID: 42, DefaultBranch: "main",
		SealedToken: []byte("sealed"), AuditGithubLogin: "alice",
	}); err != nil {
		t.Fatalf("UpsertGitHubInstall: %v", err)
	}
	gh := &githubConnectionFake{repos: []Repo{{ID: 7, FullName: "acme/api", DefaultBranch: "main", Private: true}}}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}, "", noopMailer{}, gh, nil, nil, 0, "")

	req := httptest.NewRequest(http.MethodGet, "/v1/github/repos", nil)
	req = req.WithContext(WithAccount(req.Context(), acct))
	rec := httptest.NewRecorder()
	srv.listGitHubRepositories(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []api.RepoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 1 || got[0].ID != 7 || got[0].FullName != "acme/api" || !got[0].Private {
		t.Fatalf("repositories = %+v", got)
	}
}

func TestListGitHubRepositoriesRequiresInstallation(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}, "", noopMailer{}, &githubConnectionFake{}, nil, nil, 0, "")
	req := httptest.NewRequest(http.MethodGet, "/v1/github/repos", nil)
	rec := httptest.NewRecorder()
	srv.listGitHubRepositories(rec, req, acct)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodeGitHubInstallNotFound {
		t.Fatalf("problem code = %q, want %q", problem.Code, api.CodeGitHubInstallNotFound)
	}
}
