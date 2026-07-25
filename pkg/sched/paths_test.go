package sched

import (
	"runtime"
	"strings"
	"testing"
)

// Issue #197 B3.3: BaseKey / BaseDigestKey must be partitioned by arch so
// an arm64 imaged binary can stage its base alongside an amd64 imaged
// binary on the same storage root without overwriting either. Each helper
// also has the per-arch form (BaseKeyForArch / BaseDigestKeyForArch) for
// callers that already have the arch in context (e.g. multi-arch fleets).

func TestBaseKey_PerArch(t *testing.T) {
	// The thin wrapper pins arch to runtime.GOARCH; the result must
	// embed the host arch so the key is stable across calls.
	got := BaseKey("builder")
	if !strings.HasSuffix(got, "-"+runtime.GOARCH+".ext4") {
		t.Fatalf("BaseKey(\"builder\") = %q; want suffix %q", got, "-"+runtime.GOARCH+".ext4")
	}
	if !strings.HasPrefix(got, "base/runner-builder-") {
		t.Fatalf("BaseKey(\"builder\") = %q; want prefix %q", got, "base/runner-builder-")
	}
}

func TestBaseKeyForArch_DistinctPerArch(t *testing.T) {
	// Pick an arch that's NOT the host's — the test runs on whichever
	// arch the CI runner is on (amd64 on EX44, arm64 on Mac/Lima).
	otherArch := "arm64"
	if runtime.GOARCH == "arm64" {
		otherArch = "amd64"
	}
	// Sanity: the "other" arch must produce a known-good key.
	got := BaseKeyForArch("builder", otherArch)
	want := "base/runner-builder-" + otherArch + ".ext4"
	if got != want {
		t.Fatalf("BaseKeyForArch(%q) = %q; want %q", otherArch, got, want)
	}
	// Two distinct arches must produce two distinct keys —
	// the load-bearing property of the per-arch partition.
	host := BaseKeyForArch("builder", runtime.GOARCH)
	other := BaseKeyForArch("builder", otherArch)
	if host == other {
		t.Fatalf("BaseKeyForArch distinct arches produced identical key %q", host)
	}
}

func TestBaseKeyForArch_PlainApp(t *testing.T) {
	// Empty runtime (plain app, not a function app) still gets the
	// per-arch suffix.
	got := BaseKeyForArch("", "amd64")
	if got != "base/base-amd64.ext4" {
		t.Fatalf("BaseKeyForArch(plain, amd64) = %q; want %q", got, "base/base-amd64.ext4")
	}
}

func TestBaseDigestKey_PerArch(t *testing.T) {
	got := BaseDigestKey("builder")
	if !strings.HasSuffix(got, "-"+runtime.GOARCH+".ext4.digest") {
		t.Fatalf("BaseDigestKey(\"builder\") = %q; want suffix %q", got, "-"+runtime.GOARCH+".ext4.digest")
	}
}

func TestBaseDigestKeyForArch_DistinctPerArch(t *testing.T) {
	otherArch := "arm64"
	if runtime.GOARCH == "arm64" {
		otherArch = "amd64"
	}
	host := BaseDigestKeyForArch("builder", runtime.GOARCH)
	other := BaseDigestKeyForArch("builder", otherArch)
	if host == other {
		t.Fatalf("BaseDigestKeyForArch distinct arches produced identical key %q", host)
	}
}

func TestBaseAndDigestKeyMatchPerArch(t *testing.T) {
	// The digest sidecar must live next to the ext4 under the same
	// arch partition — a fresh arm64 install must not nuke an
	// existing amd64 sidecar when both arches share the same root.
	for _, arch := range []string{"amd64", "arm64"} {
		base := BaseKeyForArch("builder", arch)
		digest := BaseDigestKeyForArch("builder", arch)
		if !strings.HasPrefix(digest, base[:len(base)-len(".ext4")]) {
			t.Fatalf("digest key %q does not match base key %q", digest, base)
		}
	}
}
