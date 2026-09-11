package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type waitContextCancelVM struct {
	waitStarted chan struct{}
	cancelCalls chan string
	startOnce   sync.Once
}

func (v *waitContextCancelVM) Spawn(_ context.Context, req VMRequest) (BuildHandle, error) {
	return BuildHandle{BuildID: req.BuildID, Instance: "wait-cancel-test", TimeoutSec: 30}, nil
}

func (v *waitContextCancelVM) WaitForCompletion(ctx context.Context, _ BuildHandle) (BuildOutcome, error) {
	v.startOnce.Do(func() { close(v.waitStarted) })
	<-ctx.Done()
	return BuildOutcome{}, ctx.Err()
}

func (v *waitContextCancelVM) Cancel(_ context.Context, buildID string) error {
	select {
	case v.cancelCalls <- buildID:
	default:
	}
	return nil
}

func TestProcessOne_CancelledWaitStopsVMAndRequeuesClaim(t *testing.T) {
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	buildID, _, _ := seedDeployment(t, store, source)

	vm := &waitContextCancelVM{
		waitStarted: make(chan struct{}),
		cancelCalls: make(chan string, 1),
	}
	b := New(store, nil, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	buildCtx, cancelBuild := context.WithCancel(context.Background())
	processDone := make(chan error, 1)
	go func() {
		_, err := b.ProcessOne(buildCtx, buildID)
		processDone <- err
	}()

	select {
	case <-vm.waitStarted:
	case <-time.After(time.Second):
		t.Fatal("build did not reach VM wait")
	}
	cancelBuild()

	select {
	case err := <-processDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ProcessOne error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ProcessOne did not finish after wait context cancellation")
	}

	select {
	case got := <-vm.cancelCalls:
		if got != buildID {
			t.Fatalf("cancelled build = %q, want %q", got, buildID)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled wait did not explicitly stop the VM")
	}

	build, err := store.BuildByID(context.Background(), buildID)
	if err != nil {
		t.Fatal(err)
	}
	if build.Status != state.BuildQueued {
		t.Fatalf("build status = %s, want queued after cancellation", build.Status)
	}
}
