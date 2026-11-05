//go:build !linux

package storage

import "context"

// CheckPrivateExclusiveRoot needs Linux openat2 and the exclusive writer.
func CheckPrivateExclusiveRoot(string) error { return ErrExclusivePutUnsupported }

// DeleteExclusive needs Linux openat2 and the exclusive writer.
func (l *LocalStorageBackend) DeleteExclusive(context.Context, string, string) error {
	return ErrExclusivePutUnsupported
}
