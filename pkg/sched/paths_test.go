package sched_test

import (
	"github.com/onebox-faas/faas/pkg/sched"
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
	got := sched.BaseKey("builder")
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
	got := sched.BaseKeyForArch("builder", otherArch)
	want := "base/runner-builder-" + otherArch + ".ext4"
	if got != want {
		t.Fatalf("BaseKeyForArch(%q) = %q; want %q", otherArch, got, want)
	}
	// Two distinct arches must produce two distinct keys —
	// the load-bearing property of the per-arch partition.
	host := sched.BaseKeyForArch("builder", runtime.GOARCH)
	other := sched.BaseKeyForArch("builder", otherArch)
	if host == other {
		t.Fatalf("BaseKeyForArch distinct arches produced identical key %q", host)
	}
}

// ADR-987: the binary runtime has no base of its own; it boots on go124's.
func TestBaseKeyForArch_BinarySharesGo124Base(t *testing.T) {
	if got, want := sched.BaseKeyForArch("binary", "amd64"), sched.BaseKeyForArch("go124", "amd64"); got != want {
		t.Fatalf("BaseKeyForArch(binary) = %q; want go124's %q", got, want)
	}
	if got, want := sched.BaseDigestKeyForArch("binary", "amd64"), sched.BaseDigestKeyForArch("go124", "amd64"); got != want {
		t.Fatalf("BaseDigestKeyForArch(binary) = %q; want go124's %q", got, want)
	}
}

func TestBaseKeyForArch_PlainApp(t *testing.T) {
	// Empty runtime (plain app, not a function app) still gets the
	// per-arch suffix.
	got := sched.BaseKeyForArch("", "amd64")
	if got != "base/base-amd64.ext4" {
		t.Fatalf("BaseKeyForArch(plain, amd64) = %q; want %q", got, "base/base-amd64.ext4")
	}
}

func TestBaseDigestKey_PerArch(t *testing.T) {
	got := sched.BaseDigestKey("builder")
	if !strings.HasSuffix(got, "-"+runtime.GOARCH+".ext4.digest") {
		t.Fatalf("BaseDigestKey(\"builder\") = %q; want suffix %q", got, "-"+runtime.GOARCH+".ext4.digest")
	}
}

func TestBaseDigestKeyForArch_DistinctPerArch(t *testing.T) {
	otherArch := "arm64"
	if runtime.GOARCH == "arm64" {
		otherArch = "amd64"
	}
	host := sched.BaseDigestKeyForArch("builder", runtime.GOARCH)
	other := sched.BaseDigestKeyForArch("builder", otherArch)
	if host == other {
		t.Fatalf("BaseDigestKeyForArch distinct arches produced identical key %q", host)
	}
}

func TestBaseAndDigestKeyMatchPerArch(t *testing.T) {
	// The digest sidecar must live next to the ext4 under the same
	// arch partition — a fresh arm64 install must not nuke an
	// existing amd64 sidecar when both arches share the same root.
	for _, arch := range []string{"amd64", "arm64"} {
		base := sched.BaseKeyForArch("builder", arch)
		digest := sched.BaseDigestKeyForArch("builder", arch)
		if !strings.HasPrefix(digest, base[:len(base)-len(".ext4")]) {
			t.Fatalf("digest key %q does not match base key %q", digest, base)
		}
	}
}

// TestIsParentBaseKey: the gRPC allow-list for MountParentExt4ReadOnly.
// Must accept the canonical parent key for this arch + the sibling
// arch (heterogenous cluster case), and reject everything else.
func TestIsParentBaseKey(t *testing.T) {
	want := sched.BaseKeyForArch(sched.ParentBaseRuntime, runtime.GOARCH)
	if !sched.IsParentBaseKey(want) {
		t.Errorf("IsParentBaseKey(%q) = false, want true (host arch parent key)", want)
	}
	// Sibling arch.
	otherArch := "arm64"
	if runtime.GOARCH == "arm64" {
		otherArch = "amd64"
	}
	sibling := sched.BaseKeyForArch(sched.ParentBaseRuntime, otherArch)
	if !sched.IsParentBaseKey(sibling) {
		t.Errorf("IsParentBaseKey(%q) = false, want true (sibling arch parent key)", sibling)
	}
	for _, bad := range []string{
		"",
		"base/runner-node22-amd64.ext4",
		"base/runner-python312-amd64.ext4",
		"base/base-debian-parent-amd64",               // missing .ext4
		"base/debian-parent-amd64.ext4",               // dropped "runner-" prefix
		"base/runner-base-debian-parent-riscv64.ext4", // unsupported arch
		"layers/foo.ext4",
		"base/base-amd64.ext4",
		"kernel/vmlinux-amd64",
		"snapshots/app-foo/mem.bin",
	} {
		if sched.IsParentBaseKey(bad) {
			t.Errorf("IsParentBaseKey(%q) = true, want false (non-parent key)", bad)
		}
	}
}

// TestAppSidecarLayerKey pins the per-workload storage-key shape
// (issue #463 / ADR-069 / PR-B). Pinned here so the imaged
// handler + vmmd Wake path + cleanupAppFiles all agree on a
// single canonical layout — a regression here would split storage
// across two locations and silently orphan key references.
func TestAppSidecarLayerKey(t *testing.T) {
	cases := []struct {
		slug, depID, sidecarName, want string
	}{
		{
			"my-app",
			"00000000-0000-0000-0000-deadbeefcafe",
			"migrator",
			"apps/my-app/00000000-0000-0000-0000-deadbeefcafe-migrator.ext4",
		},
		{
			"my-app",
			"00000000-0000-0000-0000-deadbeefcafe",
			"scraper",
			"apps/my-app/00000000-0000-0000-0000-deadbeefcafe-scraper.ext4",
		},
	}
	for _, c := range cases {
		got := sched.AppSidecarLayerKey(c.slug, c.depID, c.sidecarName)
		if got != c.want {
			t.Errorf("AppSidecarLayerKey(%q, %q, %q) = %q; want %q",
				c.slug, c.depID, c.sidecarName, got, c.want)
		}
	}
	// Two sidecars on the same deployment produce two distinct keys
	// (the load-bearing property — imaged must not silently
	// overwrite a prior sidecar's ext4).
	if sched.AppSidecarLayerKey("a", "d", "alpha") ==
		sched.AppSidecarLayerKey("a", "d", "beta") {
		t.Errorf("two sidecars collided on the same key")
	}
}
