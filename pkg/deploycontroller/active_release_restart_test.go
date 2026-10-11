package deploycontroller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

type runnerRuntime struct {
	fakeRuntime
	running    bool
	runningErr error
	root       string
}

func (r *runnerRuntime) RunningFrom(_ context.Context, _ releasebundle.Manifest, releaseRoot string) (bool, error) {
	r.root = releaseRoot
	r.calls = append(r.calls, "running")
	return r.running, r.runningErr
}

// Production-us rc.252: the 40-minute CD cap cancelled an activation after
// the current pointer named rc.252 but before the restart. Every rerun then
// treated the exact active release as converged, and the control-plane
// daemons stayed on rc.251 behind a pointer that named rc.252.
func TestDeployRestartsExactActiveReleaseWhenDaemonsRunOlderBinaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		running    bool
		runningErr error
		wantCalls  []string
		wantErr    string
	}{
		{"daemons_on_release", true, nil, []string{"running"}, ""},
		{"daemons_on_older_release", false, nil, []string{"running", "restart", "healthy"}, ""},
		{"inspection_fails", false, errors.New("systemctl unavailable"), []string{"running"}, "inspect running daemons"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			release := makeRelease(t, root, "rc252")
			current := filepath.Join(root, "active")
			if err := os.Symlink(release, current); err != nil {
				t.Fatal(err)
			}
			runtime := &runnerRuntime{running: tc.running, runningErr: tc.runningErr}
			controller := newController(t, root, current, runtime)

			err := controller.Deploy(context.Background(), "rc252")
			if tc.wantErr == "" && err != nil {
				t.Fatalf("Deploy: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Deploy = %v; want %q", err, tc.wantErr)
			}
			if !slices.Equal(runtime.calls, tc.wantCalls) {
				t.Fatalf("runtime calls = %v; want %v", runtime.calls, tc.wantCalls)
			}
			if runtime.root != release {
				t.Fatalf("RunningFrom root = %q; want %q", runtime.root, release)
			}
			if got, _ := os.Readlink(current); got != release {
				t.Fatalf("current = %q; want %q unchanged", got, release)
			}
		})
	}
}
