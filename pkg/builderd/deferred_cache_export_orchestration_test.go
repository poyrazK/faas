package builderd

// adr: 20261029171525

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// deferredCacheTestVM is a fakeVM whose builds leave their cache export owed.
type deferredCacheTestVM struct {
	fakeVM
	exportErr    error
	exportHook   func()
	exportCalls  int
	discardCalls int
}

func (v *deferredCacheTestVM) ExportDeferredCache(context.Context, BuildHandle) error {
	v.exportCalls++
	if v.exportHook != nil {
		v.exportHook()
	}
	return v.exportErr
}

func (v *deferredCacheTestVM) DiscardDeferredCache(BuildHandle) { v.discardCalls++ }

func TestProcessOne_DeferredCacheExportRunsAfterTheHandoff(t *testing.T) {
	for _, tc := range []struct {
		name        string
		deferred    bool
		cancelled   bool
		exportErr   error
		wantExport  int
		wantDiscard int
	}{
		{name: "owed export runs after the handoff", deferred: true, wantExport: 1},
		{name: "a failed export keeps the build", deferred: true, exportErr: errors.New("cache export exited 1"), wantExport: 1},
		{name: "a build that does not publish discards the drive", deferred: true, cancelled: true, wantDiscard: 1},
		{name: "nothing owed", deferred: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			src := filepath.Join(t.TempDir(), "src.tar.gz")
			makeTarballWithName(t, src, []string{"package.json", "index.js"})
			buildID, depID, _ := seedDeployment(t, store, src)
			image := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(image, []byte("produced layer"), 0o644); err != nil {
				t.Fatal(err)
			}
			notif := &fakeNotifier{}
			vm := &deferredCacheTestVM{exportErr: tc.exportErr}
			vm.out = BuildOutcome{OCIImage: image, ExitCode: 0, DependencyCacheDeferred: tc.deferred}
			if tc.cancelled {
				vm.waitHook = func() {
					if err := store.MarkBuildCancelled(ctx, buildID, depID, true, time.Now()); err != nil {
						t.Fatalf("MarkBuildCancelled: %v", err)
					}
				}
			}
			vm.exportHook = func() {
				build, err := store.BuildByID(ctx, buildID)
				if err != nil || build.Status != state.BuildSucceeded {
					t.Fatalf("cache export ran before the build succeeded: %s, %v", build.Status, err)
				}
				handedOff := false
				for _, call := range notif.calls {
					handedOff = handedOff || call.channel == db.NotifySnapshotBoot
				}
				if !handedOff {
					t.Fatal("cache export ran before the image was handed to imaged")
				}
			}
			b := New(store, notif, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{DeferCacheExport: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))

			if _, err := b.ProcessOne(ctx, buildID); err != nil {
				t.Fatalf("ProcessOne: %v", err)
			}
			if !vm.lastRequest.DeferCacheExport {
				t.Fatal("the switch did not reach the VM request")
			}
			if vm.exportCalls != tc.wantExport || vm.discardCalls != tc.wantDiscard {
				t.Fatalf("export %d, discard %d; want %d, %d", vm.exportCalls, vm.discardCalls, tc.wantExport, tc.wantDiscard)
			}
			build, err := store.BuildByID(ctx, buildID)
			if err != nil {
				t.Fatal(err)
			}
			if want := state.BuildSucceeded; !tc.cancelled && build.Status != want {
				t.Fatalf("build status = %s, want %s", build.Status, want)
			}
		})
	}
}
