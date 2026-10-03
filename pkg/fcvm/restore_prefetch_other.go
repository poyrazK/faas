//go:build !linux

package fcvm

import "errors"

var errRestorePrefetchUnsupported = errors.New("restore prefetch: unsupported platform")

func adviseWillNeed(string, []fileRange) error { return errRestorePrefetchUnsupported }

func touchedFileRanges(int, string) ([]fileRange, error) {
	return nil, errRestorePrefetchUnsupported
}
