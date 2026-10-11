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
func applyOverlayWhiteout(parent, victimName string, markerPaths ...string) error {
	if err := os.RemoveAll(filepath.Join(parent, victimName)); err != nil {
		return err
	}
	if len(markerPaths) > 0 && markerPaths[0] != "" && markerPaths[0] != filepath.Join(parent, victimName) {
		return os.RemoveAll(markerPaths[0])
	}
	return nil
}

func applyOverlayOpaque(dir string) error {
	return clearDir(dir)
}

func replaceOverlayWhiteout(string, bool) error {
	return nil
}
