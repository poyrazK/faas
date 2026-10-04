//go:build !linux && !darwin

package fcvm

import "errors"

func nativeLoopPortableDirectoryIdentity(string) (nativeLoopIdentity, error) {
	return nativeLoopIdentity{}, errors.New("native loop mounts require Linux")
}
