package main_test

import (
	"strings"
	"testing"
)

// prod hunt 2026-11-21: `gregale bucket create NAME` was an unknown
// subcommand whose list named every verb but the command that creates a
// bucket, `gregale add bucket`.
func TestBucketCreatePointsToAddBucket(t *testing.T) {
	for _, verb := range []string{"create", "add", "new"} {
		code, _, stderr := runCLI(t, "bucket", verb, "assets", "--app", "web")
		if code == 0 || !strings.Contains(stderr, "gregale add bucket NAME --app APP") {
			t.Errorf("bucket %s = %d, stderr %q; want the add bucket command", verb, code, stderr)
		}
	}
}
