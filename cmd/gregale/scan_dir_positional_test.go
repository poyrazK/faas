package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestScanTakesADirectoryPositionally: `scan ./my-app` answered only
// "unexpected positional argument" on production-us rc.294 while `doctor
// ./my-app` takes the directory (Hunt 14 H14-64). A word that is no
// directory is still refused.
func TestScanTakesADirectoryPositionally(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:9")
	t.Setenv("FAAS_TOKEN", "test-token")
	dir := t.TempDir()
	main.ResetJSONOut(t)
	_, stderr, restore := main.SwapIO(t)
	main.Run([]string{"scan", dir})
	restore()
	if strings.Contains(stderr(), "unexpected positional argument") {
		t.Fatalf("directory positional refused:\n%s", stderr())
	}
	_, stderr, restore = main.SwapIO(t)
	code := main.Run([]string{"scan", "not-a-directory-here"})
	restore()
	if code == 0 || !strings.Contains(stderr(), "unexpected positional argument(s): not-a-directory-here") {
		t.Fatalf("exit=%d stderr=%q", code, stderr())
	}
}
