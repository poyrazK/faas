//go:build !linux

package fcvm

import "errors"

func nativeJailMounts(string) ([]string, error) {
	return nil, errors.New("native recovery: mount proof requires Linux")
}
