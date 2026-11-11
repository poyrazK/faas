package apidsource

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
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

// copyingSourceBackend is a retained-source backend with a native copy.
type copyingSourceBackend struct {
	retainedSourceBackend
	copyErr error
	copies  []string
}

func (b *copyingSourceBackend) CopyObject(_ context.Context, src, dst string) error {
	if b.copyErr != nil {
		return b.copyErr
	}
	b.copies = append(b.copies, src+"->"+dst)
	return nil
}

// ChronoLake report: `release retry` timed out in the CLI because the retry
// streamed the original archive through apid. A backend with a native copy
// republishes it server side; one without a copy, or missing the retained
// object, still streams.
func TestPublishRetrySourceCopiesServerSide(t *testing.T) {
	params := EnqueueParams{SourceBuildID: "previous-build", SourcePath: filepath.Join(t.TempDir(), "gone.tar.gz"), SourceBytes: 16}
	copying := &copyingSourceBackend{retainedSourceBackend: retainedSourceBackend{newMemBackend()}}
	if err := publishRetrySource(t.Context(), copying, "new-build", params); err != nil {
		t.Fatal(err)
	}
	if len(copying.copies) != 1 || copying.copies[0] != "sources/previous-build.tar.gz->sources/new-build.tar.gz" || len(copying.puts) != 0 {
		t.Fatalf("copies=%v puts=%d; want one server-side copy and no upload", copying.copies, len(copying.puts))
	}
	for _, copyErr := range []error{errors.ErrUnsupported, storage.ErrNotFound} {
		fallback := &copyingSourceBackend{retainedSourceBackend: retainedSourceBackend{newMemBackend()}, copyErr: copyErr}
		if err := publishRetrySource(t.Context(), fallback, "new-build", params); err != nil {
			t.Fatalf("%v: %v", copyErr, err)
		}
		if string(fallback.puts["sources/new-build.tar.gz"]) != "original archive" {
			t.Fatalf("%v: archive was not streamed", copyErr)
		}
	}
	failing := &copyingSourceBackend{retainedSourceBackend: retainedSourceBackend{newMemBackend()}, copyErr: errors.New("permission denied")}
	if err := publishRetrySource(t.Context(), failing, "new-build", params); err == nil {
		t.Fatal("a failed copy was swallowed")
	}
}
