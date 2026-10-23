//go:build !linux

package bridgeproc

import "errors"

// Bridges run only on Linux. Elsewhere the checks pass when nothing asks for
// them, so the bridge packages still build and unit-test. A requested
// credential check cannot be honoured, so it fails closed.
func harden(getenv func(string) string) error {
	if getenv(ExpectUIDEnv) != "" {
		return errors.New("bridge credential checks are supported only on linux")
	}
	return nil
}
