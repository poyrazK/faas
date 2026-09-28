package imaged

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeGitHubSourceRefVerifier struct {
	sha            string
	found          bool
	err            error
	accountID      string
	installationID int64
	repo           string
	branch         string
}

func (f *fakeGitHubSourceRefVerifier) GetBranchHead(_ context.Context, accountID string, installationID int64, repo, branch string) (string, bool, error) {
	f.accountID = accountID
	f.installationID = installationID
	f.repo = repo
	f.branch = branch
	return f.sha, f.found, f.err
}

func TestGitHubSourceRefFreshness(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "source-ref@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "source-ref", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	commit := "abcdef0123456789abcdef0123456789abcdef01"
	dep := state.Deployment{
		AppID: app.ID, SourceURL: "github://onebox-faas/hello@" + commit,
		CommitSHA: commit, GitHubSourceRef: "release/2026-q3", GitHubInstallationID: 7777,
	}

	t.Run("fresh branch", func(t *testing.T) {
		verifier := &fakeGitHubSourceRefVerifier{sha: commit, found: true}
		h := New(store, nil, nil, nil, "", "", nil).WithGitHubSourceRefVerifier(verifier)
		stale, err := h.gitHubSourceRefIsStale(ctx, dep)
		if err != nil || stale {
			t.Fatalf("freshness = (%v, %v), want (false, nil)", stale, err)
		}
		if verifier.accountID != account.ID || verifier.installationID != 7777 || verifier.repo != "onebox-faas/hello" || verifier.branch != "release/2026-q3" {
			t.Fatalf("branch lookup identity = (%q, %d, %q, %q)", verifier.accountID, verifier.installationID, verifier.repo, verifier.branch)
		}
	})

	t.Run("moved branch", func(t *testing.T) {
		verifier := &fakeGitHubSourceRefVerifier{sha: "1111111111111111111111111111111111111111", found: true}
		h := New(store, nil, nil, nil, "", "", nil).WithGitHubSourceRefVerifier(verifier)
		stale, err := h.gitHubSourceRefIsStale(ctx, dep)
		if err != nil || !stale {
			t.Fatalf("freshness = (%v, %v), want (true, nil)", stale, err)
		}
	})

	t.Run("deleted branch", func(t *testing.T) {
		verifier := &fakeGitHubSourceRefVerifier{}
		h := New(store, nil, nil, nil, "", "", nil).WithGitHubSourceRefVerifier(verifier)
		stale, err := h.gitHubSourceRefIsStale(ctx, dep)
		if err != nil || !stale {
			t.Fatalf("freshness = (%v, %v), want (true, nil)", stale, err)
		}
	})

	t.Run("lookup failure", func(t *testing.T) {
		wantErr := errors.New("github unavailable")
		verifier := &fakeGitHubSourceRefVerifier{err: wantErr}
		h := New(store, nil, nil, nil, "", "", nil).WithGitHubSourceRefVerifier(verifier)
		stale, err := h.gitHubSourceRefIsStale(ctx, dep)
		if stale || !errors.Is(err, wantErr) {
			t.Fatalf("freshness = (%v, %v), want (false, wrapped lookup error)", stale, err)
		}
	})

	t.Run("pinned input skips lookup", func(t *testing.T) {
		h := New(store, nil, nil, nil, "", "", nil)
		pinned := dep
		pinned.GitHubSourceRef = ""
		pinned.GitHubInstallationID = 0
		stale, err := h.gitHubSourceRefIsStale(ctx, pinned)
		if stale || err != nil {
			t.Fatalf("pinned freshness = (%v, %v), want (false, nil)", stale, err)
		}
	})
}
