package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale doctor --path h14-py` answered "unknown flag
// --path" although deploy and scan take --path (Hunt 14 H14-25).
func TestDoctorTakesThePathFlag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main.ResetJSONOutput()
	t.Cleanup(main.ResetJSONOutput)
	stdout, restore := main.CaptureStdout(t)
	code := main.CmdDoctorWithImageInspector([]string{"--path", dir}, nil)
	restore()
	if code != 0 || !strings.Contains(stdout.String(), filepath.Base(dir)) {
		t.Fatalf("exit %d; doctor --path must check that directory: %s", code, stdout.String())
	}
	stderr, restoreErr := main.CaptureStderr(t)
	code = main.CmdDoctorWithImageInspector([]string{"--path", dir, t.TempDir()}, nil)
	restoreErr()
	if code != 1 || !strings.Contains(stderr.String(), "pass the directory once") {
		t.Fatalf("exit %d with two different directories: %s", code, stderr.String())
	}
}
