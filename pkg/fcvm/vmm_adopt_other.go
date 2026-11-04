//go:build !linux

package fcvm

import (
	"errors"
	"os"
)

// Guests run only on Linux.
func openAdoptedProcess(resourceProcessIdentity) (*os.Process, func(), error) {
	return nil, nil, errors.New("adoption is supported only on linux")
}
