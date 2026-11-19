package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/buildprogress"
	"github.com/onebox-faas/faas/pkg/state"
)

// consoleVM is a fakeVM whose builder prints a BuildKit console.
type consoleVM struct {
	*fakeVM
	console  []string
	err      error
	instance string
}

func (c *consoleVM) FollowBuildConsole(_ context.Context, h BuildHandle, line func(string)) error {
	c.instance = h.Instance
	for _, l := range c.console {
		line(l)
	}
	return c.err
}

func TestProcessOne_WritesLiveBuildSteps(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "steps reach the deployment log",
			want: []string{
				buildprogress.StepLogPrefix + "[build 1/3] FROM docker.io/library/node:22",
				buildprogress.StepLogPrefix + "[build 2/3] RUN npm ci",
				buildprogress.StepLogPrefix + "exporting to oci image format",
			},
		},
		{name: "a refused stream leaves the build alone", err: errors.New("permission denied")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			srcTar := filepath.Join(t.TempDir(), "src.tar.gz")
			makeTarballWithName(t, srcTar, []string{"package.json", "index.js"})
			buildID, depID, _ := seedDeployment(t, store, srcTar)
			out := filepath.Join(t.TempDir(), "produced.ext4")
			if err := os.WriteFile(out, []byte("produced layer"), 0o644); err != nil {
				t.Fatal(err)
			}
			vm := &consoleVM{fakeVM: &fakeVM{out: BuildOutcome{OCIImage: out}}, err: tc.err}
			if tc.err == nil {
				vm.console = []string{
					"guest-init: build-command",
					"#1 [internal] load build definition from Dockerfile",
					"#2 [build 1/3] FROM docker.io/library/node:22",
					"#2 DONE 3.0s",
					"#3 [build 2/3] RUN npm ci",
					"#3 4.120 added 312 packages",
					"#2 [build 1/3] FROM docker.io/library/node:22",
					"#4 exporting to oci image format",
				}
			}
			b := New(store, &fakeNotifier{}, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
				t.Fatalf("ProcessOne: %v", err)
			}
			if vm.instance != "build-test" {
				t.Fatalf("followed instance %q, want the build's", vm.instance)
			}
			entries, _, err := store.ListDeploymentLogs(context.Background(), depID, 0, 500)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for i := len(entries) - 1; i >= 0; i-- {
				if strings.HasPrefix(entries[i].Line, buildprogress.StepLogPrefix) {
					got = append(got, strings.TrimSuffix(entries[i].Line, "\n"))
				}
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("step lines =\n%q\nwant\n%q", got, tc.want)
			}
			row, err := store.BuildByID(context.Background(), buildID)
			if err != nil || row.Status != state.BuildSucceeded {
				t.Fatalf("build = %+v, %v; want succeeded", row.Status, err)
			}
		})
	}
}
