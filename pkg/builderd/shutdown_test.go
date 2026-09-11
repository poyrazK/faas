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

type drainVM struct {
	waitStarted chan struct{}
	cancelCalls chan string
	cancelGate  chan struct{}
	startOnce   sync.Once
}

func (v *drainVM) Spawn(_ context.Context, req VMRequest) (BuildHandle, error) {
	return BuildHandle{BuildID: req.BuildID, Instance: "drain-test", TimeoutSec: 30}, nil
}

func (v *drainVM) WaitForCompletion(ctx context.Context, _ BuildHandle) (BuildOutcome, error) {
	v.startOnce.Do(func() { close(v.waitStarted) })
	<-ctx.Done()
	return BuildOutcome{}, ctx.Err()
}

func (v *drainVM) Cancel(ctx context.Context, buildID string) error {
	select {
	case v.cancelCalls <- buildID:
	default:
	}
	if v.cancelGate != nil {
		select {
		case <-v.cancelGate:
		case <-ctx.Done():
		}
	}
	return nil
}

func TestDrainCancelsActiveBuildAndRequeuesClaim(t *testing.T) {
	store := state.NewMemStore()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, source, []string{"package.json", "index.js"})
	buildID, _, _ := seedDeployment(t, store, source)

	vm := &drainVM{
		waitStarted: make(chan struct{}),
		cancelCalls: make(chan string, 1),
		cancelGate:  make(chan struct{}),
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
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	drainDone := make(chan error, 1)
	go func() { drainDone <- b.Drain(drainCtx) }()

	select {
	case got := <-vm.cancelCalls:
		if got != buildID {
			t.Fatalf("cancelled build = %q, want %q", got, buildID)
		}
	case <-time.After(time.Second):
		t.Fatal("Drain did not cancel the active VM")
	}
	cancelBuild()
	close(vm.cancelGate)
	if err := <-drainDone; err != nil {
		t.Fatalf("Drain: %v", err)
	}
	select {
	case err := <-processDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ProcessOne error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ProcessOne did not finish during drain")
	}

	build, err := store.BuildByID(context.Background(), buildID)
	if err != nil {
		t.Fatal(err)
	}
	if build.Status != state.BuildQueued {
		t.Fatalf("build status = %s, want queued after drain", build.Status)
	}
	if _, err := b.ProcessOne(context.Background(), buildID); !errors.Is(err, ErrDraining) {
		t.Fatalf("ProcessOne after Drain = %v, want ErrDraining", err)
	}
}
