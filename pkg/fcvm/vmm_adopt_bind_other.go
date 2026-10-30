//go:build !linux

package fcvm

import (
	"errors"
	"os"
)

// openAdoptedBindTarget needs Linux's /proc and openat2.
func openAdoptedBindTarget(int, string, resourceFileIdentity) (*os.File, error) {
	return nil, errors.New("adopting a re-staged bind source needs Linux")
}
