package builderd

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProcessOne_UpstreamFetchFailureIsInfra(t *testing.T) {
	tests := []struct {
		name      string
		log       string
		wantClass state.FailureClass
		wantCode  string
		wantError string
	}{
		{
			name: "mise bootstrap 504 (#6666)",
			log: "[build]\nERRO failed to ensure mise is installed: failed to download and install: temporary failure fetching " +
				"https://github.com/jdx/mise/releases/download/v2026.8.13/mise-v2026.8.13-linux-x64-musl.tar.gz: unexpected status 504 Gateway Timeout\n",
			wantClass: state.FailureInfra,
			wantCode:  api.CodeBuildInfrastructure,
			wantError: "build exited 75: a download from github.com/jdx/mise failed upstream",
		},
		{
			name:      "the customer's code fails to compile",
			log:       "[build]\n./main.go:3:2: undefined: foo\n",
			wantClass: state.FailureUserError,
			wantCode:  api.CodeBuildFailed,
			wantError: "build exited 75",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			src := filepath.Join(t.TempDir(), "src.tar.gz")
			makeTarballWithName(t, src, []string{"package.json"})
			buildID, depID, _ := seedDeployment(t, store, src)
			fvm := &fakeVM{out: BuildOutcome{OCIImage: "/dev/null", ExitCode: 75, FailureClass: "FailureUserError", LogTail: tc.log}}
			b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

			if _, err := b.ProcessOne(context.Background(), buildID); err == nil {
				t.Fatal("expected error on non-zero VM exit")
			}
			build, _ := store.BuildByID(context.Background(), buildID)
			if build.FailureClass != tc.wantClass {
				t.Errorf("failure_class = %s, want %s", build.FailureClass, tc.wantClass)
			}
			dep, _ := store.DeploymentByID(context.Background(), depID)
			if dep.ErrorCode != tc.wantCode {
				t.Errorf("deployment error_code = %q, want %q", dep.ErrorCode, tc.wantCode)
			}
			if !strings.HasPrefix(dep.Error, tc.wantError) {
				t.Errorf("deployment error = %q, want prefix %q", dep.Error, tc.wantError)
			}
		})
	}
}
