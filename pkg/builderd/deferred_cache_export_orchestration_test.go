package builderd

// adr: 20261029171525

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// deferredCacheTestVM is a fakeVM whose builds leave their cache export owed.
type deferredCacheTestVM struct {
	fakeVM
	exportErr    error
	exportOut    BuildOutcome
	exportHook   func()
	exportCalls  int
	discardCalls int
}

func (v *deferredCacheTestVM) ExportDeferredCache(context.Context, BuildHandle) (BuildOutcome, error) {
	v.exportCalls++
	if v.exportHook != nil {
		v.exportHook()
	}
	return v.exportOut, v.exportErr
}

func (v *deferredCacheTestVM) DiscardDeferredCache(BuildHandle) { v.discardCalls++ }

func TestProcessOne_DeferredCacheExportRunsAfterTheHandoff(t *testing.T) {
	for _, tc := range []struct {
		name        string
		deferred    bool
		cancelled   bool
		exportErr   error
		exportTail  string
		wantExport  int
		wantDiscard int
		wantLog     []string
	}{
		{name: "owed export runs after the handoff", deferred: true, wantExport: 1,
			wantLog: []string{"dependency cache will be saved after the image is handed off", "dependency cache saved for the next build"}},
		{name: "a failed export keeps the build and logs its output", deferred: true, exportErr: errors.New("cache export exited 1"),
			exportTail: "[build]\n+ test -f /build/railpack-plan.json\n", wantExport: 1,
			wantLog: []string{"dependency cache could not be saved", "[dependency cache export output]", "+ test -f /build/railpack-plan.json"}},
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
			vm := &deferredCacheTestVM{exportErr: tc.exportErr, exportOut: BuildOutcome{LogTail: tc.exportTail}}
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
			rows, _, err := store.ListDeploymentLogs(ctx, depID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			var log strings.Builder
			for _, row := range rows {
				log.WriteString(row.Line)
			}
			for _, want := range tc.wantLog {
				if !strings.Contains(log.String(), want) {
					t.Fatalf("build log missing %q:\n%s", want, log.String())
				}
			}
		})
	}
}
