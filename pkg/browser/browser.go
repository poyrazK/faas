// Package browser launches URLs in the user's OS-default browser.
//
// Slice 9 of M7.5 wires `faas open <app-slug>` and `faas connect
// github` to a real OS-handler instead of printing a URL and
// expecting the user to paste it. Per UX spec §3.4 ("the CLI opens
// it for you — no copy-paste of magic links"), this is the canonical
// launch helper.
//
// We shell out to the platform opener rather than linking CGO
// against libgtk / AppKit / shdocvw. The dispatch is:
//   - linux  → xdg-open (the freedesktop standard)
//   - darwin → open  (the macOS CLI)
//   - windows → start (cmd.exe built-in)
//
// On unknown GOOS we return an ErrUnsupported so callers can fall
// back to printing the URL.
package browser

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

// ErrUnsupported is returned when runtime.GOOS is not one we know
// how to launch a browser from. The CLI's expected fallback is to
// print the URL.
var ErrUnsupported = fmt.Errorf("browser: unsupported OS %q", runtime.GOOS)

// Launcher abstracts the exec call so tests can substitute a
// recorder. The real impl is defaultLauncher.
type Launcher interface {
	// Launch opens url in the OS-default handler. It returns when
	// the opener has been started; it does not wait for the
	// browser to exit (returning earlier is the expected
	// behavior on every supported platform).
	Launch(url string) error
}

// defaultLauncher shells out to xdg-open / open / start.
type defaultLauncher struct{}

// Launch runs the platform-specific opener.
func (defaultLauncher) Launch(url string) error {
	if url == "" {
		return errors.New("browser: empty url")
	}
	name, args := opener()
	if name == "" {
		return ErrUnsupported
	}
	cmd := exec.Command(name, append(args, url)...) //nolint:gosec // URL is operator-supplied
	return cmd.Start()
}

// opener returns (program, args-before-url) for the current OS.
func opener() (string, []string) {
	switch runtime.GOOS {
	case "linux":
		return "xdg-open", nil
	case "darwin":
		return "open", nil
	case "windows":
		// `start` is a cmd.exe builtin, so we route through cmd /c.
		return "cmd", []string{"/c", "start"}
	default:
		return "", nil
	}
}

// Default is the package-level launcher used by the CLI. Tests
// substitute their own.
var Default Launcher = defaultLauncher{}

// Open is the convenience wrapper: Default.Launch(url). Callers
// that don't need a swappable launcher use this directly.
func Open(url string) error { return Default.Launch(url) }