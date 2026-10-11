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
	if nonInteractive {
		return false
	}
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

// stdinIsTTY reports whether os.Stdin is attached to a terminal on
// unix-likes. Same gate logic as stdoutIsTTY — we cache once and let
// the test seam override the result. The CLI uses it to decide
// whether to surface a confirmation prompt (issue #313 zero-config
// flow only prompts on a real TTY).
//
// phase 3 (== repo decomposition) added this; the original file only
// carried stdoutIsTTY and the §3.2 §3.3 §3.4 plan explicitly avoided
// stdin because the early PRs only emitted UX lines, never read
// input. cmdDeployTarball's --yes flag now needs to know whether a
// TTY is available for the new confirmPlan caller in commands_decompose.go.
func stdinIsTTY() bool {
	if nonInteractive {
		return false
	}
	if testOnlyTTY != nil {
		return *testOnlyTTY
	}
	if isStdinTTYOnce.Load() {
		return isStdinTTYVal.Load()
	}
	v := term.IsTerminal(int(os.Stdin.Fd()))
	isStdinTTYVal.Store(v)
	isStdinTTYOnce.Store(true)
	return v
}

// isStdinTTYCache holds the once-computed stdin TTY result. Same
// shape as the stdout cache above.
var (
	isStdinTTYOnce atomic.Bool
	isStdinTTYVal  atomic.Bool
)

// isStdoutTTYCache holds the once-computed stdout TTY result. The cache
// is intentionally not inverted: a cached "false" is rare in practice
// (tests run non-TTY), and any disagreement heals on the next invocation
// anyway.
var (
	isStdoutTTYOnce atomic.Bool
	isStdoutTTYVal  atomic.Bool
)
