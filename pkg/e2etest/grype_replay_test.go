package e2etest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeGrype counts its directory scans in state/count and fails a scan while
// state/fail exists. Like grype, every run rewrites the database directory's
// last_update_check.
const fakeGrype = `#!/bin/sh
state='@STATE@'
date +%s%N >"$GRYPE_DB_CACHE_DIR/6/last_update_check"
case "$1" in
version) echo "fake grype 1.0"; exit 0 ;;
esac
n=$(($(cat "$state/count" 2>/dev/null || echo 0) + 1))
echo "$n" >"$state/count"
if [ -e "$state/fail" ]; then echo '{"partial"'; exit 3; fi
printf '{"run":%s}\n' "$n"
`

func TestGrypeReplayReplaysOnlyByteIdenticalScans(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the replay wrapper relies on GNU find")
	}
	root, state, db := t.TempDir(), t.TempDir(), t.TempDir()
	real := filepath.Join(state, "grype")
	writeTestFile(t, real, strings.ReplaceAll(fakeGrype, "@STATE@", state), 0o700)
	writeTestFile(t, filepath.Join(db, "6", "vulnerability.db"), "db v1\n", 0o600)
	writeTestFile(t, filepath.Join(db, "6", "import.json"), `{"digest":"v1"}`, 0o600)
	t.Setenv("FAAS_GRYPE_BIN", real)
	t.Setenv("GRYPE_DB_CACHE_DIR", db)
	bin, err := writeGrypeReplay(root)
	if err != nil || bin == "" {
		t.Fatalf("writeGrypeReplay = %q, %v", bin, err)
	}
	scan := func(dir string) (string, int) {
		t.Helper()
		cmd := exec.Command(bin, "dir:"+dir, "-o", "json")
		out, err := cmd.Output()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out)), code
	}
	tree := func(content string) string {
		t.Helper()
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "usr", "lib", "os-release"), content, 0o644)
		writeTestFile(t, filepath.Join(dir, "etc", "passwd"), "root:x:0:0\n", 0o644)
		return dir
	}

	base := tree("ID=alpine\n")
	steps := []struct {
		name, dir, want string
		code            int
		before          func()
	}{
		{name: "first scan runs grype", dir: base, want: `{"run":1}`},
		{name: "a copy of the same bytes replays", dir: tree("ID=alpine\n"), want: `{"run":1}`},
		{name: "changed content runs grype", dir: tree("ID=debian\n"), want: `{"run":2}`},
		{name: "an updated database runs grype", dir: tree("ID=alpine\n"), want: `{"run":3}`, before: func() {
			writeTestFile(t, filepath.Join(db, "6", "vulnerability.db"), "db v2, rebuilt\n", 0o600)
		}},
		{name: "a failed scan is returned", dir: tree("ID=busybox\n"), want: `{"partial"`, code: 3, before: func() {
			writeTestFile(t, filepath.Join(state, "fail"), "", 0o600)
		}},
		{name: "a failed scan is not kept", dir: tree("ID=busybox\n"), want: `{"run":5}`, before: func() {
			if err := os.Remove(filepath.Join(state, "fail")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, step := range steps {
		if step.before != nil {
			step.before()
		}
		if got, code := scan(step.dir); got != step.want || code != step.code {
			t.Fatalf("%s: output %q exit %d, want %q exit %d", step.name, got, code, step.want, step.code)
		}
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "fake grype 1.0" {
		t.Fatalf("version passes through: %q, %v", out, err)
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
