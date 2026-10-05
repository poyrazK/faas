//go:build !linux && !darwin

package runtimequota

import "os"

// Operator key provisioning/loading currently supports Linux and macOS only.
func readKeyFile(string, bool, int) ([]byte, error)  { return nil, ErrTrust }
func writeKeyFile(string, []byte, os.FileMode) error { return ErrTrust }
func syncKeyDirectory(string) error                  { return ErrTrust }
