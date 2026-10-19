// Package overlaymetadata reads only the guest's supported overlay metadata.
// The builder, guest and native scanner share this namespace and root policy.
package overlaymetadata

import (
	"errors"
	"fmt"
	"os"
)

const RootOpaqueXattr = "trusted.overlay.opaque"

var (
	ErrAbsent      = errors.New("overlaymetadata: attribute absent")
	ErrInvalid     = errors.New("overlaymetadata: invalid root metadata")
	ErrUnsupported = errors.New("overlaymetadata: native metadata unavailable")
)

func ReadRootOpacity(root string) (bool, error) {
	return readRootOpacity(root, readNativeRootXattr)
}

func readRootOpacity(root string, read func(string, string, []byte) (int, error)) (bool, error) {
	before, err := os.Lstat(root)
	if err != nil || !before.IsDir() {
		return false, errors.Join(ErrInvalid, err)
	}
	var value [2]byte // The supported marker is exactly one byte.
	n, err := read(root, RootOpaqueXattr, value[:])
	if err != nil && !errors.Is(err, ErrAbsent) {
		return false, fmt.Errorf("overlaymetadata: read guest root opacity: %w", err)
	}
	after, statErr := os.Lstat(root)
	if statErr != nil || !os.SameFile(before, after) {
		return false, errors.Join(ErrInvalid, statErr)
	}
	if errors.Is(err, ErrAbsent) {
		return false, nil
	}
	if n != 1 || value[0] != 'y' && value[0] != 'x' {
		return false, ErrInvalid
	}
	return value[0] == 'y', nil
}

// GuestLowerDirectory keeps root-opacity handling explicit: the kernel treats
// an overlay root as merged even when the upper root carries an opaque xattr.
// The caller creates and verifies the empty lower directory before mounting.
func GuestLowerDirectory(upper, base, empty string) (string, error) {
	opaque, err := ReadRootOpacity(upper)
	if err != nil {
		return "", err
	}
	if opaque {
		return empty, nil
	}
	return base, nil
}

// ReadOnlyLowerDirectories retains whiteout interpretation even when the
// opaque root excludes the base: the scanner mounts an overlay over the upper.
func ReadOnlyLowerDirectories(upper, base string) ([]string, error) {
	opaque, err := ReadRootOpacity(upper)
	if err != nil {
		return nil, err
	}
	if opaque {
		return []string{upper}, nil
	}
	return []string{upper, base}, nil
}
