//go:build linux

package main

import "os"

// readBuildPlan reads the Railpack plan for buildDevPatchSourceMap. Plans are
// a few KiB; anything implausibly large is refused rather than parsed into
// the durable build result.
func readBuildPlan(name string) ([]byte, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.Size() > 1<<20 {
		return nil, os.ErrInvalid
	}
	return os.ReadFile(name)
}
