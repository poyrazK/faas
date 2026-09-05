package apidsource

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"testing"
)

type inspectingSourceBackend struct {
	*memBackend
	beforePut func(string)
}

func (b inspectingSourceBackend) Put(ctx context.Context, key string, r io.Reader) error {
	b.beforePut(key)
	return b.memBackend.Put(ctx, key, r)
}
func TestEnqueuePublishesSourceBeforeDurableWork(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "upload-failure"}[fail], func(t *testing.T) {
			ctx := context.Background()
			s := state.NewMemStore()
			app := mustSeedApp(t, s)
			path, n := stageSource(t, t.TempDir())
			notif := &recordingNotifier{}
			be := newMemBackend()
			if fail {
				be.putErr = errors.New("registry unavailable")
			}
			var key string
			backend := inspectingSourceBackend{be, func(k string) {
				key = k
				if _, err := s.LatestDeployment(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("deployment visible during upload: %v", err)
				}
				if _, err := s.ClaimNextQueuedBuild(ctx); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("build visible during upload: %v", err)
				}
			}}
			result, err := enqueueWithSourceStorage(ctx, s, notif, EnqueueParams{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: path, SourceBytes: n, LogSpool: t.TempDir(), Log: quietLogger()}, backend)
			if fail {
				if err == nil {
					t.Fatal("expected upload failure")
				}
				if _, err := s.LatestDeployment(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("orphan deployment: %v", err)
				}
				if notif.callCount() != 0 {
					t.Fatal("notified failed upload")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if key != "sources/"+result.BuildID+".tar.gz" {
				t.Fatalf("source ID mismatch %s", key)
			}
			build, err := s.ClaimNextQueuedBuild(ctx)
			if err != nil || build.ID != result.BuildID {
				t.Fatalf("claim: %+v %v", build, err)
			}
		})
	}
}
