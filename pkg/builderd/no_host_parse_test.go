package builderd_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// hostDriveParsers are the tools that parse an ext4 image in the host
// process. ADR-20261025090100 keeps every guest-written drive out of the
// host's filesystem code: pkg/guestdisk runs them sandboxed for vmmd, and
// builderd must not run them on a build's drive (bug hunt H104).
var hostDriveParsers = regexp.MustCompile(`exec\.Command(?:Context)?\([^"]*"(debugfs|e2fsck|mount|umount|fsck\.ext4|tune2fs|resize2fs)"`)

// hostDriveParseAllowed lists the existing calls, each with why it is safe
// or how it is fenced. A new call fails this test until it is reviewed here.
var hostDriveParseAllowed = map[string]string{
	// prepareCacheExportDrive's journal replay and file rewrites run only on
	// the deferred cache export path, which ParseDeferCacheExport refuses to
	// enable (H104) until the inputs reach the guest without a host edit.
	"vm_metal.go:e2fsck":  "deferred cache export only; switch refused (H104)",
	"vm_metal.go:debugfs": "deferred cache export and warm builders only; both off (H104, DisableWarmBuilders)",
	// Test-only wrappers that re-mount a drive CreateBuildDrive1 produced
	// from a host directory, before any guest has written to it.
	"mount_helpers.go:mount":  "tests only, builderd's own fresh drive",
	"mount_helpers.go:umount": "tests only, builderd's own fresh drive",
}

func TestBuilderdDoesNotParseGuestDrivesOnTheHost(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range hostDriveParsers.FindAllStringSubmatch(string(src), -1) {
			key := file + ":" + m[1]
			seen[key] = true
			if _, ok := hostDriveParseAllowed[key]; !ok {
				t.Errorf("%s runs %s on the host; a build's drive is guest-written, so parse it only through pkg/guestdisk's sandbox (ADR-20261025090100, bug hunt H104)", file, m[1])
			}
		}
	}
	// A stale entry means the pattern no longer finds the call, which would
	// let a new one through unnoticed; drop the entry when its call goes.
	for key := range hostDriveParseAllowed {
		if !seen[key] {
			t.Errorf("allowed host parse %s no longer found; remove it from hostDriveParseAllowed", key)
		}
	}
}
