//go:build !linux

package fcvm

import "errors"

func anchorAdoptedNetns(string, int, resourceFileIdentity) (*resourceAsset, error) {
	return nil, errors.New("network namespaces are supported only on linux")
}
