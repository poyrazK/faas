//go:build linux

package fcvm

import "os"

func nativeJailMounts(root string) ([]string, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return nativeMountsBelow(data, root)
}
