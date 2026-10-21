package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

// Outside a linked project add bucket needs --env; it used to print only its
// usage line (Snapbox SB-2).
func TestAddBucketWithoutEnvSaysWhy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	main.WithCwd(t, t.TempDir())
	stderr, restore := main.CaptureStderr(t)
	code := main.CmdAddBucket([]string{"files", "--app", "my-api"})
	restore()
	if code != 1 || !strings.Contains(stderr.String(), "--env is required outside a linked project") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}
