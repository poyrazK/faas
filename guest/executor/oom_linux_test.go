package executor

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

func TestExecutionOOMNeedsKernelCounterAndKilledProcess(t *testing.T) {
	// The helper drops to the real guest uid. Give it a traversable private
	// parent even when the operator's TMPDIR is root-only.
	root, err := os.MkdirTemp("/tmp", "gregale-oom-unit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(root, 0o711); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name, command string
		before, after uint64
		valid         bool
		want          api.ExecutionStatus
	}{
		{"kernel OOM", "kill -KILL $$", 2, 3, true, api.ExecutionStatusOutOfMemory},
		{"ordinary SIGKILL", "kill -KILL $$", 2, 2, true, api.ExecutionStatusFailed},
		{"counter regressed", "kill -KILL $$", 3, 2, true, api.ExecutionStatusFailed},
		{"counter unavailable", "kill -KILL $$", 0, 1, false, api.ExecutionStatusFailed},
		{"ordinary exit", "exit 7", 2, 3, true, api.ExecutionStatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			build := e.build
			var process *exec.Cmd
			e.build = func(ctx context.Context, command string, args ...string) *exec.Cmd {
				process = build(ctx, command, args...)
				return process
			}
			e.resolve = func(executionproto.Request) (string, []string, string, error) {
				return "/bin/sh", []string{"-c", tc.command}, nodeSourceName, nil
			}
			calls := 0
			e.readOOM = func() (uint64, bool) {
				calls++
				if calls == 1 {
					return tc.before, tc.valid
				}
				return tc.after, tc.valid
			}
			result, err := e.Handle(t.Context(), executionproto.Request{Version: executionproto.Version, ExecutionID: "oom-proof", Runtime: api.ExecutionRuntimeNode24, Source: "unused", Input: json.RawMessage("null"), TimeoutMS: 3000, MaxOutput: 4096, NetworkMode: api.ExecutionNetworkNone}, nil, nil)
			if process == nil || process.ProcessState == nil {
				t.Fatal("helper never executed; setup failure cannot qualify OOM attribution")
			}
			if err != nil || result.Status != tc.want {
				t.Fatalf("status=%s error=%v, want %s", result.Status, err, tc.want)
			}
			if tc.want == api.ExecutionStatusOutOfMemory && result.FailureCode != "out_of_memory" {
				t.Fatalf("failure=%s", result.FailureCode)
			}
		})
	}
}
