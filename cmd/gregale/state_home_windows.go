//go:build windows

package main

import "io/fs"

// checkPrivateStateDir has nothing to check on Windows, where the temp
// directory is already per user.
func checkPrivateStateDir(string, fs.FileInfo) error { return nil }
