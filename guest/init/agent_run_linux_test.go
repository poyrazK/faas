//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
	"testing/fstest"
)

func TestDecideMode_AgentMarker(t *testing.T) {
	marker := &fstest.MapFile{Data: []byte(`{"kind":"agent","version":1}`)}
	mode, _, err := decideMode(fstest.MapFS{"etc/faas/agent.json": marker})
	if err != nil || mode != modeAgent {
		t.Fatalf("mode %v, %v; want modeAgent", mode, err)
	}
	// The agent mode never shares a guest with another workload marker.
	for _, other := range []string{"etc/faas/job.json", "etc/faas/execution.json", "etc/faas/build.json"} {
		fsys := fstest.MapFS{"etc/faas/agent.json": marker, other: &fstest.MapFile{Data: []byte(`{"kind":"job"}`)}}
		if _, _, err := decideMode(fsys); err == nil {
			t.Errorf("agent marker with %s accepted", other)
		}
	}
	if _, _, err := decideMode(fstest.MapFS{"etc/faas/agent.json": &fstest.MapFile{Data: []byte(`{"kind":"agent","version":9}`)}}); err == nil {
		t.Fatal("invalid agent marker accepted")
	}
}

// A fast command's output is complete once finish returns, even when the
// caller reaps it with Process.Wait as guest-init does as PID 1.
func TestAgentRunStdioDrainsBeforeFinish(t *testing.T) {
	for i := 0; i < 20; i++ {
		var stdout, stderr bytes.Buffer
		stdio, err := newAgentRunStdio(&stdout, &stderr, os.Getuid())
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", "echo plain; echo warn >&2")
		if err := stdio.attach(cmd); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stdio.started()
		if _, err := cmd.Process.Wait(); err != nil {
			t.Fatal(err)
		}
		stdio.finish()
		if stdout.String() != "plain\n" || stderr.String() != "warn\n" {
			t.Fatalf("run %d: stdout %q stderr %q", i, stdout.String(), stderr.String())
		}
	}
}
