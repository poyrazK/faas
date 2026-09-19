package imaged

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// digestScanArtifact hashes the exact ext4 blob handed to Grype. Remote
// storage is staged as a directory containing rootfs.ext4; local storage
// returns the ext4 path directly. The hash is evidence about the bytes that
// were selected for scanning, not a replacement for the OCI image digest.
func digestScanArtifact(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat scan artifact: %w", err)
	}
	if info.IsDir() {
		path = filepath.Join(path, "rootfs.ext4")
		info, err = os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("stat staged rootfs: %w", err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("scan artifact %q is a directory", path)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open scan artifact: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash scan artifact: %w", err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
