//go:build !linux

package internal

import "os"

// ProcessResourceUsage is unavailable outside Linux, where the runner's
// one-shot process statistics are not collected by this build.
func ProcessResourceUsage(_ *os.ProcessState) GuestProcessUsage {
	return GuestProcessUsage{}
}
