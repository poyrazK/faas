//go:build !linux

package fcvm

import (
	"context"
	"errors"
	"os"
)

func verifyNativeRestoreLoadDescriptor(context.Context, *os.File, nativeImageSourceRecord, nativeSnapshotBackingImage) error {
	return errors.New("native restore load: Linux descriptor proof is required")
}
