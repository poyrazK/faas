package e2etest

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// GrypeReplayEnv turns off the grype replay wrapper when set to "0".
const GrypeReplayEnv = "FAAS_E2E_GRYPE_REPLAY"

// grypeReplayScript replays grype's JSON for a directory whose files match an
// earlier scan byte for byte, against unchanged vulnerability database content.
// Any other invocation (version, db status, a non-directory source), a missing
// database directory and any listing error run grype. A failed scan is never
// kept.
const grypeReplayScript = `#!/bin/sh
# Written by pkg/e2etest (grype_replay.go) for one test process.
set -u
real='@REAL@'
cache='@CACHE@'
if [ "$#" -ne 3 ] || [ "$2" != "-o" ] || [ "$3" != "json" ]; then
	exec "$real" "$@"
fi
case "$1" in
dir:*) src=${1#dir:} ;;
*) exec "$real" "$@" ;;
esac
[ -d "$src" ] || exec "$real" "$@"
# grype's database directory, resolved as grype resolves it. "grype db
# status" would name it too, but it checksums the whole database: minutes.
dbroot=${GRYPE_DB_CACHE_DIR:-${XDG_CACHE_HOME:-${HOME:-/nonexistent}/.cache}/grype/db}
[ -d "$dbroot" ] || exec "$real" "$@"
# The key covers the database's content files (path, size, modification
# time) and the tree's every entry (path, type, mode, size, link target) and
# file digest. grype rewrites last_update_check on every run, so only the
# files an update replaces count. Any error while listing them means no key.
work=$(mktemp -d "$cache/.work.XXXXXX") || exec "$real" "$@"
key=
if find "$dbroot" -type f \( -name vulnerability.db -o -name import.json -o -name metadata.json \) \
	-printf '%P\t%s\t%T@\n' 2>"$work/err" | LC_ALL=C sort >"$work/db" &&
	[ -s "$work/db" ] &&
	(cd "$src" &&
		find . -printf '%p\t%y\t%m\t%s\t%l\n' 2>>"$work/err" | LC_ALL=C sort >"$work/tree" &&
		find . -type f -print0 2>>"$work/err" | LC_ALL=C sort -z | xargs -0r sha256sum >"$work/sums" 2>>"$work/err") &&
	[ ! -s "$work/err" ]; then
	key=$(cat "$work/db" "$work/tree" "$work/sums" | sha256sum | cut -c1-64)
fi
rm -rf "$work"
[ -n "$key" ] || exec "$real" "$@"
hit="$cache/$key.json"
if [ -s "$hit" ]; then
	exec cat "$hit"
fi
tmp=$(mktemp "$cache/.scan.XXXXXX") || exec "$real" "$@"
"$real" "$@" >"$tmp"
rc=$?
if [ "$rc" -eq 0 ]; then
	mv -f "$tmp" "$hit" && exec cat "$hit"
fi
cat "$tmp"
rm -f "$tmp"
exit "$rc"
`

var grypeReplay struct {
	once sync.Once
	bin  string
	err  error
}

// grypeReplayBin returns a grype for imaged (FAAS_GRYPE_BIN) that replays the
// result of an earlier scan of byte-identical files, or "" when there is no
// grype to wrap or GrypeReplayEnv is "0".
//
// Production scans a shared base once and keeps the result in the database
// (pkg/imaged/base_image_scan.go). Each test starts a fresh database, so every
// test re-scanned the same bases before its first deployment could boot: 13 s
// for the minimal base on a quiet native host, up to 187 s on a loaded one,
// against 60-75 s deploy deadlines (containers lane, 2026-10-29). App layers
// carry new bytes, so their scans still run grype.
func grypeReplayBin(t testing.TB) string {
	t.Helper()
	if os.Getenv(GrypeReplayEnv) == "0" || runtime.GOOS != "linux" {
		return ""
	}
	grypeReplay.once.Do(func() {
		grypeReplay.bin, grypeReplay.err = writeGrypeReplay(os.TempDir())
	})
	if grypeReplay.err != nil {
		t.Logf("e2etest: grype replay unavailable; imaged runs grype directly: %v", grypeReplay.err)
	}
	return grypeReplay.bin
}

// writeGrypeReplay writes the wrapper and its cache under a per-process
// directory in root and returns the wrapper's path.
func writeGrypeReplay(root string) (string, error) {
	real := os.Getenv("FAAS_GRYPE_BIN")
	if real == "" {
		found, err := exec.LookPath("grype")
		if errors.Is(err, exec.ErrNotFound) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		real = found
	}
	real, err := filepath.Abs(real)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, fmt.Sprintf("faas-e2e-grype-replay-%d", os.Getpid()))
	cache := filepath.Join(dir, "cache")
	if strings.ContainsRune(real, '\'') || strings.ContainsRune(cache, '\'') {
		return "", fmt.Errorf("grype path %q or cache %q cannot be quoted", real, cache)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", err
	}
	script := strings.NewReplacer("@REAL@", real, "@CACHE@", cache).Replace(grypeReplayScript)
	bin := filepath.Join(dir, "grype")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		return "", err
	}
	return bin, nil
}
