//go:build !linux && !darwin

package vmmdmount

import (
	"io/fs"
	"os"
)

func ownRuntimeScanView(*os.Root, string) error { return ErrInvalidOverlayPath }

func sameRuntimeScanTargetMetadata(fs.FileInfo, fs.FileInfo) bool { return false }
