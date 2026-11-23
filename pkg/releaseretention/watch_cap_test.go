package releaseretention

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestUnwatchedReleasesLeaveTheKnownGoodRetained: a host holds at most
// watch.max_unwatched unit releases not yet watched clean
// (scripts/ci/release_units.json), and a watch breach rolls it back to the
// release before them. That release, and one spare for a converge rerun,
// must survive pruning: max_unwatched < DefaultKeepPrevious.
func TestUnwatchedReleasesLeaveTheKnownGoodRetained(t *testing.T) {
	// go test runs in the package directory.
	body, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci", "release_units.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Watch struct {
			MaxUnwatched int `json:"max_unwatched"`
		} `json:"watch"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	if config.Watch.MaxUnwatched < 1 {
		t.Fatalf("watch.max_unwatched = %d, want at least 1", config.Watch.MaxUnwatched)
	}
	if config.Watch.MaxUnwatched >= DefaultKeepPrevious {
		t.Fatalf("watch.max_unwatched = %d, but release retention keeps only %d previous releases: "+
			"a breach could find its known-good release pruned", config.Watch.MaxUnwatched, DefaultKeepPrevious)
	}
}
