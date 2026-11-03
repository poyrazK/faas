package apidserver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"testing"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A GitHub push deploys the workflows its gregale.yaml declares, as a CLI
// deploy of the same source does; a PR preview does not, so a preview never
// runs scheduled workflows.
func TestEnqueueBuild_PushCarriesManifestWorkflowsPreviewDoesNot(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind githubdpb.EnqueueBuildEventKind
		want bool
	}{
		{"push", githubdpb.EnqueueBuildEventKind_EVENT_KIND_PUSH, true},
		{"pull request preview", githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &bridgeStubStore{
				app:     state.App{ID: "app-1", AccountID: "acct-1", Status: state.AppActive},
				account: state.Account{ID: "acct-1", Plan: api.PlanPro},
			}
			bridge := newBridge(t, store, &bridgeStubNotifier{})
			path, _ := stageFixtureFile(t, bridge.stagingRoot, "acct-1/app-1/abc123", []byte("x"))
			size := writeBridgeSourceArchive(t, path, map[string]string{
				"index.js":     "x",
				"gregale.yaml": "workflows:\n  - name: process_order\n    steps:\n      - name: charge\n        run: charge_order\n",
			})
			req := &githubdpb.EnqueueBuildRequest{
				AccountId: "acct-1", AppId: "app-1", CommitSha: "abc123", SourcePath: path,
				SourceUrl: "https://codeload.example.com/repo/tar.gz/abc123", SourceBytes: size,
				Branch: "main", Pusher: "octocat", EventKind: tc.kind,
			}
			if tc.kind == githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST {
				req.PullRequestNumber = 7
			}
			if _, err := bridge.EnqueueBuild(t.Context(), req); err != nil {
				t.Fatalf("EnqueueBuild: %v", err)
			}
			if got := bytes.Contains(store.createDeploymentReturned.Workflows, []byte("process_order")); got != tc.want {
				t.Fatalf("deployment workflows = %s, want present=%v", store.createDeploymentReturned.Workflows, tc.want)
			}
		})
	}
}

// writeBridgeSourceArchive replaces a staged fixture with a gzip tarball of
// files and returns its size.
func writeBridgeSourceArchive(t *testing.T, path string, files map[string]string) int64 {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return int64(archive.Len())
}
