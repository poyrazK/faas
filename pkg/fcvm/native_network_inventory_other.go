//go:build !linux

package fcvm

import "errors"

func nativeNetworkInventory([]Lease) error {
	return errors.New("native recovery: network ownership inventory requires Linux")
}
