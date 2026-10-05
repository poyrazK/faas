//go:build !unix

package hostsize

import "errors"

// FilesystemUsedPct is unavailable without statfs; callers report no data.
func FilesystemUsedPct(string) (float64, error) {
	return 0, errors.New("hostsize: filesystem usage is unavailable on this platform")
}
