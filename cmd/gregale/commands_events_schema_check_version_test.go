package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Hunt 32 H32-60: `event schema check SOURCE TYPE` without --version failed
// on the internal schema_versions field instead of naming the flag.
func TestCmdEventSchemaCheckNeedsVersion(t *testing.T) {
	main.ResetJSONOut(t)
	oldErr := *main.OsStderr
	var stderr bytes.Buffer
	*main.OsStderr = &stderr
	t.Cleanup(func() { *main.OsStderr = oldErr })
	if code := main.CmdEventsSchemaRollout([]string{"h32", "h32.test"}); code == 0 {
		t.Fatal("check without --version succeeded")
	}
	if got := stderr.String(); !strings.Contains(got, "--version is required") || strings.Contains(got, "schema_versions") {
		t.Fatalf("stderr = %q", got)
	}
}
