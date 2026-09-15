package builderd

import (
	"os"
	"path/filepath"
	"testing"
)

// spec: a parent builder-cgroup OOM is attributed to the sole admitted build
// even when no guest completion manifest survives.
func TestBuilderSliceOOMOutcome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.events")
	if err := os.WriteFile(path, []byte("low 0\nhigh 2\nmax 4\noom 3\noom_kill 8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := builderSliceOOMOutcome(BuildHandle{
		BuildID:                     "build-1",
		Instance:                    "build-build-1",
		ExportDir:                   "/tmp/build-1",
		BuilderSliceOOMKillsAtStart: 7,
		BuilderSliceOOMCounterValid: true,
	}, path)
	if !ok || out.ExitCode != 137 || out.FailureClass != "FailureOOM" || out.FailureCode != "build_oom" {
		t.Fatalf("outcome = %+v, attributed=%v", out, ok)
	}
	if out.BuilderSliceOOMKills != 1 {
		t.Fatalf("oom delta = %d, want 1", out.BuilderSliceOOMKills)
	}
}

func TestBuilderSliceOOMOutcomeIgnoresOldCounter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.events")
	if err := os.WriteFile(path, []byte("oom_kill 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, ok := builderSliceOOMOutcome(BuildHandle{
		BuilderSliceOOMKillsAtStart: 7,
		BuilderSliceOOMCounterValid: true,
	}, path); ok {
		t.Fatalf("old OOM was re-attributed: %+v", out)
	}
}
