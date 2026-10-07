// adr: 641
package imaged

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestImageSnapshotGitHubBranchFreshness(t *testing.T) {
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name       string
		verifier   *fakeGitHubSourceRefVerifier
		pinned     bool
		incomplete bool
		wantCode   string
	}{
		{name: "fresh", verifier: &fakeGitHubSourceRefVerifier{sha: commit, found: true}},
		{name: "moved", verifier: &fakeGitHubSourceRefVerifier{sha: strings.Repeat("b", 40), found: true}, wantCode: api.CodeSourceRefStale},
		{name: "deleted", verifier: &fakeGitHubSourceRefVerifier{}, wantCode: api.CodeSourceRefStale},
		{name: "lookup unavailable", verifier: &fakeGitHubSourceRefVerifier{err: errors.New("GitHub unavailable")}, wantCode: api.CodeSourceRefUnavailable},
		{name: "verifier missing", wantCode: api.CodeSourceRefUnavailable},
		{name: "incomplete provenance", incomplete: true, wantCode: api.CodeSourceRefUnavailable},
		{name: "pinned image", pinned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			account, err := store.CreateAccount(ctx, "image-branch@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "image-branch", RAMMB: 256, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			stable, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
				t.Fatal(err)
			}
			input := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "example.com/api@sha256:" + commit + strings.Repeat("a", 24),
				SourceURL:   "https://codeload.github.com/owner/repo/tar.gz/" + commit,
				CommitSHA:   commit, GitHubSourceRef: "main", GitHubInstallationID: 42}
			if tc.pinned {
				input.GitHubSourceRef, input.GitHubInstallationID = "", 0
			} else if tc.incomplete {
				input.GitHubInstallationID = 0
			}
			candidate, err := store.CreateDeployment(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
			backend := mustLocalStorage(t, t.TempDir())
			notifier := &fakeNotifier{}
			h := New(store, notifier, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithStorage(backend)
			if tc.verifier != nil {
				h.WithGitHubSourceRefVerifier(tc.verifier)
			}
			key := state.SnapshotCaptureMemKey(candidate.ID, state.SnapshotTierInit, "image-branch")
			for _, part := range []string{key, state.SnapshotVMStateKey(state.Snapshot{StorageKey: key})} {
				if err := backend.Put(ctx, part, strings.NewReader(part)); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.handleSnapshotWritten(ctx, snapshotWrittenPayload{DeploymentID: candidate.ID, StorageKey: key, FCVersion: "1.10.0", Tier: state.SnapshotTierInit}); err != nil {
				t.Fatal(err)
			}
			got, err := store.DeploymentByID(ctx, candidate.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantCode == "" {
				if got.Status != state.DeployLive {
					t.Fatalf("valid image did not become live: %+v", got)
				}
			} else {
				if got.Status != state.DeployFailed || got.ErrorCode != tc.wantCode {
					t.Fatalf("unverified image = %s/%s, want failed/%s", got.Status, got.ErrorCode, tc.wantCode)
				}
				serving, err := store.LiveDeploymentForScope(ctx, app.ID, candidate.Scope)
				if err != nil || serving.ID != stable.ID || serving.TrafficPercent != 100 {
					t.Fatalf("unverified image changed serving release: %+v, %v", serving, err)
				}
				for _, call := range notifier.calls {
					if strings.Contains(call.payload, `"kind":"candidate_route"`) {
						t.Fatalf("unverified image route published: %+v", call)
					}
				}
			}
			if tc.verifier != nil && (tc.verifier.repo != "owner/repo" || tc.verifier.branch != "main" || tc.verifier.installationID != 42) {
				t.Fatalf("wrong branch lookup identity: %+v", tc.verifier)
			}
		})
	}
}
