//go:build !linux

package rootfs

import (
	"os"
	"path/filepath"
)

// Developer builds on non-Linux hosts cannot create Linux overlayfs device
// nodes or xattrs. Materialize the whiteout instead; this preserves the
// historical staging semantics for local tests while production Linux builds
// use the real marker implementation.
func applyOverlayWhiteout(parent, victimName, _ string) error {
	return os.RemoveAll(filepath.Join(parent, victimName))
}

func applyOverlayOpaque(dir string) error {
	return clearDir(dir)
}
