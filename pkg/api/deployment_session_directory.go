package api

import (
	"fmt"
	"os"
)

// SyncPrivateDeploymentSessionDirectory durably records a local session-file
// rename. The caller owns the private directory. No file contents are read.
func SyncPrivateDeploymentSessionDirectory(path string) error {
	directory, err := os.Open(path) //nolint:forbidigo // Private metadata directory; only stat and fsync use this descriptor.
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("deployment session metadata path is not a directory")
	}
	return directory.Sync()
}
