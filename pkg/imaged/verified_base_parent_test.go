package imaged

// adr: 20261028090000 — a child base finds a parent staged from a tag ref,
// whose producer records repository@digest.

import (
	"strings"
	"testing"
)

func TestParentSourceMatches(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	other := "sha256:" + strings.Repeat("b", 64)
	recorded := "ghcr.io/poyrazk/base-debian-parent@" + digest
	for _, tc := range []struct {
		name, ref string
		want      bool
	}{
		{"same string", recorded, true},
		{"tag ref of the same repository", "ghcr.io/poyrazk/base-debian-parent:latest", true},
		{"pinned to the recorded digest", "ghcr.io/poyrazk/base-debian-parent@" + digest, true},
		{"pinned with a tag to the recorded digest", "ghcr.io/poyrazk/base-debian-parent:latest@" + digest, true},
		{"pinned to another digest", "ghcr.io/poyrazk/base-debian-parent@" + other, false},
		{"another repository", "ghcr.io/poyrazk/runner-node24:latest", false},
		{"another registry", "registry.example.com/poyrazk/base-debian-parent:latest", false},
		{"unparseable", "::", false},
	} {
		if got := parentSourceMatches(recorded, tc.ref); got != tc.want {
			t.Errorf("%s: parentSourceMatches(%q) = %v, want %v", tc.name, tc.ref, got, tc.want)
		}
	}
}
