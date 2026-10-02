package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/vmmdmount"
)

type runtimeSourceLookupStore struct {
	state.Store
	instance state.Instance
	err      error
	gotID    string
}

func (s *runtimeSourceLookupStore) InstanceByID(_ context.Context, id string) (state.Instance, error) {
	s.gotID = id
	return s.instance, s.err
}

func TestRuntimeSourceLivenessUsesDurableStateAndRefusesUnknowns(t *testing.T) {
	for _, lifecycle := range state.States {
		t.Run(string(lifecycle), func(t *testing.T) {
			s := &runtimeSourceLookupStore{instance: state.Instance{State: string(lifecycle)}}
			live, err := vmmdRuntimeSourceLiveness(s)(t.Context(), "the-instance")
			if err != nil || live != lifecycle.CountsForRAM() || s.gotID != "the-instance" {
				t.Fatalf("durable state=%s live=%v err=%v id=%q", lifecycle, live, err, s.gotID)
			}
		})
	}
	for _, test := range []struct {
		name string
		s    runtimeSourceLookupStore
		want error
	}{
		{"gone", runtimeSourceLookupStore{err: state.ErrNotFound}, nil},
		{"lookup failure", runtimeSourceLookupStore{err: context.DeadlineExceeded}, context.DeadlineExceeded},
		{"unknown state", runtimeSourceLookupStore{instance: state.Instance{State: "future_state"}}, errors.New("unknown")},
	} {
		t.Run(test.name, func(t *testing.T) {
			live, err := vmmdRuntimeSourceLiveness(&test.s)(t.Context(), "the-instance")
			if live || (err == nil) != (test.want == nil) {
				t.Fatalf("liveness=%v err=%v", live, err)
			}
			if test.s.err != nil && test.want != nil && !errors.Is(err, test.want) {
				t.Fatal("lookup error was hidden", err)
			}
		})
	}
}

func TestRuntimeSourceRootPrefersNodeLocalCacheAndNoStoreSkipsReap(t *testing.T) {
	local, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := t.TempDir()
	cache, err := storage.NewLocalCacheBackend(local, cacheRoot, 1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_STORAGE_ROOT", t.TempDir())
	if got := vmmdRuntimeSourceRoot(cache); got != filepath.Join(cacheRoot, ".vmmd-runtime-sources") {
		t.Fatalf("sealed bytes escaped node-local cache: %q", got)
	}
	if sweep := vmmdRuntimeSourceSweep(nil, vmmdRuntimeSourceRoot(local), slog.Default()); sweep != nil {
		t.Fatal("nil durable view enabled cleanup")
	}
}

func TestRuntimeSourcePeriodicSweepStopsWithDaemon(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tick, done := make(chan struct{}, 1), make(chan struct{})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		defer close(done)
		runParentMountSweep(ctx, vmmdmount.NewRegistry(1), 5*time.Millisecond, log, nil, func(context.Context) {
			select {
			case tick <- struct{}{}:
			default:
			}
		})
	}()
	select {
	case <-tick:
	case <-time.After(time.Second):
		t.Fatal("periodic runtime source cleanup never ran")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runtime source sweep survived daemon cancellation")
	}
}
