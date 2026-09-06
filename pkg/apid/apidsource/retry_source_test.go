package apidsource

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type retainedSourceBackend struct{ *memBackend }

func (b retainedSourceBackend) Get(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("original archive")), nil
}

func TestRetrySource_DurableQueueAndRetainedArchive(t *testing.T) {
	for _, scenario := range []string{"success", "upload failure", "queue failure", "notification failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := t.Context()
			st := state.NewMemStore()
			app := mustSeedApp(t, st)
			dep, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: filepath.Join(t.TempDir(), "removed-source.tar.gz"), SourceBytes: 16})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.FailSourceDeployment(ctx, dep.ID, "original failure"); err != nil {
				t.Fatal(err)
			}
			backend := retainedSourceBackend{newMemBackend()}
			store := &errStore{Store: st}
			var notifier Notifier = &recordingNotifier{}
			if scenario == "upload failure" {
				backend.putErr = errors.New("registry offline")
			}
			if scenario == "queue failure" {
				store.createBuildErr = errors.New("queue offline")
			}
			if scenario == "notification failure" {
				notifier = errNotifier{errors.New("notify offline")}
			}
			result, err := enqueueWithSourceStorage(ctx, store, notifier, EnqueueParams{RetryOf: dep.ID, RetryFrom: state.StageSecurityScan, SourceBuildID: "previous-build", AppID: app.ID, Kind: dep.Kind, SourcePath: dep.SourcePath, SourceBytes: dep.SourceBytes, LogSpool: t.TempDir(), Log: quietLogger()}, backend)
			if scenario == "upload failure" || scenario == "queue failure" {
				if err == nil {
					t.Fatal("expected enqueue error")
				}
				if _, err := st.ClaimNextQueuedBuild(ctx); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("failed enqueue is claimable: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				build, err := st.ClaimNextQueuedBuild(ctx)
				if err != nil || build.ID != result.BuildID {
					t.Fatalf("durable claim: %+v %v", build, err)
				}
				if string(backend.puts["sources/"+build.ID+".tar.gz"]) != "original archive" {
					t.Fatal("original source was not republished for the new build")
				}
			}
			original, err := st.DeploymentByID(ctx, dep.ID)
			if err != nil || original.Status != state.DeployFailed {
				t.Fatalf("original deployment changed: %+v %v", original, err)
			}
		})
	}
}
