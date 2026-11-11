//go:build metal && !linux

package e2e_test

import (
	"os"
	"testing"
)

// removeDbNodeRun removes the run directory; only Linux mounts jail binds.
func removeDbNodeRun(_ *testing.T, run string) { _ = os.RemoveAll(run) }
