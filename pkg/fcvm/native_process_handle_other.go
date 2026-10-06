//go:build !linux

package fcvm

import "errors"

func openNativeProcess(_ int) (nativeProcessHandle, error) {
	return nil, errors.New("native process recovery requires Linux pidfd support")
}

func nativeKernelBootID() (string, error) {
	return "", errors.New("native process recovery requires a Linux kernel boot identity")
}
