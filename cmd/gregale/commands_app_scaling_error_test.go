package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 32 H32-60: a zero --expected-app-id was reported as a missing flag.
func TestCmdAppScalingNamesTheInvalidFlag(t *testing.T) {
	main.ResetJSONOut(t)
	oldErr := *main.OsStderr
	var stderr bytes.Buffer
	*main.OsStderr = &stderr
	t.Cleanup(func() { *main.OsStderr = oldErr })
	code := main.CmdAppScaling("demo", []string{
		"--expected-app-id", "00000000-0000-0000-0000-000000000000",
		"--request-id", "6b1f1b9e-3c55-4b0a-9a55-6d1c1c7f5f01", "--expected-revision", "3", "--ram", "512",
	})
	if code == 0 || !strings.Contains(stderr.String(), "--expected-app-id must be the reviewed app UUID") {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}
