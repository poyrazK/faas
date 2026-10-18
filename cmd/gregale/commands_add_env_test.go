package main

import (
	"strings"
	"testing"
)

// Outside a linked project add bucket needs --env; it used to print only its
// usage line (Snapbox SB-2).
func TestAddBucketWithoutEnvSaysWhy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	withCwd(t, t.TempDir())
	stderr, restore := captureStderr(t)
	code := cmdAddBucket([]string{"files", "--app", "my-api"})
	restore()
	if code != 1 || !strings.Contains(stderr.String(), "--env is required outside a linked project") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}
