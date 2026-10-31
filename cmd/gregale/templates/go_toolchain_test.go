package templates

import (
	"os"
	"regexp"
	"testing"
)

// Issue #2641: the Go starters and the function builder's Railpack pin share
// GoToolchainVersion, and it moves with the repository toolchain so a Go
// bump cannot leave customer builds on a release without security fixes.
func TestGoToolchainVersionMatchesRepositoryToolchain(t *testing.T) {
	data, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^toolchain go(\S+)$`).FindSubmatch(data)
	if match == nil {
		t.Fatal("go.mod has no toolchain directive")
	}
	if got := string(match[1]); got != GoToolchainVersion {
		t.Fatalf("GoToolchainVersion = %s, want go.mod toolchain %s", GoToolchainVersion, got)
	}
}
