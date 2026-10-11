package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/releasebundle"
)

// RunningFrom backs the deploy controller's check that an exact active
// release actually runs: production-us rc.252 kept control-plane daemons on
// rc.251 after a cancelled activation published the current pointer.
func TestHostRuntimeRunningFromReadsEachDaemonsBinary(t *testing.T) {
	order, err := daemonunitspec.RestartOrder()
	if err != nil {
		t.Fatal(err)
	}
	manifest := releasebundle.Manifest{}
	for _, service := range []string{"apid", "schedd"} {
		manifest.Files = append(manifest.Files, releasebundle.File{Path: "systemd/faas-" + service + ".service"})
	}
	release := "/opt/faas/releases/new"
	for _, tc := range []struct {
		name string
		pids map[string]string
		exes map[string]string
		want bool
	}{
		{"all_on_release", map[string]string{"apid": "10", "schedd": "11"}, map[string]string{"10": release + "/bin/apid", "11": release + "/bin/schedd"}, true},
		{"one_on_older_release", map[string]string{"apid": "10", "schedd": "11"}, map[string]string{"10": release + "/bin/apid", "11": "/opt/faas/releases/old/bin/schedd"}, false},
		{"one_not_running", map[string]string{"apid": "10", "schedd": "0"}, map[string]string{"10": release + "/bin/apid"}, false},
		{"prefix_is_not_a_sibling_release", map[string]string{"apid": "10", "schedd": "11"}, map[string]string{"10": release + "/bin/apid", "11": release + "-hotfix/bin/schedd"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proc := t.TempDir()
			for pid, exe := range tc.exes {
				if err := os.MkdirAll(filepath.Join(proc, pid), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(exe, filepath.Join(proc, pid, "exe")); err != nil {
					t.Fatal(err)
				}
			}
			origOutput, origProc := commandOutput, procRoot
			t.Cleanup(func() { commandOutput, procRoot = origOutput, origProc })
			procRoot = proc
			commandOutput = func(_ context.Context, _ string, args ...string) (string, error) {
				unit := strings.TrimSuffix(strings.TrimPrefix(args[1], "faas-"), ".service")
				return tc.pids[unit] + "\n", nil
			}
			got, err := hostRuntime{serviceOrder: order}.RunningFrom(context.Background(), manifest, release)
			if err != nil || got != tc.want {
				t.Fatalf("RunningFrom = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
