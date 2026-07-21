//go:build !windows

package main

// unix TTY probe for the §3.2 stdout-TTY gate. The match for the windows
// build is isatty_windows.go, which always reports false. Keeping the
// platform-specific ioctl dance in a tiny file means the cross-platform
// renderers in output.go stay buildable on every GOOS without dragging
// the unix term package into a Windows build (where it's not needed).

import (
	"os"
	"sync/atomic"

	"golang.org/x/term"
)

// stdoutIsTTY reports whether os.Stdout is attached to a terminal on
// unix-likes. The test seam (testOnlyTTY in output.go) overrides this
// in tests so the captured-buffer path is deterministic regardless of
// how `go test` is invoked.
//
// Cache: the once-set pair (isStdoutTTYOnce, isStdoutTTYVal) is two
// atomic.Bools so reads stay race-clean under future t.Parallel usage.
// The doc on testOnlyTTY in output.go promises the atomic upgrade; this
// is the matching implementation.
func stdoutIsTTY() bool {
	if testOnlyTTY != nil {
		return *testOnlyTTY
	}
	if isStdoutTTYOnce.Load() {
		return isStdoutTTYVal.Load()
	}
	v := term.IsTerminal(int(os.Stdout.Fd()))
	isStdoutTTYVal.Store(v)
	isStdoutTTYOnce.Store(true)
	return v
}

// isStdoutTTYCache holds the once-computed stdout TTY result. The cache
// is intentionally not inverted: a cached "false" is rare in practice
// (tests run non-TTY), and any disagreement heals on the next invocation
// anyway.
var (
	isStdoutTTYOnce atomic.Bool
	isStdoutTTYVal  atomic.Bool
)
