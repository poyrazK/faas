//go:build !linux

package imaged

import "errors"

// mountOverlaySyscall is a no-op stub on non-linux platforms so
// the package compiles for unit tests on macOS dev boxes. The
// real path is in mount_overlay_linux.go.
func mountOverlaySyscall(merged, lowerdir, upperdir, workdir string) error {
	return errors.New("imaged: mountOverlay: only implemented on linux")
}

// umountOverlaySyscall is the non-linux stub.
func umountOverlaySyscall(merged string) error {
	return errors.New("imaged: umountOverlay: only implemented on linux")
}
