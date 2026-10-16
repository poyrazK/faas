package imaged

// adr: 435. Scan bytes must come from a pinned regular artifact, never a link.

import (
	"errors"
	"os"
)

func openStagedScanArtifact(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errScanArtifactMismatch
	}
	f, err := os.Open(path) //nolint:forbidigo // Lstat rejects links and non-regular artifacts; descriptor and path identity are checked before any bytes are read.
	if err != nil {
		return nil, err
	}
	opened, statErr := f.Stat()
	after, pathErr := os.Lstat(path)
	if statErr != nil || pathErr != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, errors.Join(errScanArtifactMismatch, statErr, pathErr, f.Close())
	}
	return f, nil
}
