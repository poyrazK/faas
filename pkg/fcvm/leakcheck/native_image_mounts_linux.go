//go:build linux && metal

package leakcheck

import (
	"fmt"
	"os"
)

func nativeImageMountLeaks() []error {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return []error{fmt.Errorf("native image mount inspection: %w", err)}
	}
	return nativeImageMountsFrom(data)
}
